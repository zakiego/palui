// safeFetch: the only way agent-steered code fetches a URL (PLAN 10). On top of
// the static checks in ssrf.ts it:
//   - resolves DNS itself and refuses a host if any address is internal,
//   - pins the connection to the address it checked (node:http `lookup`), so a
//     second DNS answer can't rebind the socket to 127.0.0.1,
//   - follows redirects by hand (max 5) and re-runs every check on each hop,
//   - caps the decoded body size (gzip bombs count after decompression),
//   - enforces an overall timeout and an optional content type allowlist.
import http from "node:http"
import https from "node:https"
import type { LookupFunction } from "node:net"
import { Readable } from "node:stream"
import zlib from "node:zlib"
import { assertPublicUrl, hostOf, resolvePublic, SsrfError, systemResolver, type ResolvedAddress, type Resolver, type SsrfErrorCode } from "./ssrf"

export type SafeFetchErrorCode = SsrfErrorCode | "too_many_redirects" | "timeout" | "aborted" | "too_large" | "content_type" | "network"

export class SafeFetchError extends Error {
  constructor(
    readonly code: SafeFetchErrorCode,
    message: string,
  ) {
    super(message)
    this.name = "SafeFetchError"
  }
}

// --- Transport ------------------------------------------------------------------------

export type TransportRequest = {
  url: URL
  // The address the guard approved. The transport must connect to this, never re-resolve.
  address: ResolvedAddress
  method: string
  headers: Record<string, string>
  body?: string
  signal: AbortSignal
}

export type TransportResponse = {
  status: number
  headers: Headers
  // Decoded (decompressed) body chunks.
  body: AsyncIterable<Uint8Array>
  // Drop the body without reading it (redirects, refused content types).
  cancel(): void
}

export type Transport = (req: TransportRequest) => Promise<TransportResponse>

function toHeaders(raw: http.IncomingHttpHeaders): Headers {
  const h = new Headers()
  for (const [k, v] of Object.entries(raw)) {
    if (v === undefined) continue
    if (Array.isArray(v)) for (const one of v) h.append(k, one)
    else h.set(k, v)
  }
  return h
}

function decoder(encoding: string | null): NodeJS.ReadWriteStream | null {
  switch ((encoding ?? "").trim().toLowerCase()) {
    case "gzip":
    case "x-gzip":
      return zlib.createGunzip()
    case "deflate":
      return zlib.createInflate()
    case "br":
      return zlib.createBrotliDecompress()
    default:
      return null
  }
}

// node:http(s) with the socket pinned to the approved address. TLS still
// verifies the certificate against the hostname (servername), so pinning
// doesn't weaken HTTPS.
export const httpTransport: Transport = (req) =>
  new Promise((resolve, reject) => {
    const { url, address } = req
    const lookup: LookupFunction = (_hostname, options, callback) => {
      if (options && (options as { all?: boolean }).all) {
        ;(callback as unknown as (err: null, addrs: { address: string; family: number }[]) => void)(null, [address])
      } else {
        callback(null, address.address, address.family)
      }
    }
    const isHttps = url.protocol === "https:"
    const host = hostOf(url)
    const headers = { "accept-encoding": "gzip, deflate, br", ...req.headers }
    const request = (isHttps ? https : http).request(
      {
        protocol: url.protocol,
        hostname: host,
        port: url.port || (isHttps ? 443 : 80),
        path: `${url.pathname}${url.search}`,
        method: req.method,
        headers,
        signal: req.signal,
        lookup,
        agent: false,
        ...(isHttps ? { servername: host } : {}),
      },
      (res) => {
        const h = toHeaders(res.headers)
        const dec = decoder(h.get("content-encoding"))
        const stream: Readable = dec ? (res.pipe(dec) as unknown as Readable) : res
        if (dec) res.on("error", (e) => (dec as unknown as Readable).destroy(e))
        resolve({
          status: res.statusCode ?? 0,
          headers: h,
          body: stream,
          cancel: () => {
            res.destroy()
            if (dec) (dec as unknown as Readable).destroy()
          },
        })
      },
    )
    request.on("error", reject)
    if (req.body !== undefined) request.write(req.body)
    request.end()
  })

// --- safeFetch ------------------------------------------------------------------------

export type SafeFetchOptions = {
  method?: "GET" | "HEAD" | "POST"
  headers?: Record<string, string>
  body?: string
  // Overall budget across every redirect hop. Default 15 s.
  timeoutMs?: number
  // Decoded body cap. Default 5 MB.
  maxBytes?: number
  // true: stop reading at maxBytes and mark the response truncated. false (default): throw too_large.
  truncate?: boolean
  // Default 5.
  maxRedirects?: number
  // Media types allowed on a 2xx answer: "text/html", "text/*", "application/json",
  // "+json" (any structured JSON suffix). Unset allows everything.
  allowedContentTypes?: string[]
  signal?: AbortSignal
  resolver?: Resolver
  transport?: Transport
}

export type SafeResponse = {
  // Final URL after redirects.
  url: string
  status: number
  ok: boolean
  headers: Headers
  // Media type without parameters, lowercased ("text/html").
  contentType: string
  charset: string | null
  bytes: Uint8Array
  truncated: boolean
  // Every URL visited before the final one.
  redirects: string[]
  text(): string
  json<T = unknown>(): T
}

export const DEFAULT_USER_AGENT = "PaluiAgent/1.0 (+https://palui.zakiego.com)"
const REDIRECTS = new Set([301, 302, 303, 307, 308])

export function parseContentType(header: string | null): { type: string; charset: string | null } {
  if (!header) return { type: "", charset: null }
  const [type, ...params] = header.split(";")
  let charset: string | null = null
  for (const p of params) {
    const m = /^\s*charset\s*=\s*"?([^";\s]+)"?/i.exec(p)
    if (m?.[1]) charset = m[1].toLowerCase()
  }
  return { type: (type ?? "").trim().toLowerCase(), charset }
}

export function contentTypeAllowed(type: string, allowed: string[]): boolean {
  return allowed.some((a) => {
    const rule = a.toLowerCase()
    if (rule.startsWith("+")) return type.endsWith(rule)
    if (rule.endsWith("/*")) return type.startsWith(rule.slice(0, -1))
    return type === rule
  })
}

function decodeText(bytes: Uint8Array, charset: string | null): string {
  try {
    return new TextDecoder(charset ?? "utf-8").decode(bytes)
  } catch {
    return new TextDecoder("utf-8").decode(bytes)
  }
}

async function readBody(res: TransportResponse, maxBytes: number, truncate: boolean): Promise<{ bytes: Uint8Array; truncated: boolean }> {
  const chunks: Uint8Array[] = []
  let size = 0
  let truncated = false
  for await (const chunk of res.body) {
    if (size + chunk.byteLength > maxBytes) {
      if (!truncate) {
        res.cancel()
        throw new SafeFetchError("too_large", `Response is larger than ${maxBytes} bytes`)
      }
      chunks.push(chunk.subarray(0, maxBytes - size))
      size = maxBytes
      truncated = true
      res.cancel()
      break
    }
    chunks.push(chunk)
    size += chunk.byteLength
  }
  const out = new Uint8Array(size)
  let offset = 0
  for (const c of chunks) {
    out.set(c, offset)
    offset += c.byteLength
  }
  return { bytes: out, truncated }
}

export async function safeFetch(input: string | URL, opts: SafeFetchOptions = {}): Promise<SafeResponse> {
  const resolver = opts.resolver ?? systemResolver
  const transport = opts.transport ?? httpTransport
  const maxBytes = opts.maxBytes ?? 5 * 1024 * 1024
  const maxRedirects = opts.maxRedirects ?? 5
  const timeout = AbortSignal.timeout(opts.timeoutMs ?? 15_000)
  const signal = opts.signal ? AbortSignal.any([timeout, opts.signal]) : timeout

  let method: string = opts.method ?? "GET"
  let body = opts.body
  let headers: Record<string, string> = { "user-agent": DEFAULT_USER_AGENT, accept: "*/*", ...lowerKeys(opts.headers ?? {}) }
  const redirects: string[] = []
  let current = typeof input === "string" ? input : input.href

  const fail = (err: unknown): never => {
    if (err instanceof SafeFetchError) throw err
    if (err instanceof SsrfError) throw new SafeFetchError(err.code, err.message)
    if (timeout.aborted) throw new SafeFetchError("timeout", `Timed out after ${opts.timeoutMs ?? 15_000} ms`)
    if (signal.aborted) throw new SafeFetchError("aborted", "Request was aborted")
    throw new SafeFetchError("network", (err as Error)?.message ?? String(err))
  }

  try {
    for (let hop = 0; ; hop++) {
      const url = assertPublicUrl(current)
      const address = await resolvePublic(hostOf(url), resolver)
      if (signal.aborted) throw signal.reason
      const res = await transport({ url, address, method, headers, body, signal })

      const location = res.headers.get("location")
      if (REDIRECTS.has(res.status) && location) {
        res.cancel()
        if (hop >= maxRedirects) throw new SafeFetchError("too_many_redirects", `More than ${maxRedirects} redirects`)
        const next = new URL(location, url)
        redirects.push(url.href)
        // 303, and 301/302 after a POST, switch to GET without a body (fetch spec).
        if (res.status === 303 || ((res.status === 301 || res.status === 302) && method === "POST")) {
          method = "GET"
          body = undefined
          const { "content-type": _ct, "content-length": _cl, ...rest } = headers
          headers = rest
        }
        // Never carry credentials to another origin.
        if (next.origin !== url.origin) {
          const { authorization: _a, cookie: _c, ...rest } = headers
          headers = rest
        }
        current = next.href
        continue
      }

      const { type, charset } = parseContentType(res.headers.get("content-type"))
      const ok = res.status >= 200 && res.status < 300
      if (ok && opts.allowedContentTypes && !contentTypeAllowed(type, opts.allowedContentTypes)) {
        res.cancel()
        throw new SafeFetchError("content_type", `Content type ${type || "(none)"} is not allowed`)
      }
      const declared = Number(res.headers.get("content-length") ?? "")
      if (!opts.truncate && Number.isFinite(declared) && declared > maxBytes && !res.headers.get("content-encoding")) {
        res.cancel()
        throw new SafeFetchError("too_large", `Response is larger than ${maxBytes} bytes`)
      }
      const read = method === "HEAD" ? { bytes: new Uint8Array(), truncated: false } : await readBody(res, maxBytes, opts.truncate ?? false)
      if (method === "HEAD") res.cancel()
      return {
        url: url.href,
        status: res.status,
        ok,
        headers: res.headers,
        contentType: type,
        charset,
        bytes: read.bytes,
        truncated: read.truncated,
        redirects,
        text: () => decodeText(read.bytes, charset),
        json: <T>() => JSON.parse(decodeText(read.bytes, charset)) as T,
      }
    }
  } catch (err) {
    return fail(err)
  }
}

function lowerKeys(h: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(h)) out[k.toLowerCase()] = v
  return out
}

// Re-exported so callers need one import.
export { assertPublicUrl, blockedAddressReason, isBlockedAddress, resolvePublic, SsrfError } from "./ssrf"
export type { ResolvedAddress, Resolver } from "./ssrf"

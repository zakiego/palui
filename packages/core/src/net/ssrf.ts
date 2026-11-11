// SSRF guard (PLAN 10): every fetch the agent can steer goes through here. A URL
// a model supplies is a request this server makes on its behalf, so we only
// allow http(s), refuse credentials in the URL, and refuse any address that only
// means something from inside this machine or its network. Lessons from
// utusan's kirim.ts: check the resolved address, not the hostname string, and
// never let a redirect walk past the check.
import { isIP } from "node:net"

export type SsrfErrorCode =
  | "invalid_url"
  | "blocked_protocol"
  | "blocked_credentials"
  | "blocked_host"
  | "blocked_address"
  | "blocked_port"
  | "dns"

export class SsrfError extends Error {
  constructor(
    readonly code: SsrfErrorCode,
    message: string,
  ) {
    super(message)
    this.name = "SsrfError"
  }
}

// Ports that speak something other than HTTP and have a history of being
// abused through SSRF (mail, databases, caches). Mirrors the fetch spec's
// "bad ports" idea for the ones that matter on a self-hosted box.
const BLOCKED_PORTS = new Set([
  0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117,
  119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636,
  989, 990, 993, 995, 1719, 1720, 1723, 2049, 3306, 3659, 4045, 5060, 5061, 5432, 6000, 6379, 6566, 6665, 6666, 6667, 6668, 6669, 6697,
  10080, 11211, 27017,
])

// --- IPv4 -----------------------------------------------------------------------------

function ipv4ToInt(ip: string): number | null {
  const parts = ip.split(".")
  if (parts.length !== 4) return null
  let n = 0
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p)) return null
    const v = Number(p)
    if (v > 255) return null
    n = n * 256 + v
  }
  return n
}

const V4_BLOCKS: [string, number, string][] = [
  ["0.0.0.0", 8, "this network"],
  ["10.0.0.0", 8, "private"],
  ["100.64.0.0", 10, "carrier-grade NAT"],
  ["127.0.0.0", 8, "loopback"],
  ["169.254.0.0", 16, "link-local (cloud metadata)"],
  ["172.16.0.0", 12, "private"],
  ["192.0.0.0", 24, "IETF protocol assignments"],
  ["192.0.2.0", 24, "documentation"],
  ["192.88.99.0", 24, "6to4 relay"],
  ["192.168.0.0", 16, "private"],
  ["198.18.0.0", 15, "benchmarking"],
  ["198.51.100.0", 24, "documentation"],
  ["203.0.113.0", 24, "documentation"],
  ["224.0.0.0", 4, "multicast"],
  ["240.0.0.0", 4, "reserved"],
]

const V4_RANGES = V4_BLOCKS.map(([base, bits, why]) => {
  const start = ipv4ToInt(base) as number
  const size = 2 ** (32 - bits)
  return { start, end: start + size - 1, why }
})

function blockedV4(ip: string): string | null {
  const n = ipv4ToInt(ip)
  if (n === null) return "unparseable IPv4 address"
  for (const r of V4_RANGES) if (n >= r.start && n <= r.end) return r.why
  return null
}

// --- IPv6 -----------------------------------------------------------------------------

// Expand an IPv6 address into 8 16-bit groups. Handles "::" and a trailing
// dotted IPv4 part (::ffff:1.2.3.4). Zone ids (%eth0) are dropped.
function ipv6Groups(ip: string): number[] | null {
  let s = ip.toLowerCase().replace(/^\[|\]$/g, "")
  const zone = s.indexOf("%")
  if (zone >= 0) s = s.slice(0, zone)
  let tail: number[] = []
  const lastColon = s.lastIndexOf(":")
  if (s.includes(".")) {
    const v4 = ipv4ToInt(s.slice(lastColon + 1))
    if (v4 === null) return null
    tail = [Math.floor(v4 / 65536), v4 % 65536]
    // Keep a trailing "::" intact, drop a single trailing ":".
    s = s.slice(0, lastColon + 1)
    if (!s.endsWith("::")) s = s.slice(0, -1)
  }
  const halves = s.split("::")
  if (halves.length > 2) return null
  const parse = (part: string) => (part === "" ? [] : part.split(":").map((h) => (/^[0-9a-f]{1,4}$/.test(h) ? Number.parseInt(h, 16) : Number.NaN)))
  const head = parse(halves[0] ?? "")
  const rest = halves.length === 2 ? parse(halves[1] ?? "") : []
  const known = head.length + rest.length + tail.length
  if (head.some(Number.isNaN) || rest.some(Number.isNaN)) return null
  if (halves.length === 1) {
    if (known !== 8) return null
    return [...head, ...tail]
  }
  if (known > 7) return null
  return [...head, ...new Array(8 - known).fill(0), ...rest, ...tail]
}

function v4FromGroups(hi: number, lo: number): string {
  return [hi >> 8, hi & 255, lo >> 8, lo & 255].join(".")
}

function blockedV6(ip: string): string | null {
  const g = ipv6Groups(ip)
  if (!g) return "unparseable IPv6 address"
  const [g0, g1, g2, g3, g4, g5, g6, g7] = g as [number, number, number, number, number, number, number, number]
  const zeroPrefix = g0 === 0 && g1 === 0 && g2 === 0 && g3 === 0 && g4 === 0
  if (zeroPrefix && g5 === 0 && g6 === 0 && g7 === 0) return "unspecified"
  if (zeroPrefix && g5 === 0 && g6 === 0 && g7 === 1) return "loopback"
  // IPv4-mapped (::ffff:a.b.c.d) and the deprecated IPv4-compatible (::a.b.c.d):
  // judge the embedded IPv4 address.
  if (zeroPrefix && (g5 === 0xffff || g5 === 0)) {
    const inner = blockedV4(v4FromGroups(g6, g7))
    return inner ? `IPv4-mapped ${inner}` : null
  }
  // IPv4-translated (::ffff:0:a.b.c.d, RFC 2765) embeds one the same way.
  if (g0 === 0 && g1 === 0 && g2 === 0 && g3 === 0 && g4 === 0xffff && g5 === 0) {
    const inner = blockedV4(v4FromGroups(g6, g7))
    return inner ? `IPv4-translated ${inner}` : null
  }
  // Local-use NAT64 (64:ff9b:1::/48, RFC 8215) can put the IPv4 address anywhere in
  // the suffix and only routes inside a network: block the whole prefix.
  if (g0 === 0x64 && g1 === 0xff9b && g2 === 1) return "local-use NAT64 (64:ff9b:1::/48)"
  // NAT64 (64:ff9b::/96) embeds an IPv4 address too.
  if (g0 === 0x64 && g1 === 0xff9b && g2 === 0 && g3 === 0 && g4 === 0 && g5 === 0) {
    const inner = blockedV4(v4FromGroups(g6, g7))
    return inner ? `NAT64 ${inner}` : null
  }
  // 6to4 (2002::/16) embeds the IPv4 address in groups 1 and 2.
  if (g0 === 0x2002) {
    const inner = blockedV4(v4FromGroups(g1, g2))
    return inner ? `6to4 ${inner}` : null
  }
  if ((g0 & 0xfe00) === 0xfc00) return "unique local (fc00::/7)"
  if ((g0 & 0xffc0) === 0xfe80) return "link-local (fe80::/10)"
  if ((g0 & 0xffc0) === 0xfec0) return "site-local (fec0::/10)"
  if ((g0 & 0xff00) === 0xff00) return "multicast"
  if (g0 === 0x2001 && g1 === 0x0db8) return "documentation"
  // Teredo (2001::/32) tunnels to an IPv4 address hidden in the suffix.
  if (g0 === 0x2001 && g1 === 0) return "Teredo (2001::/32)"
  if (g0 === 0x0100 && g1 === 0 && g2 === 0 && g3 === 0) return "discard-only"
  return null
}

// Why an address is off limits, or null when it's a public unicast address.
export function blockedAddressReason(ip: string): string | null {
  const bare = ip.replace(/^\[|\]$/g, "")
  const family = isIP(bare.split("%")[0] ?? bare)
  if (family === 4) return blockedV4(bare)
  if (family === 6) return blockedV6(bare)
  return "not an IP address"
}

export function isBlockedAddress(ip: string): boolean {
  return blockedAddressReason(ip) !== null
}

// --- URLs -----------------------------------------------------------------------------

const BLOCKED_SUFFIXES = [".localhost", ".internal", ".local", ".localdomain", ".home.arpa", ".lan"]

// Static checks that need no DNS: scheme, credentials, port, obviously local names
// and literal IPs. Returns the parsed URL.
export function assertPublicUrl(raw: string | URL): URL {
  let url: URL
  try {
    url = raw instanceof URL ? new URL(raw.href) : new URL(raw)
  } catch {
    throw new SsrfError("invalid_url", "Not a valid URL")
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") throw new SsrfError("blocked_protocol", `Only http and https are allowed, not ${url.protocol}`)
  if (url.username || url.password) throw new SsrfError("blocked_credentials", "URLs with credentials are not allowed")
  const port = url.port ? Number(url.port) : url.protocol === "https:" ? 443 : 80
  if (BLOCKED_PORTS.has(port)) throw new SsrfError("blocked_port", `Port ${port} is not allowed`)
  const host = hostOf(url)
  if (!host) throw new SsrfError("invalid_url", "URL has no host")
  if (host === "localhost" || BLOCKED_SUFFIXES.some((s) => host.endsWith(s)) || host === "metadata.google.internal") {
    throw new SsrfError("blocked_host", `Host ${host} is internal`)
  }
  if (isIP(host)) {
    const why = blockedAddressReason(host)
    if (why) throw new SsrfError("blocked_address", `Address ${host} is ${why}`)
  } else if (!host.includes(".")) {
    // Single-label names (http://router/) resolve through the local search domain.
    throw new SsrfError("blocked_host", `Host ${host} is not a public name`)
  }
  return url
}

// Hostname without IPv6 brackets or a trailing dot, lowercased.
export function hostOf(url: URL): string {
  return url.hostname.toLowerCase().replace(/^\[|\]$/g, "").replace(/\.$/, "")
}

export type ResolvedAddress = { address: string; family: 4 | 6 }

// dns.lookup(host, { all: true }) shaped resolver, injectable for tests.
export type Resolver = (hostname: string) => Promise<ResolvedAddress[]>

export const systemResolver: Resolver = async (hostname) => {
  const { lookup } = await import("node:dns/promises")
  const rows = await lookup(hostname, { all: true, verbatim: true })
  return rows.map((r) => ({ address: r.address, family: r.family === 6 ? 6 : 4 }))
}

// Resolve a host and refuse it when ANY of its addresses is internal: a name that
// answers with both a public and a private address is a rebinding setup.
export async function resolvePublic(hostname: string, resolver: Resolver = systemResolver): Promise<ResolvedAddress> {
  const literal = isIP(hostname)
  if (literal) {
    const why = blockedAddressReason(hostname)
    if (why) throw new SsrfError("blocked_address", `Address ${hostname} is ${why}`)
    return { address: hostname, family: literal === 6 ? 6 : 4 }
  }
  let rows: ResolvedAddress[]
  try {
    rows = await resolver(hostname)
  } catch (err) {
    throw new SsrfError("dns", `Could not resolve ${hostname}: ${(err as Error).message ?? String(err)}`)
  }
  if (!rows.length) throw new SsrfError("dns", `${hostname} has no addresses`)
  for (const r of rows) {
    const why = blockedAddressReason(r.address)
    if (why) throw new SsrfError("blocked_address", `${hostname} resolves to ${r.address} (${why})`)
  }
  return rows[0] as ResolvedAddress
}

// A fetch that refuses internal targets on every request, for long-lived clients
// (an MCP connection) whose URL came from chat. The host is re-resolved each call,
// so a name that later starts pointing inside is refused too. Redirects are not
// followed: a public server could otherwise bounce the request inward.
export function publicOnlyFetch(base: typeof fetch = fetch, resolver: Resolver = systemResolver): typeof fetch {
  return async (input, init) => {
    const raw = input instanceof Request ? input.url : String(input)
    const url = assertPublicUrl(raw)
    await resolvePublic(hostOf(url), resolver)
    return base(input, { ...init, redirect: "error" })
  }
}

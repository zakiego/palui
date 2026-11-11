// Flow templates (RFC flows 4.2): "{{ path }}" or "{{ path | filter | filter(arg) }}".
// A value that is exactly one template keeps its type (an array stays an array);
// a template inside a longer string is stringified. Filters come from a fixed
// list. Paths use the expression grammar, so a template can only read values,
// never run anything.
//
// Message mode (message and check-in text) sanitizes every interpolated value
// before any filter runs: control characters go, and the outbox row is sent as
// plain text, which keeps markup characters inert. A tainted value (outside
// data) also loses its line breaks and is capped at 500 characters, so it can't
// fake line structure; a clean one (the flow's own recap, say) keeps its lines.
import { formatClock, formatDate } from "../reminders/time"
import { ExprError, evaluate, parsePath, rootKey, type EvalEnv, type PathNode } from "./expr"
import { compactJson, isEmpty, numeric, toMs } from "./values"

export const FILTERS: Record<string, [min: number, max: number]> = {
  time: [0, 0],
  date: [0, 0],
  json: [0, 0],
  default: [1, 1],
  join: [0, 1],
  lines: [0, 1],
  upper: [0, 0],
  lower: [0, 0],
  round: [0, 1],
  truncate: [1, 1],
}

export const MAX_VALUE_CHARS = 500
export const MAX_LINES = 20
export const MAX_RENDERED_CHARS = 4000

export class TemplateError extends Error {}

export type Filter = { name: string; args: (string | number)[] }
export type TemplatePart = { t: "text"; v: string } | { t: "tpl"; path: PathNode; filters: Filter[]; src: string }

export type RenderMode = "data" | "message"

export function hasTemplate(s: unknown): s is string {
  return typeof s === "string" && s.includes("{{")
}

// Splits on "|" outside quotes and brackets.
function splitPipes(s: string): string[] {
  const out: string[] = []
  let cur = ""
  let quote: string | null = null
  let depth = 0
  for (const ch of s) {
    if (quote) {
      cur += ch
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'") quote = ch
    else if (ch === "[" || ch === "(") depth++
    else if (ch === "]" || ch === ")") depth--
    else if (ch === "|" && depth === 0) {
      out.push(cur)
      cur = ""
      continue
    }
    cur += ch
  }
  out.push(cur)
  return out.map((x) => x.trim())
}

function parseFilter(src: string): Filter {
  const m = /^([A-Za-z_][A-Za-z0-9_]*)\s*(?:\((.*)\))?$/s.exec(src)
  if (!m) throw new TemplateError(`"${src}" isn't a filter. Filters: ${Object.keys(FILTERS).join(", ")}.`)
  const name = m[1]!
  const arity = FILTERS[name]
  if (!arity) throw new TemplateError(`Unknown filter "${name}". Filters: ${Object.keys(FILTERS).join(", ")}.`)
  const args: (string | number)[] = []
  const raw = (m[2] ?? "").trim()
  if (raw) {
    const re = /\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|(-?\d+(?:\.\d+)?))\s*(,|$)/gy
    let at = 0
    while (at < raw.length) {
      re.lastIndex = at
      const a = re.exec(raw)
      if (!a) throw new TemplateError(`Filter ${name}: arguments must be quoted strings or numbers.`)
      if (a[3] !== undefined) args.push(Number(a[3]))
      else args.push((a[1] ?? a[2] ?? "").replace(/\\(.)/g, (_x, c: string) => (c === "n" ? "\n" : c)))
      at = re.lastIndex
      if (!a[4]) break
    }
  }
  if (args.length < arity[0] || args.length > arity[1]) throw new TemplateError(`Filter ${name} takes ${arity[0] === arity[1] ? arity[0] : `${arity[0]} to ${arity[1]}`} argument${arity[1] === 1 ? "" : "s"}.`)
  return { name, args }
}

export function parseTemplate(s: string): TemplatePart[] {
  const parts: TemplatePart[] = []
  let i = 0
  while (i < s.length) {
    const open = s.indexOf("{{", i)
    if (open < 0) {
      parts.push({ t: "text", v: s.slice(i) })
      break
    }
    if (open > i) parts.push({ t: "text", v: s.slice(i, open) })
    const close = s.indexOf("}}", open + 2)
    if (close < 0) throw new TemplateError(`A "{{" at character ${open + 1} has no closing "}}".`)
    const inner = s.slice(open + 2, close).trim()
    if (!inner) throw new TemplateError(`Empty template at character ${open + 1}.`)
    const [pathSrc, ...filterSrcs] = splitPipes(inner)
    let path: PathNode
    try {
      path = parsePath(pathSrc!)
    } catch (e) {
      throw new TemplateError(`{{${inner}}}: ${(e as ExprError).message}`)
    }
    parts.push({ t: "tpl", path, filters: filterSrcs.map(parseFilter), src: inner })
    i = close + 2
  }
  return parts
}

// Throws TemplateError, or returns the paths the template reads.
export function templatePaths(s: string): PathNode[] {
  return parseTemplate(s).flatMap((p) => (p.t === "tpl" ? [p.path] : []))
}

// --- Rendering ----------------------------------------------------------------------------

export function sanitizeText(s: string, strict = true): string {
  if (strict) {
    const clean = s.replace(/[\u0000-\u001f\u007f\u2028\u2029]/g, " ")
    return clean.length > MAX_VALUE_CHARS ? `${clean.slice(0, MAX_VALUE_CHARS - 1)}…` : clean
  }
  const clean = s
    .replace(/\r\n?/g, "\n")
    .replace(/[\u0000-\u0009\u000b-\u001f\u007f\u2028\u2029]/g, " ")
    .replace(/\n{3,}/g, "\n\n")
  return clean.length > MAX_RENDERED_CHARS ? `${clean.slice(0, MAX_RENDERED_CHARS - 1)}…` : clean
}

// Every string leaf sanitized (message mode), bounded in size.
function sanitizeDeep(v: unknown, strict: boolean, depth = 0): unknown {
  if (typeof v === "string") return sanitizeText(v, strict)
  if (depth > 6 || v === null || typeof v !== "object") return v
  if (Array.isArray(v)) return v.slice(0, 100).map((x) => sanitizeDeep(x, strict, depth + 1))
  return Object.fromEntries(
    Object.entries(v as Record<string, unknown>)
      .slice(0, 100)
      .map(([k, x]) => [sanitizeText(k), sanitizeDeep(x, strict, depth + 1)]),
  )
}

export function stringify(v: unknown): string {
  if (v === null || v === undefined) return ""
  if (typeof v === "string") return v
  if (typeof v === "number" || typeof v === "boolean") return String(v)
  return compactJson(v)
}

function applyFilter(f: Filter, v: unknown, env: EvalEnv): unknown {
  switch (f.name) {
    case "time": {
      const ms = toMs(v, env.tz, env.now)
      return ms === null ? v : formatClock(ms, env.tz)
    }
    case "date": {
      const ms = toMs(v, env.tz, env.now)
      return ms === null ? v : formatDate(ms, env.tz, env.now)
    }
    case "json":
      return compactJson(v)
    case "default":
      return isEmpty(v) ? f.args[0] : v
    case "join":
      return Array.isArray(v) ? v.slice(0, 100).map(stringify).join(String(f.args[0] ?? ", ")) : v
    case "lines": {
      if (!Array.isArray(v)) return v
      const field = typeof f.args[0] === "string" ? f.args[0].split(".").filter(Boolean) : []
      const pick = (el: unknown) => {
        let cur = el
        for (const k of field) cur = cur && typeof cur === "object" ? (cur as Record<string, unknown>)[k] : null
        return cur
      }
      const shown = v.slice(0, MAX_LINES).map((el) => `- ${stringify(pick(el))}`)
      if (v.length > MAX_LINES) shown.push(`- … (+${v.length - MAX_LINES} lagi)`)
      return shown.join("\n")
    }
    case "upper":
      return typeof v === "string" ? v.toUpperCase() : v
    case "lower":
      return typeof v === "string" ? v.toLowerCase() : v
    case "round": {
      const x = numeric(v)
      if (x === null) return v
      const f10 = 10 ** Math.max(0, Math.min(6, Math.trunc(Number(f.args[0] ?? 0))))
      return Math.round(x * f10) / f10
    }
    case "truncate": {
      const s = stringify(v)
      const n = Math.max(1, Math.trunc(Number(f.args[0])))
      return s.length > n ? `${s.slice(0, Math.max(0, n - 1))}…` : s
    }
  }
  return v
}

// Root keys whose values came from outside data (see taint.ts).
export type TaintCheck = (key: string) => boolean

function evalPart(p: Extract<TemplatePart, { t: "tpl" }>, env: EvalEnv, mode: RenderMode, tainted?: TaintCheck): unknown {
  let v = evaluate(p.path, env).value
  if (mode === "message") v = sanitizeDeep(v, tainted ? tainted(rootKey(p.path)) : true)
  for (const f of p.filters) v = applyFilter(f, v, env)
  return v
}

// One string: a lone template keeps its type, anything else becomes a string.
export function renderString(s: string, env: EvalEnv, mode: RenderMode = "data", tainted?: TaintCheck): unknown {
  if (!hasTemplate(s)) return s
  const parts = parseTemplate(s)
  if (parts.length === 1 && parts[0]!.t === "tpl" && mode === "data") return evalPart(parts[0]!, env, mode)
  let out = ""
  for (const p of parts) {
    out += p.t === "text" ? p.v : stringify(evalPart(p, env, mode, tainted))
    if (out.length > MAX_RENDERED_CHARS * 4) break
  }
  return out
}

// Text for a message or check-in: always a string, capped. Without a taint
// check every value is treated as tainted (the strictest rendering).
export function renderText(s: string, env: EvalEnv, tainted?: TaintCheck): string {
  const out = String(renderString(s, env, "message", tainted))
  return out.length > MAX_RENDERED_CHARS ? `${out.slice(0, MAX_RENDERED_CHARS - 1)}…` : out
}

// Tool args, prompt inputs: every string inside is rendered, keys stay literal.
export function renderDeep(v: unknown, env: EvalEnv, depth = 0): unknown {
  if (typeof v === "string") return renderString(v, env, "data")
  if (depth > 10 || v === null || typeof v !== "object") return v
  if (Array.isArray(v)) return v.map((x) => renderDeep(x, env, depth + 1))
  return Object.fromEntries(Object.entries(v as Record<string, unknown>).map(([k, x]) => [k, renderDeep(x, env, depth + 1)]))
}

// Every template string inside a value (args), for validation and taint.
export function templatesIn(v: unknown, depth = 0): string[] {
  if (typeof v === "string") return hasTemplate(v) ? [v] : []
  if (depth > 10 || v === null || typeof v !== "object") return []
  return (Array.isArray(v) ? v : Object.values(v as object)).flatMap((x) => templatesIn(x, depth + 1))
}

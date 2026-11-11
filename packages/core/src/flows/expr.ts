// The flow expression language (RFC flows section 6). A small hand-written
// parser turns a string into an AST at save time; the AST is what runs. There is
// no eval, no Function and no regex from the flow, and evaluation is bounded.
//
//   expr  := or
//   or    := and ("or" and)*
//   and   := not ("and" not)*
//   not   := "not" not | cmp
//   cmp   := sum (("==" | "!=" | "<" | "<=" | ">" | ">=") sum)?
//   sum   := term (("+" | "-") term)*
//   term  := unary (("*" | "/") unary)*
//   unary := "-" unary | atom
//   atom  := number | string | true | false | null | path | call | "(" expr ")"
//   path  := root ("." segment | "[" (path | string | number) "]")*
//   call  := name "(" args? ")"
//
// Semantics: == is strict except numbers against numeric strings; a comparison
// with null other than == / != is false; and/or short-circuit; division by zero
// is null; in + and - a null counts as 0 so counters can start from nothing.
import { calendarDaysBetween, getIn, isEmpty, localDateOf, numeric, toMs, truthy, weekdayOfMs, type Scope, type Segment } from "./values"

export const ROOTS = ["trigger", "steps", "vars", "flow", "item", "index", "now", "run"] as const
export type Root = (typeof ROOTS)[number]

export const FUNCTIONS: Record<string, [min: number, max: number]> = {
  exists: [1, 1],
  empty: [1, 1],
  contains: [2, 2],
  startsWith: [2, 2],
  len: [1, 1],
  lower: [1, 1],
  any: [3, 3],
  count: [3, 3],
  number: [1, 1],
  round: [1, 2],
  min: [2, 2],
  max: [2, 2],
  // choose(cond, a, b): a when cond is truthy, else b.
  choose: [3, 3],
  date: [1, 1],
  daysUntil: [1, 1],
  hoursUntil: [1, 1],
  minutesSince: [1, 1],
  weekday: [0, 1],
  today: [0, 0],
}

export const MAX_NODES = 200
export const MAX_EVAL_STEPS = 1000
export const MAX_STRING = 10_000
export const MAX_SOURCE = 2000

export type CmpOp = "==" | "!=" | "<" | "<=" | ">" | ">="

export type PathNode = { t: "path"; root: Root; segs: (Segment | Node)[]; src: string }
export type Node =
  | { t: "lit"; v: string | number | boolean | null; src: string }
  | PathNode
  | { t: "call"; name: string; args: Node[]; src: string }
  | { t: "not"; e: Node; src: string }
  | { t: "neg"; e: Node; src: string }
  | { t: "and" | "or"; l: Node; r: Node; src: string }
  | { t: "cmp"; op: CmpOp; l: Node; r: Node; src: string }
  | { t: "arith"; op: "+" | "-" | "*" | "/"; l: Node; r: Node; src: string }

export class ExprError extends Error {
  constructor(
    message: string,
    readonly at: number,
  ) {
    super(message)
  }
}

// --- Tokens -------------------------------------------------------------------------------

type Tok = { k: "num" | "str" | "id" | "op" | "end"; v: string; n?: number; s: number; e: number }

const OPS = ["==", "!=", "<=", ">=", "<", ">", "+", "-", "*", "/", "(", ")", "[", "]", ".", ","]

function tokenize(src: string): Tok[] {
  const out: Tok[] = []
  let i = 0
  while (i < src.length) {
    const c = src[i]!
    if (/\s/.test(c)) {
      i++
      continue
    }
    const s = i
    if (/[0-9]/.test(c)) {
      while (i < src.length && /[0-9]/.test(src[i]!)) i++
      // A dot followed by a digit is a decimal point, unless we're in a path (handled by the parser: paths use id tokens).
      if (src[i] === "." && /[0-9]/.test(src[i + 1] ?? "") && !(out.at(-1)?.k === "op" && out.at(-1)?.v === ".")) {
        i++
        while (i < src.length && /[0-9]/.test(src[i]!)) i++
      }
      out.push({ k: "num", v: src.slice(s, i), n: Number(src.slice(s, i)), s, e: i })
      continue
    }
    if (c === '"' || c === "'") {
      i++
      let v = ""
      for (;;) {
        if (i >= src.length) throw new ExprError("A string is missing its closing quote.", s)
        const ch = src[i]!
        if (ch === c) {
          i++
          break
        }
        if (ch === "\\") {
          const nx = src[i + 1]
          v += nx === "n" ? "\n" : nx === "t" ? "\t" : (nx ?? "")
          i += 2
          continue
        }
        v += ch
        i++
      }
      out.push({ k: "str", v, s, e: i })
      continue
    }
    if (/[A-Za-z_]/.test(c)) {
      while (i < src.length && /[A-Za-z0-9_]/.test(src[i]!)) i++
      out.push({ k: "id", v: src.slice(s, i), s, e: i })
      continue
    }
    const op = OPS.find((o) => src.startsWith(o, i))
    if (!op) {
      if (c === "=") throw new ExprError('Use "==" to compare, "=" alone is not an operator.', i)
      if (c === "&" || c === "|") throw new ExprError('Use "and" / "or" instead of && and ||.', i)
      if (c === "!") throw new ExprError('Use "not" instead of "!".', i)
      throw new ExprError(`Unexpected character "${c}".`, i)
    }
    out.push({ k: "op", v: op, s, e: i + op.length })
    i += op.length
  }
  out.push({ k: "end", v: "", s: src.length, e: src.length })
  return out
}

// --- Parser -------------------------------------------------------------------------------

const KEYWORDS = new Set(["and", "or", "not", "true", "false", "null"])

export function parseExpr(src: string): Node {
  if (typeof src !== "string" || !src.trim()) throw new ExprError("The expression is empty.", 0)
  if (src.length > MAX_SOURCE) throw new ExprError(`The expression is longer than ${MAX_SOURCE} characters.`, 0)
  const toks = tokenize(src)
  let p = 0
  let nodes = 0
  const peek = () => toks[p]!
  const next = () => toks[p++]!
  const isOp = (v: string) => peek().k === "op" && peek().v === v
  const isKw = (v: string) => peek().k === "id" && peek().v === v
  const node = <T extends Node>(n: T): T => {
    if (++nodes > MAX_NODES) throw new ExprError(`The expression has more than ${MAX_NODES} parts.`, 0)
    return n
  }
  const text = (s: number, e: number) => src.slice(s, e).trim()

  function expect(v: string) {
    if (!isOp(v)) throw new ExprError(`Expected "${v}"${peek().k === "end" ? " before the end" : ` near "${peek().v}"`}.`, peek().s)
    next()
  }

  function or(): Node {
    const s = peek().s
    let l = and()
    while (isKw("or")) {
      next()
      const r = and()
      l = node({ t: "or", l, r, src: text(s, toks[p - 1]!.e) })
    }
    return l
  }
  function and(): Node {
    const s = peek().s
    let l = not()
    while (isKw("and")) {
      next()
      const r = not()
      l = node({ t: "and", l, r, src: text(s, toks[p - 1]!.e) })
    }
    return l
  }
  function not(): Node {
    const s = peek().s
    if (isKw("not")) {
      next()
      const e = not()
      return node({ t: "not", e, src: text(s, toks[p - 1]!.e) })
    }
    return cmp()
  }
  function cmp(): Node {
    const s = peek().s
    const l = sum()
    const t = peek()
    if (t.k === "op" && ["==", "!=", "<", "<=", ">", ">="].includes(t.v)) {
      next()
      const r = sum()
      const n = node({ t: "cmp", op: t.v as CmpOp, l, r, src: text(s, toks[p - 1]!.e) })
      const after = peek()
      if (after.k === "op" && ["==", "!=", "<", "<=", ">", ">="].includes(after.v)) throw new ExprError('Comparisons don\'t chain: write "a < b and b < c".', after.s)
      return n
    }
    return l
  }
  function sum(): Node {
    const s = peek().s
    let l = term()
    while (isOp("+") || isOp("-")) {
      const op = next().v as "+" | "-"
      const r = term()
      l = node({ t: "arith", op, l, r, src: text(s, toks[p - 1]!.e) })
    }
    return l
  }
  function term(): Node {
    const s = peek().s
    let l = unary()
    while (isOp("*") || isOp("/")) {
      const op = next().v as "*" | "/"
      const r = unary()
      l = node({ t: "arith", op, l, r, src: text(s, toks[p - 1]!.e) })
    }
    return l
  }
  function unary(): Node {
    const s = peek().s
    if (isOp("-")) {
      next()
      const e = unary()
      return node({ t: "neg", e, src: text(s, toks[p - 1]!.e) })
    }
    return atom()
  }
  function atom(): Node {
    const t = peek()
    if (t.k === "num") {
      next()
      return node({ t: "lit", v: t.n!, src: t.v })
    }
    if (t.k === "str") {
      next()
      if (t.v.length > MAX_STRING) throw new ExprError("A string literal is too long.", t.s)
      return node({ t: "lit", v: t.v, src: text(t.s, t.e) })
    }
    if (isOp("(")) {
      next()
      const e = or()
      expect(")")
      return e
    }
    if (t.k === "id") {
      if (t.v === "true" || t.v === "false") {
        next()
        return node({ t: "lit", v: t.v === "true", src: t.v })
      }
      if (t.v === "null") {
        next()
        return node({ t: "lit", v: null, src: t.v })
      }
      if (KEYWORDS.has(t.v)) throw new ExprError(`"${t.v}" can't start a value here.`, t.s)
      if (toks[p + 1]?.k === "op" && toks[p + 1]!.v === "(") return call()
      return path()
    }
    if (t.k === "end") throw new ExprError("The expression ends too early.", t.s)
    throw new ExprError(`Unexpected "${t.v}".`, t.s)
  }
  function call(): Node {
    const t = next()
    const arity = FUNCTIONS[t.v]
    if (!arity) throw new ExprError(`Unknown function "${t.v}". Functions: ${Object.keys(FUNCTIONS).join(", ")}.`, t.s)
    expect("(")
    const args: Node[] = []
    if (!isOp(")")) {
      args.push(or())
      while (isOp(",")) {
        next()
        args.push(or())
      }
    }
    expect(")")
    if (args.length < arity[0] || args.length > arity[1]) {
      throw new ExprError(`${t.v}() takes ${arity[0] === arity[1] ? arity[0] : `${arity[0]} to ${arity[1]}`} argument${arity[1] === 1 ? "" : "s"}, got ${args.length}.`, t.s)
    }
    return node({ t: "call", name: t.v, args, src: text(t.s, toks[p - 1]!.e) })
  }
  function path(): PathNode {
    const t = next()
    if (!(ROOTS as readonly string[]).includes(t.v)) {
      // Routines had {{n}} for the re-ask number.
      throw new ExprError(`"${t.v}" isn't a known value. Paths start with ${ROOTS.join(", ")}.${t.v === "n" ? " For the re-ask number use run.followUps." : ""}`, t.s)
    }
    const segs: (Segment | Node)[] = []
    for (;;) {
      if (isOp(".")) {
        next()
        const seg = next()
        if (seg.k === "id") segs.push(seg.v)
        else if (seg.k === "num" && /^\d+$/.test(seg.v)) segs.push(Number(seg.v))
        else if (seg.k === "num") {
          // "items.0.1" tokenizes "0.1" as one number when it follows a dot in odd spots; split it.
          for (const part of seg.v.split(".")) segs.push(Number(part))
        } else throw new ExprError(`Expected a field name after "."`, seg.s)
        continue
      }
      if (isOp("[")) {
        next()
        const inner = peek()
        if (inner.k === "str") {
          next()
          segs.push(inner.v)
        } else if (inner.k === "num") {
          next()
          segs.push(inner.n!)
        } else if (inner.k === "id" && (ROOTS as readonly string[]).includes(inner.v)) segs.push(path())
        else throw new ExprError("Inside [ ] use a path, a string or a number.", inner.s)
        expect("]")
        continue
      }
      break
    }
    return node({ t: "path", root: t.v as Root, segs, src: text(t.s, toks[p - 1]!.e) })
  }

  const ast = or()
  if (peek().k !== "end") throw new ExprError(`Unexpected "${peek().v}" after the end of the expression.`, peek().s)
  return ast
}

// A path only ("steps.bills.data.items"): each.items and poll.select.
export function parsePath(src: string): PathNode {
  const ast = parseExpr(src)
  if (ast.t !== "path") throw new ExprError("Use a plain path here, like steps.x.data.items.", 0)
  return ast
}

// Throws ExprError with the problem as a sentence, or returns the AST.
export function checkExpr(src: string): Node {
  return parseExpr(src)
}

// --- Static reads -------------------------------------------------------------------------

// Every path the expression reads, including paths used inside [ ].
export function pathsOf(n: Node, out: PathNode[] = []): PathNode[] {
  switch (n.t) {
    case "lit":
      break
    case "path":
      out.push(n)
      for (const s of n.segs) if (typeof s === "object") pathsOf(s, out)
      break
    case "call":
      for (const a of n.args) pathsOf(a, out)
      break
    case "not":
    case "neg":
      pathsOf(n.e, out)
      break
    default:
      pathsOf(n.l, out)
      pathsOf(n.r, out)
  }
  return out
}

// The taint key a path belongs to: "trigger", "steps.<id>", "vars.<name>",
// "flow.<name>", "item", "index", "now", "run".
export function rootKey(p: { root: Root; segs: (Segment | Node)[] }): string {
  if (p.root === "steps" || p.root === "vars" || p.root === "flow") {
    const first = p.segs[0]
    return typeof first === "string" || typeof first === "number" ? `${p.root}.${first}` : p.root
  }
  return p.root
}

// --- Evaluation ---------------------------------------------------------------------------

export type EvalEnv = { scope: Scope; tz: string; now: number }

export type TraceEntry = { expr: string; value: unknown }

export type EvalResult = { value: unknown; trace: TraceEntry[]; warnings: string[] }

class Budget extends Error {}

export function evaluate(ast: Node, env: EvalEnv): EvalResult {
  let steps = 0
  const trace: TraceEntry[] = []
  const warnings: string[] = []
  const seen = new Set<string>()
  const record = (src: string, value: unknown) => {
    if (seen.has(src) || trace.length >= 24) return
    seen.add(src)
    trace.push({ expr: src, value: clip(value) })
  }
  const warn = (w: string) => {
    if (warnings.length < 8 && !warnings.includes(w)) warnings.push(w)
  }

  const resolvePath = (n: PathNode): unknown => {
    const segs: Segment[] = []
    for (const s of n.segs) {
      if (typeof s === "object") {
        const k = ev(s)
        if (typeof k === "string" || (typeof k === "number" && Number.isFinite(k))) segs.push(k)
        else return null
      } else segs.push(s)
    }
    return getIn(env.scope[n.root] ?? null, segs)
  }

  const ev = (n: Node): unknown => {
    if (++steps > MAX_EVAL_STEPS) throw new Budget()
    switch (n.t) {
      case "lit":
        return n.v
      case "path": {
        const v = resolvePath(n)
        record(n.src, v)
        return v
      }
      case "call": {
        const v = callFn(n.name, n.args.map(ev), env, warn)
        record(n.src, v)
        return v
      }
      case "not":
        return !truthy(ev(n.e))
      case "neg": {
        const v = numeric(ev(n.e))
        if (v === null) {
          warn(`${n.src}: not a number`)
          return null
        }
        return -v
      }
      case "and":
        return truthy(ev(n.l)) ? truthy(ev(n.r)) : false
      case "or":
        return truthy(ev(n.l)) ? true : truthy(ev(n.r))
      case "cmp":
        return compare(n.op, ev(n.l), ev(n.r), n.src, warn)
      case "arith":
        return arith(n.op, ev(n.l), ev(n.r), n.src, warn)
    }
  }

  try {
    const value = ev(ast)
    if (typeof value === "string" && value.length > MAX_STRING) {
      warn(`the result is longer than ${MAX_STRING} characters`)
      return { value: null, trace, warnings }
    }
    return { value, trace, warnings }
  } catch (e) {
    if (e instanceof Budget) {
      warn(`evaluation took more than ${MAX_EVAL_STEPS} steps`)
      return { value: false, trace, warnings }
    }
    throw e
  }
}

// Evaluates a condition to a boolean (an `if` or an `each.where`).
export function evalCondition(ast: Node, env: EvalEnv): EvalResult & { result: boolean } {
  const r = evaluate(ast, env)
  return { ...r, result: truthy(r.value) }
}

function clip(v: unknown): unknown {
  if (typeof v === "string" && v.length > 200) return `${v.slice(0, 199)}…`
  if (v && typeof v === "object") {
    try {
      const s = JSON.stringify(v)
      if (s.length > 300) return `${s.slice(0, 299)}…`
    } catch {
      return "[object]"
    }
  }
  return v
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a && b && typeof a === "object" && typeof b === "object") {
    try {
      return JSON.stringify(a) === JSON.stringify(b)
    } catch {
      return false
    }
  }
  return false
}

export function looseEqual(a: unknown, b: unknown): boolean {
  if (a === undefined) a = null
  if (b === undefined) b = null
  if (typeof a === "number" || typeof b === "number") {
    const x = numeric(a)
    const y = numeric(b)
    if (x !== null && y !== null && (typeof a === "number" || typeof b === "number")) return x === y
  }
  return deepEqual(a, b)
}

function compare(op: CmpOp, a: unknown, b: unknown, src: string, warn: (w: string) => void): boolean {
  if (op === "==") return looseEqual(a, b)
  if (op === "!=") return !looseEqual(a, b)
  if (a === null || a === undefined || b === null || b === undefined) return false
  let x: number | string
  let y: number | string
  const na = numeric(a)
  const nb = numeric(b)
  if (na !== null && nb !== null) {
    x = na
    y = nb
  } else if (typeof a === "string" && typeof b === "string") {
    // ISO dates and clock times compare correctly as text.
    x = a
    y = b
  } else {
    warn(`${src}: can't compare ${typeOf(a)} with ${typeOf(b)}`)
    return false
  }
  switch (op) {
    case "<":
      return x < y
    case "<=":
      return x <= y
    case ">":
      return x > y
    case ">=":
      return x >= y
  }
}

function typeOf(v: unknown): string {
  if (v === null || v === undefined) return "null"
  if (Array.isArray(v)) return "a list"
  return typeof v === "object" ? "an object" : `a ${typeof v}`
}

function arith(op: "+" | "-" | "*" | "/", a: unknown, b: unknown, src: string, warn: (w: string) => void): unknown {
  if (op === "+" && (typeof a === "string" || typeof b === "string") && numeric(a) === null && numeric(b) === null) {
    const s = `${a ?? ""}${b ?? ""}`
    return s.length > MAX_STRING ? s.slice(0, MAX_STRING) : s
  }
  if (op === "+" || op === "-") {
    const x = a === null || a === undefined ? 0 : numeric(a)
    const y = b === null || b === undefined ? 0 : numeric(b)
    if (x === null || y === null) {
      warn(`${src}: not a number`)
      return null
    }
    return op === "+" ? x + y : x - y
  }
  const x = numeric(a)
  const y = numeric(b)
  if (x === null || y === null) {
    if (a !== null && b !== null) warn(`${src}: not a number`)
    return null
  }
  if (op === "*") return x * y
  return y === 0 ? null : x / y
}

function callFn(name: string, args: unknown[], env: EvalEnv, warn: (w: string) => void): unknown {
  const [a, b, c] = args
  switch (name) {
    case "exists":
      return a !== null && a !== undefined
    case "empty":
      return isEmpty(a)
    case "contains":
      if (typeof a === "string" && (typeof b === "string" || typeof b === "number")) return a.includes(String(b))
      if (Array.isArray(a)) return a.some((x) => looseEqual(x, b))
      return false
    case "startsWith":
      return typeof a === "string" && (typeof b === "string" || typeof b === "number") ? a.startsWith(String(b)) : false
    case "len":
      if (typeof a === "string" || Array.isArray(a)) return a.length
      if (a && typeof a === "object") return Object.keys(a).length
      return 0
    case "lower":
      return typeof a === "string" ? a.toLowerCase() : a
    case "any":
    case "count": {
      if (!Array.isArray(a)) return name === "any" ? false : 0
      const field = typeof b === "string" ? b.split(".").filter(Boolean) : []
      const n = a.slice(0, 1000).filter((el) => looseEqual(getIn(el, field), c)).length
      return name === "any" ? n > 0 : n
    }
    case "number":
      return numeric(a)
    case "round": {
      const x = numeric(a)
      const d = b === undefined ? 0 : numeric(b)
      if (x === null || d === null) return null
      const f = 10 ** Math.max(0, Math.min(6, Math.trunc(d)))
      return Math.round(x * f) / f
    }
    case "min":
    case "max": {
      const x = numeric(a)
      const y = numeric(b)
      if (x === null || y === null) return x ?? y
      return name === "min" ? Math.min(x, y) : Math.max(x, y)
    }
    case "choose":
      return truthy(a) ? b : c
    case "date":
      return toMs(a, env.tz, env.now)
    case "daysUntil": {
      const ms = toMs(a, env.tz, env.now)
      if (ms === null) {
        if (a !== null) warn(`daysUntil: ${JSON.stringify(clip(a))} isn't a date`)
        return null
      }
      return calendarDaysBetween(env.now, ms, env.tz)
    }
    case "hoursUntil": {
      const ms = toMs(a, env.tz, env.now)
      return ms === null ? null : Math.round(((ms - env.now) / 3600_000) * 100) / 100
    }
    case "minutesSince": {
      const ms = toMs(a, env.tz, env.now)
      return ms === null ? null : Math.floor((env.now - ms) / 60_000)
    }
    case "weekday": {
      const ms = args.length ? toMs(a, env.tz, env.now) : env.now
      return ms === null ? null : weekdayOfMs(ms, env.tz)
    }
    case "today":
      return localDateOf(env.now, env.tz)
  }
  return null
}

// "daysUntil(item.due) = 2" lines for logs and explain_flow.
export function explainTrace(trace: TraceEntry[]): string {
  return trace.map((t) => `${t.expr} = ${JSON.stringify(t.value) ?? "null"}`).join(", ")
}

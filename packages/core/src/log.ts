// JSON-lines logger with secret redaction (PLAN 4.5, 17). Every line can carry
// run_id, chat_id and connector_id through child loggers.

const PATTERNS: RegExp[] = [
  /\brcl_[A-Za-z0-9_-]{6,}/g,
  /\bzkg_[A-Za-z0-9_-]{6,}/g,
  /\bsk-[A-Za-z0-9_-]{8,}/g,
  // Telegram bot token, also inside URLs like /bot123456:AA... where \b doesn't fire.
  /(?<!\d)\d{6,12}:[A-Za-z0-9_-]{30,}/g,
  /\bBearer\s+[A-Za-z0-9._~+/=-]{12,}/gi,
]

export function redact(s: string): string {
  let out = s
  for (const p of PATTERNS) out = out.replace(p, (m) => `${m.slice(0, 4)}••••`)
  return out
}

// redact() applied to every string inside a JSON-like value (event data).
export function redactDeep<T>(value: T): T {
  if (typeof value === "string") return redact(value) as T
  if (Array.isArray(value)) return value.map((v) => redactDeep(v)) as T
  if (value && typeof value === "object" && Object.getPrototypeOf(value) === Object.prototype) {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value)) out[k] = redactDeep(v)
    return out as T
  }
  return value
}

export type LogLevel = "debug" | "info" | "warn" | "error"
export type LogFields = Record<string, unknown>

export type Logger = {
  debug(msg: string, fields?: LogFields): void
  info(msg: string, fields?: LogFields): void
  warn(msg: string, fields?: LogFields): void
  error(msg: string, fields?: LogFields): void
  child(fields: LogFields): Logger
}

const ORDER: Record<LogLevel, number> = { debug: 10, info: 20, warn: 30, error: 40 }

export type LogSink = (line: string) => void

export function createLogger(base: LogFields = {}, sink: LogSink = (l) => process.stdout.write(`${l}\n`), min: LogLevel = (process.env.LOG_LEVEL as LogLevel) || "info"): Logger {
  const write = (level: LogLevel, msg: string, fields?: LogFields) => {
    if (ORDER[level] < ORDER[min]) return
    const rec = { t: new Date().toISOString(), level, msg, ...base, ...fields }
    let line: string
    try {
      line = JSON.stringify(rec, (_k, v) => (v instanceof Error ? { name: v.name, message: v.message, stack: v.stack } : v))
    } catch {
      line = JSON.stringify({ t: rec.t, level, msg, note: "unserializable fields" })
    }
    sink(redact(line))
  }
  return {
    debug: (m, f) => write("debug", m, f),
    info: (m, f) => write("info", m, f),
    warn: (m, f) => write("warn", m, f),
    error: (m, f) => write("error", m, f),
    child: (fields) => createLogger({ ...base, ...fields }, sink, min),
  }
}

export const silentLogger: Logger = {
  debug() {},
  info() {},
  warn() {},
  error() {},
  child: () => silentLogger,
}

// The bridge's config file: the token and the allowlist of commands. Nothing
// runs that isn't listed here, commands run without a shell, and the model can
// only fill in declared params, each checked against an enum or a pattern.
import { readFileSync } from "node:fs"
import { homedir } from "node:os"
import { isAbsolute, join } from "node:path"
import { z } from "zod"

export const DEFAULT_PORT = 4893

export function defaultConfigPath(): string {
  return process.env.PALUI_HOST_BRIDGE_CONFIG ?? join(homedir(), ".config", "palui", "host-bridge.json")
}

export function expandHome(path: string): string {
  return path === "~" || path.startsWith("~/") ? join(homedir(), path.slice(1)) : path
}

const NAME = /^[a-z][a-z0-9_]{0,47}$/
// `{name}` inside an arg, or an arg that is exactly `{...name}` (a list param).
const PLACEHOLDER = /\{([a-z][a-z0-9_]*)\}/g
const SPREAD = /^\{\.\.\.([a-z][a-z0-9_]*)\}$/

// Strict objects: a typo like "timeout" or "allowdash" is an error naming the
// key, not a setting that silently does nothing. `note` is free text, since JSON
// has no comments.
const paramSchema = z
  .strictObject({
    description: z.string().optional(),
    note: z.string().optional(),
    // Exactly one of enum or pattern. A pattern is anchored: it must match the whole value.
    enum: z.array(z.string().min(1)).min(1).optional(),
    pattern: z.string().min(1).optional(),
    optional: z.boolean().default(false),
    // A list of values, spread into separate args by `{...name}`.
    list: z.boolean().default(false),
    // Values starting with "-" are refused unless this is on, so a value can't
    // turn into a flag (`--exec=...`) the owner never meant to allow.
    allowDash: z.boolean().default(false),
  })
  .refine((p) => (p.enum ? 1 : 0) + (p.pattern ? 1 : 0) === 1, "a param needs exactly one of enum or pattern")
  .refine((p) => {
    if (!p.pattern) return true
    try {
      new RegExp(p.pattern)
      return true
    } catch {
      return false
    }
  }, "pattern isn't a valid regular expression")

export type ParamSpec = z.infer<typeof paramSchema>

const commandSchema = z
  .strictObject({
    name: z.string().regex(NAME, "lowercase letters, digits and _, starting with a letter"),
    // Shown in Palui's tool table, chat transcript and approval prompts. Say what
    // it does and where, e.g. "Restart nginx on the Mac mini". Default: "Host: <name>".
    title: z.string().min(1).max(80).optional(),
    description: z.string().min(1),
    note: z.string().optional(),
    // read: no side effects. write: changes something, undoable. destructive: anything else.
    // Palui prefills the tool class from this; the dashboard still has the last word.
    class: z.enum(["read", "write", "destructive"]),
    // Absolute path (or ~/...). launchd's PATH is minimal, and a bare name could resolve anywhere.
    command: z.string().min(1).transform(expandHome).refine(isAbsolute, "command must be an absolute path"),
    args: z.array(z.string()).default([]),
    params: z.record(z.string().regex(NAME), paramSchema).default({}),
    cwd: z.string().transform(expandHome).optional(),
    env: z.record(z.string(), z.string()).default({}),
    timeoutMs: z.number().int().min(100).max(600_000).default(30_000),
    // Per stream. Longer output keeps its start and its end (where errors usually are).
    maxOutputBytes: z.number().int().min(1024).max(1_048_576).default(16_384),
    // Exit codes that count as success. grep exits 1 on "no match", for example.
    okExitCodes: z.array(z.number().int().min(0).max(255)).min(1).default([0]),
  })
  .superRefine((c, ctx) => {
    const used = new Set<string>()
    for (const arg of c.args) {
      const spread = SPREAD.exec(arg)
      if (spread) {
        const name = spread[1]!
        used.add(name)
        if (!c.params[name]) ctx.addIssue({ code: "custom", message: `arg "${arg}" uses an undeclared param` })
        else if (!c.params[name].list) ctx.addIssue({ code: "custom", message: `"{...${name}}" needs a list param` })
        continue
      }
      for (const m of arg.matchAll(PLACEHOLDER)) {
        const name = m[1]!
        used.add(name)
        if (!c.params[name]) ctx.addIssue({ code: "custom", message: `arg "${arg}" uses an undeclared param` })
        else if (c.params[name].list) ctx.addIssue({ code: "custom", message: `list param "${name}" must be a whole arg: "{...${name}}"` })
      }
    }
    for (const name of Object.keys(c.params)) {
      if (!used.has(name)) ctx.addIssue({ code: "custom", message: `param "${name}" isn't used in args` })
    }
  })

export type CommandSpec = z.infer<typeof commandSchema>

export const configSchema = z
  .strictObject({
    note: z.string().optional(),
    // 127.0.0.1 is enough: Docker Desktop and OrbStack route host.docker.internal
    // to the Mac's loopback. 0.0.0.0 would expose the bridge to the LAN.
    host: z.string().default("127.0.0.1"),
    port: z.number().int().min(1).max(65_535).default(DEFAULT_PORT),
    token: z.string().min(32, "use a token of at least 32 characters"),
    commands: z.array(commandSchema).default([]),
  })
  .superRefine((c, ctx) => {
    const seen = new Set<string>()
    for (const cmd of c.commands) {
      if (seen.has(cmd.name)) ctx.addIssue({ code: "custom", message: `duplicate command name "${cmd.name}"` })
      seen.add(cmd.name)
    }
  })

export type BridgeConfig = z.infer<typeof configSchema>

export function parseConfig(raw: unknown): BridgeConfig {
  const r = configSchema.safeParse(raw)
  if (!r.success) {
    const lines = r.error.issues.map((i) => `  ${issuePath(raw, i.path)}: ${i.message}`)
    throw new Error(`invalid host bridge config:\n${lines.join("\n")}`)
  }
  return r.data
}

// "commands.3.params.repo" becomes "git_log.params.repo", so nobody counts entries by hand.
function issuePath(raw: unknown, path: PropertyKey[]): string {
  if (path[0] === "commands" && typeof path[1] === "number") {
    const entry = (raw as { commands?: unknown[] } | null)?.commands?.[path[1]]
    const name = entry && typeof entry === "object" && "name" in entry && typeof entry.name === "string" ? entry.name : null
    const head = name ? `command "${name}"` : `commands[${path[1]}]`
    return [head, ...path.slice(2).map(String)].join(".")
  }
  return path.map(String).join(".") || "(root)"
}

export class MissingConfigError extends Error {}

export function loadConfig(path: string): BridgeConfig {
  let text: string
  try {
    text = readFileSync(path, "utf8")
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") throw new MissingConfigError(`no config at ${path}`)
    throw new Error(`can't read ${path}: ${(err as Error).message}`)
  }
  let json: unknown
  try {
    json = JSON.parse(text)
  } catch (err) {
    throw new Error(`${path} isn't valid JSON: ${(err as Error).message}`)
  }
  return parseConfig(json)
}

export class ArgError extends Error {}

// Turns the model's input into argv. Throws ArgError on anything that isn't
// declared, doesn't match, or looks like a flag without allowDash.
export function buildArgv(spec: CommandSpec, input: Record<string, unknown>): string[] {
  for (const key of Object.keys(input)) {
    if (!spec.params[key]) throw new ArgError(`unknown param "${key}"`)
  }
  const values = new Map<string, string[]>()
  for (const [name, p] of Object.entries(spec.params)) {
    const v = input[name]
    if (v === undefined || v === null) {
      if (!p.optional) throw new ArgError(`missing param "${name}"`)
      continue
    }
    const list = p.list ? v : [v]
    if (!Array.isArray(list)) throw new ArgError(`param "${name}" must be a list of strings`)
    for (const item of list) checkValue(name, p, item)
    values.set(name, list as string[])
  }

  const argv: string[] = []
  for (const arg of spec.args) {
    const spread = SPREAD.exec(arg)
    if (spread) {
      argv.push(...(values.get(spread[1]!) ?? []))
      continue
    }
    let missing = false
    const out = arg.replace(PLACEHOLDER, (_, name: string) => {
      const v = values.get(name)
      if (!v) missing = true
      return v?.[0] ?? ""
    })
    // An arg that mentions an absent optional param is dropped whole.
    if (!missing) argv.push(out)
  }
  return argv
}

function checkValue(name: string, p: ParamSpec, v: unknown): asserts v is string {
  if (typeof v !== "string") throw new ArgError(`param "${name}" must be a string`)
  // Control characters (NUL, newlines) are never fine in an arg.
  if (/[\u0000-\u001f\u007f]/.test(v)) throw new ArgError(`param "${name}" has control characters`)
  if (!p.allowDash && v.startsWith("-")) throw new ArgError(`param "${name}" can't start with "-"`)
  if (p.enum && !p.enum.includes(v)) throw new ArgError(`param "${name}" must be one of: ${p.enum.join(", ")}`)
  if (p.pattern && !new RegExp(`^(?:${p.pattern})$`).test(v)) throw new ArgError(`param "${name}" doesn't match ${p.pattern}`)
}

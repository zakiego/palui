// Save-time checks for a flow definition (RFC flows 4, 6, 7.2): the zod shape,
// then the graph (unique ids, known targets, no cycles, nothing unreachable),
// every expression and template parsed, templates only where they're allowed,
// `item` only inside an each body, and every tool resolved against the running
// connectors with its class. Problems come back as sentences with a JSON path,
// for check_flow and every write. The derived `writeCalls` list is what the owner
// approves for a version.
import { BUILTIN_COMMANDS } from "../chat/command-names"
import { isValidZone } from "../reminders/time"
import { ExprError, parseExpr, parsePath, pathsOf, type PathNode } from "./expr"
import { edgesOf, findCycle, reachable } from "./graph"
import {
  canonicalJson,
  definitionSchema,
  formatPath,
  MAX_DEFINITION_BYTES,
  RESERVED_IDS,
  upgradeDefinition,
  zodProblems,
  type FlowDefinition,
  type FlowStep,
} from "./schema"
import { hasTemplate, TemplateError, templatePaths, templatesIn } from "./template"
import type { ToolClass } from "@palui/db"

export type Problem = { path: string; message: string }

export type FlowApprovalMode = "version" | "every_run"

// A tool as the running connectors describe it.
export type ToolInfo = {
  // Namespaced, as search_tools shows it.
  name: string
  connectorId: string
  tool: string
  class: ToolClass
  untrusted: boolean
  connectorType?: string
  flowApproval?: FlowApprovalMode
}

// One side effect a version can cause, as the owner approves it.
export type WriteCall =
  | { stepId: string; kind: "tool"; connectorId: string; connectorType?: string; tool: string; class: ToolClass; args: Record<string, unknown> }
  | { stepId: string; kind: "message"; to: string }

export type ValidateContext = {
  // Resolves a tool step's tool. Omitted: tools aren't checked (offline drafts).
  resolveTool?: (ref: { tool: string; connectorId?: string }) => ToolInfo | null
  hookExists?: (hookId: string) => boolean
  // The flow's own chat, and whether another chat may receive messages.
  chatId?: string
  chatAllowed?: (chatId: string) => "own" | "allowed" | false
  // Other flows in the chat, for event triggers that name one.
  flowNames?: string[]
}

export type Validated = {
  ok: boolean
  problems: Problem[]
  definition: FlowDefinition | null
  tools: Record<string, ToolInfo>
  writeCalls: WriteCall[]
}

export function parseDefinition(raw: unknown): { definition: FlowDefinition | null; problems: Problem[] } {
  const up = upgradeDefinition(raw)
  let size = 0
  try {
    size = Buffer.byteLength(canonicalJson(up), "utf8")
  } catch {
    return { definition: null, problems: [{ path: "(root)", message: "The definition isn't JSON." }] }
  }
  if (size > MAX_DEFINITION_BYTES) return { definition: null, problems: [{ path: "(root)", message: `The definition is ${Math.round(size / 1024)} KB; the limit is 32 KB.` }] }
  const r = definitionSchema.safeParse(up)
  if (!r.success) return { definition: null, problems: zodProblems(r.error, up) }
  return { definition: r.data, problems: [] }
}

export function validateDefinition(raw: unknown, ctx: ValidateContext = {}): Validated {
  const parsed = parseDefinition(raw)
  const out: Validated = { ok: false, problems: parsed.problems, definition: parsed.definition, tools: {}, writeCalls: [] }
  const def = parsed.definition
  if (!def) return out
  const problems = out.problems
  const p = (path: (string | number)[], message: string) => problems.push({ path: formatPath(path), message })

  if (def.tz && !isValidZone(def.tz)) p(["tz"], `Unknown time zone "${def.tz}".`)

  // --- ids and targets ---------------------------------------------------------------
  const stepIds = new Set<string>()
  def.steps.forEach((s, i) => {
    if (stepIds.has(s.id)) p(["steps", i, "id"], `Step id "${s.id}" is used twice.`)
    if (RESERVED_IDS.includes(s.id)) p(["steps", i, "id"], `"${s.id}" is reserved for onError; pick another step id.`)
    stepIds.add(s.id)
  })
  const triggerIds = new Set<string>()
  def.triggers.forEach((t, i) => {
    if (triggerIds.has(t.id)) p(["triggers", i, "id"], `Trigger id "${t.id}" is used twice.`)
    triggerIds.add(t.id)
    if (!stepIds.has(t.start)) p(["triggers", i, "start"], `Trigger "${t.id}" starts at "${t.start}", which isn't a step.`)
  })
  def.steps.forEach((s, i) => {
    if (s.kind === "if" && s.next !== undefined) p(["steps", i, "next"], `Step "${s.id}" is an if: use then/else, not next.`)
    for (const e of edgesOf(def, s)) if (!stepIds.has(e.to)) p(["steps", i, e.label], `Step "${s.id}" points ${e.label} to "${e.to}", which isn't a step.`)
    if (s.kind === "checkin") {
      const answers = new Set(s.answers.map((a) => a.id))
      for (const a of Object.keys(s.on ?? {})) {
        if (!answers.has(a)) p(["steps", i, "on", a], `Step "${s.id}": "${a}" isn't one of its answers.`)
        else if (!s.answers.find((x) => x.id === a)!.closes) p(["steps", i, "on", a], `Step "${s.id}": "${a}" doesn't close the check-in, so it can't branch.`)
      }
    }
  })
  if (problems.length) return out

  // --- graph -------------------------------------------------------------------------
  const cycle = findCycle(def)
  if (cycle) p(["steps"], `The steps loop: ${cycle.join(" -> ")}. Flows can't repeat steps; use a checkin's re-asks or an each instead.`)
  const starts = def.triggers.length ? def.triggers.map((t) => t.start) : [def.steps[0]!.id]
  const reach = reachable(def, starts)
  def.steps.forEach((s, i) => {
    if (!reach.has(s.id)) p(["steps", i], `Step "${s.id}" can't be reached from any trigger. Point a next, branch or trigger start at it, or remove it.`)
  })
  // Steps inside an each body, and steps on a main path.
  const bodyStarts = def.steps.flatMap((s) => (s.kind === "each" ? [s.body] : []))
  const inBody = reachable(def, bodyStarts)
  const onMain = reachable(def, starts, false)
  def.steps.forEach((s, i) => {
    if (s.kind === "each" && inBody.has(s.id)) p(["steps", i], `Step "${s.id}": an each can't run inside another each's body. Use a second flow with an event trigger instead.`)
  })

  // --- expressions and templates -----------------------------------------------------
  const usesItem = (paths: PathNode[]) => paths.some((x) => x.root === "item" || x.root === "index")
  def.steps.forEach((s, i) => {
    const paths: PathNode[] = []
    const expr = (field: (string | number)[], src: string, pathOnly = false) => {
      try {
        const ast = pathOnly ? parsePath(src) : parseExpr(src)
        paths.push(...pathsOf(ast))
      } catch (e) {
        if (e instanceof ExprError) p(["steps", i, ...field], `${e.message} (in ${JSON.stringify(src.slice(0, 80))})`)
        else throw e
      }
    }
    const tpl = (field: (string | number)[], src: unknown) => {
      for (const t of templatesIn(src)) {
        try {
          paths.push(...templatePaths(t))
        } catch (e) {
          if (e instanceof TemplateError) p(["steps", i, ...field], e.message)
          else throw e
        }
      }
    }
    const noTemplate = (field: (string | number)[], src: string | undefined, what: string) => {
      if (src && hasTemplate(src)) p(["steps", i, ...field], `${what} is a literal; templates aren't allowed there.`)
    }
    switch (s.kind) {
      case "tool":
        tpl(["args"], s.args)
        for (const k of Object.keys(s.args)) if (hasTemplate(k)) p(["steps", i, "args", k], "Arg names are literals; templates aren't allowed there.")
        break
      case "if":
        expr(["when"], s.when)
        break
      case "message":
        tpl(["text"], s.text)
        noTemplate(["to"], s.to, "message.to")
        break
      case "checkin":
        tpl(["text"], s.text)
        tpl(["followUp", "text"], s.followUp.text)
        tpl(["missedText"], s.missedText)
        s.answers.forEach((a, j) => tpl(["answers", j, "reply"], a.reply))
        if (s.deadline.kind === "after" && typeof s.deadline.minutes === "string") {
          if (!hasTemplate(s.deadline.minutes)) p(["steps", i, "deadline", "minutes"], 'deadline.minutes is a number or a template like "{{vars.left}}".')
          tpl(["deadline", "minutes"], s.deadline.minutes)
        }
        if (s.deadline.kind === "at") {
          if (!hasTemplate(s.deadline.at) && !/^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}.*)?$/.test(s.deadline.at)) p(["steps", i, "deadline", "at"], "deadline.at is a template or an ISO date/datetime.")
          tpl(["deadline", "at"], s.deadline.at)
        }
        break
      case "wait":
        if ((s.minutes === undefined) === (s.until === undefined)) p(["steps", i], `Wait "${s.id}" needs exactly one of minutes or until.`)
        if (s.until !== undefined) {
          if (!hasTemplate(s.until) && !/^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}.*)?$/.test(s.until)) p(["steps", i, "until"], "until is a template or an ISO date/datetime.")
          tpl(["until"], s.until)
        }
        if (s.plusMin !== undefined && s.until === undefined) p(["steps", i, "plusMin"], "plusMin only goes with until.")
        break
      case "prompt":
        noTemplate(["prompt"], s.prompt, "The prompt instruction")
        tpl(["input"], s.input)
        break
      case "set":
        for (const [k, v] of Object.entries(s.values)) {
          if (hasTemplate(v)) tpl(["values", k], v)
          else expr(["values", k], v)
        }
        break
      case "each":
        expr(["items"], s.items, true)
        if (s.where) expr(["where"], s.where)
        if (s.key) {
          if (!hasTemplate(s.key)) p(["steps", i, "key"], 'key is a template, like "{{item.id}}".')
          tpl(["key"], s.key)
        }
        break
    }
    // An each's own where and key read the item they filter; any other step reads
    // item only when it runs inside a body (and never on a main path).
    if (s.kind !== "each" && usesItem(paths) && (!inBody.has(s.id) || onMain.has(s.id))) {
      p(["steps", i], `Step "${s.id}" reads item/index but isn't only inside an each body.`)
    }
    if (s.kind === "each" && pathsOfEachItems(s).some((x) => x.root === "item")) p(["steps", i, "items"], "items can't read item; it's the list the each walks.")
  })

  // --- triggers ----------------------------------------------------------------------
  def.triggers.forEach((t, i) => {
    switch (t.kind) {
      case "chat":
        if (!t.command === !t.phrases) p(["triggers", i], `Chat trigger "${t.id}" needs exactly one of command or phrases.`)
        if (t.command && BUILTIN_COMMANDS.includes(t.command)) p(["triggers", i, "command"], `${t.command} is a built-in command; pick another.`)
        break
      case "webhook":
        if (ctx.hookExists && !ctx.hookExists(t.hookId)) p(["triggers", i, "hookId"], `No hook ${t.hookId}. Create one on the Hooks page first.`)
        break
      case "event":
        if (t.flow === "self" && t.event === "run.failed") p(["triggers", i], "A flow can't subscribe to its own run.failed.")
        if (t.flow !== "self" && ctx.flowNames && !ctx.flowNames.includes(t.flow)) p(["triggers", i, "flow"], `No flow named "${t.flow}" in this chat.`)
        break
      case "poll":
        if (ctx.resolveTool) {
          const info = ctx.resolveTool({ tool: t.tool, connectorId: t.connectorId })
          if (!info) p(["triggers", i, "tool"], `No enabled tool ${t.connectorId ? `${t.connectorId}.` : ""}${t.tool}. Find it with palui__search_tools.`)
          else if (info.class !== "read") p(["triggers", i, "tool"], `${info.name} is ${info.class}; a poll only calls read tools.`)
        }
        for (const x of templatesIn(t.args)) if (x) p(["triggers", i, "args"], "Poll args are literals; templates aren't allowed there.")
        break
      case "schedule":
      case "manual":
        break
    }
  })

  // `deadline: next` needs the next occurrence of a schedule trigger.
  def.steps.forEach((s, i) => {
    if (s.kind !== "checkin" || s.deadline.kind !== "next") return
    const from = def.triggers.filter((t) => reachable(def, [t.start]).has(s.id))
    if (!from.length || from.some((t) => t.kind !== "schedule")) {
      p(["steps", i, "deadline"], `Check-in "${s.id}" uses deadline "next", which only works when every trigger reaching it is a schedule. Use {kind:"after", minutes:N} or {kind:"at", at:"<template>", plusMin?}.`)
    }
  })

  // --- tools and side effects -----------------------------------------------------------
  const ownChat = ctx.chatId
  def.steps.forEach((s, i) => {
    if (s.kind === "tool" && ctx.resolveTool) {
      const info = ctx.resolveTool({ tool: s.tool, connectorId: s.connectorId })
      if (!info) {
        p(["steps", i, "tool"], `No enabled tool ${s.connectorId ? `${s.connectorId}.` : ""}${s.tool}. Find it with palui__search_tools.`)
        return
      }
      out.tools[s.id] = info
      if (info.class === "destructive") {
        p(["steps", i, "tool"], `${info.name} is destructive. Flows never run destructive tools; ask with a checkin and do it from chat instead.`)
        return
      }
      if (info.class === "write") out.writeCalls.push({ stepId: s.id, kind: "tool", connectorId: info.connectorId, connectorType: info.connectorType, tool: info.tool, class: info.class, args: s.args })
    }
    if (s.kind === "prompt" && ctx.resolveTool) {
      s.tools.forEach((name, j) => {
        // "*": every enabled tool under the unattended rules (migrated routine prompts).
        if (name === "*") return
        const info = ctx.resolveTool!({ tool: name })
        if (!info) p(["steps", i, "tools", j], `No enabled tool ${name}.`)
        else if (info.class === "destructive") p(["steps", i, "tools", j], `${info.name} is destructive; prompt steps never get destructive tools.`)
      })
    }
    if (s.kind === "message" && s.to && s.to !== ownChat) {
      const ok = ctx.chatAllowed ? ctx.chatAllowed(s.to) : "allowed"
      if (!ok) p(["steps", i, "to"], `Chat ${s.to} isn't one of the owner's chats or an allowed chat.`)
      else if (ok === "allowed") out.writeCalls.push({ stepId: s.id, kind: "message", to: s.to })
    }
  })

  out.problems = problems.filter((x) => x.message)
  out.ok = out.problems.length === 0
  return out
}

function pathsOfEachItems(s: Extract<FlowStep, { kind: "each" }>): PathNode[] {
  try {
    return pathsOf(parsePath(s.items))
  } catch {
    return []
  }
}

// The write calls as the owner sees them: "keuangan.mark_paid(id: {{item.id}})".
export function describeWriteCall(w: WriteCall): string {
  if (w.kind === "message") return `kirim pesan ke chat ${w.to}`
  const args = Object.entries(w.args)
    .map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(", ")
  return `${w.connectorId}.${w.tool}(${args.length > 200 ? `${args.slice(0, 199)}…` : args})`
}

// Same calls, same args templates, same classes: no new approval needed.
export function sameWriteCalls(a: WriteCall[], b: WriteCall[]): boolean {
  const key = (w: WriteCall) => (w.kind === "message" ? `m:${w.to}` : `t:${w.connectorId}:${w.tool}:${w.class}:${canonicalJson(w.args)}`)
  const ka = a.map(key).sort()
  const kb = b.map(key).sort()
  return ka.length === kb.length && ka.every((x, i) => x === kb[i])
}

// A resolver over a list of known tools: by namespaced name, or by connector id
// plus the bare tool name.
export function resolverFrom(tools: ToolInfo[]): (ref: { tool: string; connectorId?: string }) => ToolInfo | null {
  return (ref) => {
    if (ref.connectorId) return tools.find((t) => t.connectorId === ref.connectorId && t.tool === ref.tool) ?? tools.find((t) => t.name === ref.tool && t.connectorId === ref.connectorId) ?? null
    return tools.find((t) => t.name === ref.tool) ?? null
  }
}

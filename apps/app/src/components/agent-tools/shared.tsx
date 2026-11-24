// Pieces every Tools tab layout shares: the grouping, one tool row, the
// global-or-own select, and the open state that follows a search.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { ArrowUpRightIcon, BotIcon, CircleOffIcon, ContactIcon, GlobeIcon, LockIcon, LockOpenIcon, Share2Icon, UserIcon, UsersIcon } from "lucide-react"

import { connectorIcon } from "@/components/connector-icon"
import { IconTile } from "@/components/icon-tile"
import { SimpleSelect } from "@/components/simple-select"
import { CLASS_TONE } from "@/components/tool-trace"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldDescription, FieldLabel } from "@/components/ui/field"
import { StatusText } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import type { DataScope, getAgentToolsFn } from "@/lib/api/agents"
import type { ToolPermissions } from "@/lib/api/types"
import { useCount, useLabel, useT } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { cn } from "@/lib/utils"

export type ToolsData = Awaited<ReturnType<typeof getAgentToolsFn>>
export type ToolItem = NonNullable<ToolsData["tools"]>[number]

export type ToolsViewProps = {
  agent: { id: string; isDefault: boolean; guestTools: string }
  data: ToolsData
  // Every tool, and the ones the search kept, one list per connector.
  tools: ToolItem[]
  groups: ToolItem[][]
  query: string
  // The agents a tool connector serves; empty means every agent.
  agentsOf: (connectorId: string) => string[]
  setScope: (connectorId: string, scope: string) => void
  // Whether the owner marked an MCP server as holding no private data.
  noPrivate: (connectorId: string) => boolean
  setNoPrivate: (connectorId: string, on: boolean) => void
  // The agent's tool permissions, as changed on this tab.
  permissions: ToolPermissions
  // One entry at one level: a tool name, a connector id, or a classKey().
  // null puts it back to the default.
  setPermission: (level: PermissionLevel, key: string, p: Permission | null) => void
}

export type Permission = "allow" | "ask" | "deny"
export type PermissionLevel = "tools" | "classes" | "connectors"
export type ToolClass = "read" | "write" | "destructive"

// The order a connector's tools are grouped in.
export const CLASSES: ToolClass[] = ["read", "write", "destructive"]

// Anything unclassified is destructive, like the server.
export function classOf(x: ToolItem): ToolClass {
  return x.class === "read" || x.class === "write" ? x.class : "destructive"
}

export const classKey = (connectorId: string, cls: ToolClass) => `${connectorId}:${cls}`

// The entry above a tool that locks it: its connector's, then its class's.
// A higher level locks the ones under it, like the server.
export function lockFor(x: ToolItem, p: ToolPermissions): Permission | null {
  return p.connectors?.[x.connectorId] ?? p.classes?.[classKey(x.connectorId, classOf(x))] ?? null
}

// What the owner set for a tool at any level, allow-all aside.
export function setFor(x: ToolItem, p: ToolPermissions): Permission | null {
  return lockFor(x, p) ?? p.tools[x.name] ?? null
}

// What applies to a tool. Allow-all is only the fallback. null: the class rule.
export function permissionFor(x: ToolItem, p: ToolPermissions): Permission | null {
  return setFor(x, p) ?? (p.allowAll ? "allow" : null)
}

// The tools that match a search, one list per connector, in catalog order.
export function groupTools(tools: ToolItem[], query: string) {
  const q = query.trim().toLowerCase()
  const hits = tools.filter((x) => !q || x.tool.toLowerCase().includes(q) || (x.title ?? "").toLowerCase().includes(q) || x.connectorLabel.toLowerCase().includes(q))
  const by = new Map<string, ToolItem[]>()
  for (const x of hits) (by.get(x.connectorId) ?? by.set(x.connectorId, []).get(x.connectorId)!).push(x)
  return [...by.values()]
}

// Whose data a tool touches. A tool that doesn't say follows its server: none
// when the owner marked it as holding no private data, else the agent's when
// this agent owns the server, every agent's when it's global.
export function dataOf(x: ToolItem, owned: boolean, noPrivate = false): DataScope {
  return x.data ?? (noPrivate ? "none" : owned ? "agent" : "shared")
}

// The widest scope in a server: one shared tool makes the whole server shared.
const DATA_RANK: Record<DataScope, number> = { none: 0, chat: 1, agent: 2, shared: 3 }
export function groupData(list: ToolItem[], owned: boolean, noPrivate = false): DataScope {
  return list.map((x) => dataOf(x, owned, noPrivate)).reduce((a, b) => (DATA_RANK[b] > DATA_RANK[a] ? b : a), "none")
}

// Whose data, in the words of the two cards above.
export function DataScopeText({ scope }: { scope: DataScope }) {
  const t = useT()
  const icon = scope === "shared" ? <Share2Icon aria-hidden="true" /> : scope === "none" ? <LockOpenIcon aria-hidden="true" /> : <LockIcon aria-hidden="true" />
  return (
    <StatusText tone="secondary" icon={icon}>
      {t(`agents.tools.data.${scope}`)}
    </StatusText>
  )
}

// Whether a shared chat (a group, or someone else's DM) gets this tool: the
// server says it may, the agent offers tools there, and the owner didn't set
// it to ask first or off (shared chats never ask, so those stay out).
export function inSharedChats(x: ToolItem, guestTools: string, permissions: ToolsViewProps["permissions"]) {
  const p = permissionFor(x, permissions)
  return x.guestSafe && guestTools === "safe" && p !== "ask" && p !== "deny"
}

// Whether a members chat (PLAN 19.4) gets this tool: the server marks it
// member-safe and the owner didn't set it to ask first or off.
export function inMemberChats(x: ToolItem, permissions: ToolsViewProps["permissions"]) {
  const p = permissionFor(x, permissions)
  return x.memberSafe === true && p !== "ask" && p !== "deny"
}

// Who gets a tool, widest first: everyone in a shared chat, the owner and
// members, or only the owner. Ask first and destructive tools land on the owner.
export type Audience = "everyone" | "members" | "owner"

export function audienceOf(x: ToolItem, agent: ToolsViewProps["agent"], permissions: ToolsViewProps["permissions"]): Audience | "off" {
  if (permissionFor(x, permissions) === "deny") return "off"
  if (inSharedChats(x, agent.guestTools, permissions)) return "everyone"
  if (!agent.isDefault && inMemberChats(x, permissions)) return "members"
  return "owner"
}

const AUDIENCE_RANK: Record<Audience, number> = { owner: 0, members: 1, everyone: 2 }

// The widest audience among the tools that aren't off, how many tools have it,
// and whether every one of them does. null when every tool is off.
export function groupAudience(list: ToolItem[], agent: ToolsViewProps["agent"], permissions: ToolsViewProps["permissions"]) {
  const on = list.map((x) => audienceOf(x, agent, permissions)).filter((a): a is Audience => a !== "off")
  if (on.length === 0) return null
  const audience = on.reduce((a, b) => (AUDIENCE_RANK[b] > AUDIENCE_RANK[a] ? b : a))
  const n = on.filter((a) => a === audience).length
  return { audience, n, all: n === on.length }
}

const AUDIENCE_ICON: Record<Audience, React.ReactNode> = {
  everyone: <UsersIcon aria-hidden="true" />,
  members: <ContactIcon aria-hidden="true" />,
  owner: <UserIcon aria-hidden="true" />,
}

// Who gets it, with the icons of the rule rows. `n` switches to the count
// form, for a server where only some tools reach that far.
export function AudienceText({ audience, n }: { audience: Audience | "off"; n?: number }) {
  const t = useT()
  if (audience === "off") {
    return (
      <StatusText tone="secondary" icon={<CircleOffIcon aria-hidden="true" />}>
        {t("agents.perm.off")}
      </StatusText>
    )
  }
  const some = n !== undefined && audience !== "owner"
  return (
    <StatusText tone="secondary" icon={AUDIENCE_ICON[audience]} className={some ? "tabular-nums" : undefined}>
      {some ? t(`agents.tools.audienceSome.${audience}`, { n: n.toLocaleString() }) : t(`agents.tools.audience.${audience}`)}
    </StatusText>
  )
}

export function countClass(list: ToolItem[], cls: ToolClass) {
  return list.filter((x) => classOf(x) === cls).length
}

// Which panels are open. Starts at `initial`; a search opens every panel
// with a hit, and clearing it puts back what was open before.
export function useSearchOpen(query: string, hits: string[], initial: string[]) {
  const q = query.trim()
  const [open, setOpen] = React.useState<string[] | null>(null)
  const [saved, setSaved] = React.useState<string[] | null>(null)
  const [last, setLast] = React.useState(q)
  if (last !== q) {
    setLast(q)
    if (q) {
      if (!last) setSaved(open)
      setOpen(hits)
    } else {
      setOpen(saved)
    }
  }
  return [open ?? initial, setOpen] as const
}

export function ConnectorGlyph({ type, size = "md" }: { type: string; size?: "sm" | "md" }) {
  const Icon = connectorIcon(manifestFor(type)?.icon)
  return (
    <IconTile size={size}>
      <Icon />
    </IconTile>
  )
}

// Global (every agent) or only this agent. A server shared with other agents
// too says so (pick it on the connector's page); one this agent isn't on never
// shows here, since this agent doesn't get its tools.
export function ScopeSelect({ connectorLabel, agentId, agents, onChange }: { connectorLabel: string; agentId: string; agents: string[]; onChange: (v: string) => void }) {
  const t = useT()
  const value = agents.length === 0 ? "all" : agents.length === 1 && agents[0] === agentId ? "only" : "some"
  const options = [
    { value: "all", label: t("agents.tools.scopeAll"), icon: <GlobeIcon aria-hidden="true" /> },
    { value: "only", label: t("agents.tools.scopeHere"), icon: <BotIcon aria-hidden="true" /> },
    ...(value === "some" ? [{ value: "some", label: t("agents.tools.scopeShared", { n: agents.filter((a) => a !== agentId).length.toLocaleString() }), icon: <UsersIcon aria-hidden="true" /> }] : []),
  ]
  return <SimpleSelect size="sm" className="w-fit max-w-full min-w-0" aria-label={t("agents.tools.scopeFor", { name: connectorLabel })} value={value} options={options} onChange={onChange} />
}

// The global-or-own select (when the connector has one) and for an MCP
// server the owner's "holds no private data" switch. Nothing when neither.
export function GroupActions({ head, props }: { head: ToolItem; props: ToolsViewProps }) {
  const t = useT()
  const id = `no-private-${head.connectorId}`
  const scoped = head.connectorId in props.data.scopes
  if (!scoped && head.connectorType !== "mcp") return null
  return (
    <div className="flex flex-col gap-3">
      {scoped && (
        <ScopeSelect connectorLabel={head.connectorLabel} agentId={props.agent.id} agents={props.agentsOf(head.connectorId)} onChange={(v) => props.setScope(head.connectorId, v)} />
      )}
      {head.connectorType === "mcp" && (
        <Field orientation="horizontal" className="max-w-2xl">
          <FieldContent>
            <FieldLabel htmlFor={id}>{t("agents.tools.noPrivate")}</FieldLabel>
            <FieldDescription>
              {t("agents.tools.noPrivateHint")}
              {props.agentsOf(head.connectorId).length !== 1 && ` ${t("agents.tools.noPrivateGlobal")}`}
            </FieldDescription>
          </FieldContent>
          <Switch id={id} checked={props.noPrivate(head.connectorId)} onCheckedChange={(v) => props.setNoPrivate(head.connectorId, v)} />
        </Field>
      )}
    </div>
  )
}

// Every row with a select puts it in the same column, so the levels line up:
// the connector, each class, each tool. Under the text on a narrow screen.
const ROW = "grid grid-cols-1 items-center gap-x-4 gap-y-2 @md/main:grid-cols-[minmax(0,1fr)_10rem]"

// A connector's permissions as a tree: one select for every tool, one per
// class (read, write, destructive) and one per tool. A choice at a higher
// level locks the selects under it to that choice. `list` is the search
// hits; a class with none is left out.
export function PermissionTree({ head, list, props, scopeOf }: { head: ToolItem; list: ToolItem[]; props: ToolsViewProps; scopeOf?: (x: ToolItem) => DataScope }) {
  const t = useT()
  const label = useLabel()
  const p = props.permissions
  const connectorId = head.connectorId
  const connectorValue = p.connectors?.[connectorId] ?? null
  return (
    <div className="flex flex-col rounded-lg border">
      <div className={cn(ROW, "rounded-t-lg border-b bg-muted/40 px-3 py-2.5")}>
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="text-sm font-medium">{t("agents.perm.allTools")}</span>
            <span className="text-caption text-pretty text-muted-foreground">{t("agents.perm.allToolsHint")}</span>
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="shrink-0 text-muted-foreground"
            render={<Link to="/connectors/$id" params={{ id: connectorId }} />}
            aria-label={t("agents.tools.openFor", { name: head.connectorLabel })}
          >
            {t("common.open")}
            <ArrowUpRightIcon data-icon="inline-end" />
          </Button>
        </div>
        <PermissionSelect
          label={t("agents.perm.allToolsFor", { name: head.connectorLabel })}
          value={connectorValue}
          allowAll={p.allowAll}
          onChange={(v) => props.setPermission("connectors", connectorId, v)}
        />
      </div>
      {CLASSES.map((cls) => {
        const tools = list.filter((x) => classOf(x) === cls)
        if (tools.length === 0) return null
        const key = classKey(connectorId, cls)
        const className = label("tool.class", cls)
        return (
          <section key={cls} aria-label={className} className="border-b px-3 last:border-b-0">
            <div className={cn(ROW, "py-2.5")}>
              <span className="flex min-w-0 items-baseline gap-2">
                <StatusText tone={CLASS_TONE[cls] ?? "destructive"} icon={null} className="font-medium">
                  {className}
                </StatusText>
                <span className="text-caption text-muted-foreground tabular-nums">{tools.length.toLocaleString()}</span>
              </span>
              <PermissionSelect
                label={t("agents.perm.classFor", { class: className, name: head.connectorLabel })}
                value={p.classes?.[key] ?? null}
                locked={connectorValue}
                allowAll={p.allowAll}
                onChange={(v) => props.setPermission("classes", key, v)}
              />
            </div>
            <ul className="mb-2 ms-1 flex flex-col divide-y border-s ps-4">
              {tools.map((x) => (
                <ToolRow key={x.name} x={x} props={props} scope={scopeOf?.(x)} />
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

// One tool, with how often it ran and who gets it. Its class is the group
// it sits in. `scope` is set when the tool's server mixes scopes, so each
// tool says its own.
export function ToolRow({ x, props, scope }: { x: ToolItem; props: ToolsViewProps; scope?: DataScope }) {
  const t = useT()
  const used = props.data.used[x.name]
  const audience = audienceOf(x, props.agent, props.permissions)
  return (
    <li className={cn(ROW, "py-2.5")}>
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="font-mono text-sm [overflow-wrap:anywhere]">{x.tool}</span>
        {x.title && (
          <span dir="auto" className="text-caption text-pretty text-muted-foreground">
            {x.title}
          </span>
        )}
        <span className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1">
          {scope && <DataScopeText scope={scope} />}
          {x.untrusted && (
            <StatusText tone="secondary" icon={<GlobeIcon aria-hidden="true" />}>
              {t("agents.tools.outside")}
            </StatusText>
          )}
          <AudienceText audience={audience} />
          {used ? <span className="text-caption text-muted-foreground tabular-nums">{t("agents.tools.used", { n: used.toLocaleString() })}</span> : null}
        </span>
      </div>
      <PermissionSelect
        label={t("agents.perm.for", { name: x.tool })}
        value={props.permissions.tools[x.name] ?? null}
        locked={lockFor(x, props.permissions)}
        allowAll={props.permissions.allowAll}
        onChange={(p) => props.setPermission("tools", x.name, p)}
      />
    </li>
  )
}

// Default (allow with allow-all on, else the class rule), allow, ask or off.
// `locked` is a choice above this one: the select shows it and can't change.
export function PermissionSelect({
  label,
  value,
  locked = null,
  allowAll,
  onChange,
}: {
  label: string
  value: Permission | null
  locked?: Permission | null
  allowAll: boolean
  onChange: (p: Permission | null) => void
}) {
  const t = useT()
  const lock = (v: string) => (locked === v ? <LockIcon aria-hidden="true" /> : undefined)
  const options = [
    { value: "default", label: allowAll ? t("agents.perm.defaultAllow") : t("agents.perm.default") },
    { value: "allow", label: t("agents.perm.allow"), icon: lock("allow") },
    { value: "ask", label: t("agents.perm.ask"), icon: lock("ask") },
    { value: "deny", label: t("agents.perm.deny"), icon: lock("deny") },
  ]
  return (
    <SimpleSelect
      size="sm"
      className="w-full max-w-60 @md/main:max-w-none"
      aria-label={locked ? t("agents.perm.lockedFor", { name: label }) : label}
      disabled={locked !== null}
      value={locked ?? value ?? "default"}
      options={options}
      onChange={(v) => onChange(v === "allow" || v === "ask" || v === "deny" ? v : null)}
    />
  )
}

// How many tools a closed group holds, whose data they touch, who gets them,
// and how many are destructive.
export function GroupMeta({
  list,
  scope,
  agent,
  permissions,
  className,
}: {
  list: ToolItem[]
  scope: DataScope
  agent?: ToolsViewProps["agent"]
  permissions?: ToolsViewProps["permissions"]
  className?: string
}) {
  const t = useT()
  const count = useCount()
  const reach = agent && permissions ? groupAudience(list, agent, permissions) : null
  const destructive = countClass(list, "destructive")
  const allowed = permissions ? list.filter((x) => setFor(x, permissions) === "allow").length : 0
  const off = permissions ? list.filter((x) => setFor(x, permissions) === "deny").length : 0
  return (
    <span className={cn("flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-caption font-normal text-muted-foreground", className)}>
      <span className="tabular-nums">{count(list.length, "agents.tools.count.one", "agents.tools.count.many")}</span>
      <DataScopeText scope={scope} />
      {reach && <AudienceText audience={reach.audience} n={reach.all ? undefined : reach.n} />}
      {destructive > 0 && (
        <StatusText tone="destructive" icon={null} className="tabular-nums">
          {t("agents.tools.destructiveCount", { n: destructive.toLocaleString() })}
        </StatusText>
      )}
      {allowed > 0 && <span className="tabular-nums">{t("agents.perm.allowedCount", { n: allowed.toLocaleString() })}</span>}
      {off > 0 && <span className="tabular-nums">{t("agents.perm.offCount", { n: off.toLocaleString() })}</span>}
    </span>
  )
}

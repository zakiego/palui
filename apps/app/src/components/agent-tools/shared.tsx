// Pieces every Tools tab layout shares: the grouping, one tool row, the
// global-or-own select, and the open state that follows a search.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { ArrowUpRightIcon, BotIcon, CircleOffIcon, GlobeIcon, UsersIcon } from "lucide-react"

import { connectorIcon } from "@/components/connector-icon"
import { IconTile } from "@/components/icon-tile"
import { SimpleSelect } from "@/components/simple-select"
import { CLASS_TONE } from "@/components/tool-trace"
import { Button } from "@/components/ui/button"
import { StatusText } from "@/components/ui/status-text"
import type { getAgentToolsFn } from "@/lib/fns/agents"
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
  ownerOf: (connectorId: string) => string | null
  setScope: (connectorId: string, scope: string) => void
  // The agent's tool permissions, as changed on this tab. null: the default.
  permissions: { allowAll: boolean; tools: Record<string, Permission> }
  setPermission: (name: string, p: Permission | null) => void
}

export type Permission = "allow" | "ask" | "deny"

// The tools that match a search, one list per connector, in catalog order.
export function groupTools(tools: ToolItem[], query: string) {
  const q = query.trim().toLowerCase()
  const hits = tools.filter((x) => !q || x.tool.toLowerCase().includes(q) || (x.title ?? "").toLowerCase().includes(q) || x.connectorLabel.toLowerCase().includes(q))
  const by = new Map<string, ToolItem[]>()
  for (const x of hits) (by.get(x.connectorId) ?? by.set(x.connectorId, []).get(x.connectorId)!).push(x)
  return [...by.values()]
}

export function countClass(list: ToolItem[], cls: string) {
  return list.filter((x) => (CLASS_TONE[x.class] ? x.class : "destructive") === cls).length
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

// Global (every agent) or owned by this agent. A server another agent owns
// never shows here: this agent doesn't get its tools.
export function ScopeSelect({ connectorLabel, scope, onChange }: { connectorLabel: string; scope: string | null; onChange: (v: string) => void }) {
  const t = useT()
  const value = scope === null ? "all" : "only"
  const options = [
    { value: "all", label: t("agents.tools.scopeAll"), icon: <GlobeIcon aria-hidden="true" /> },
    { value: "only", label: t("agents.tools.scopeHere"), icon: <BotIcon aria-hidden="true" /> },
  ]
  return <SimpleSelect size="sm" className="w-fit max-w-full min-w-0" aria-label={t("agents.tools.scopeFor", { name: connectorLabel })} value={value} options={options} onChange={onChange} />
}

// The global-or-own select (when the connector has one) and a way to its page.
export function GroupActions({ head, props }: { head: ToolItem; props: ToolsViewProps }) {
  const t = useT()
  return (
    <div className="flex flex-wrap items-center gap-2">
      {head.connectorId in props.data.scopes && (
        <ScopeSelect connectorLabel={head.connectorLabel} scope={props.ownerOf(head.connectorId)} onChange={(v) => props.setScope(head.connectorId, v)} />
      )}
      <Button
        variant="ghost"
        size="sm"
        className="-me-2.5 ms-auto"
        render={<Link to="/connectors/$id" params={{ id: head.connectorId }} />}
        aria-label={t("agents.tools.openFor", { name: head.connectorLabel })}
      >
        {t("common.open")}
        <ArrowUpRightIcon data-icon="inline-end" />
      </Button>
    </div>
  )
}

// One tool, with how often it ran and what it may do.
export function ToolRow({ x, props }: { x: ToolItem; props: ToolsViewProps }) {
  const t = useT()
  const label = useLabel()
  const used = props.data.used[x.name]
  const { agent } = props
  const off = props.permissions.tools[x.name] === "deny"
  // Two fixed columns: the tool and its facts, then the permission select at
  // the same spot on every row (under the text on a narrow screen).
  return (
    <li className="grid grid-cols-1 items-center gap-x-4 gap-y-2 py-3 @md/main:grid-cols-[minmax(0,1fr)_10rem]">
      <div className="flex min-w-0 flex-col gap-1">
        <span className="font-mono text-sm [overflow-wrap:anywhere]">{x.tool}</span>
        {x.title && (
          <span dir="auto" className="text-caption text-pretty text-muted-foreground">
            {x.title}
          </span>
        )}
        <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <StatusText tone={CLASS_TONE[x.class] ?? "destructive"} icon={null}>
            {label("tool.class", x.class)}
          </StatusText>
          {x.untrusted && (
            <StatusText tone="secondary" icon={<GlobeIcon aria-hidden="true" />}>
              {t("agents.tools.outside")}
            </StatusText>
          )}
          {x.guestSafe && !agent.isDefault && agent.guestTools === "safe" && (
            <StatusText tone="secondary" icon={<UsersIcon aria-hidden="true" />}>
              {t("agents.tools.guests")}
            </StatusText>
          )}
          {off && (
            <StatusText tone="secondary" icon={<CircleOffIcon aria-hidden="true" />}>
              {t("agents.perm.off")}
            </StatusText>
          )}
          {used ? <span className="text-caption text-muted-foreground tabular-nums">{t("agents.tools.used", { n: used.toLocaleString() })}</span> : null}
        </span>
      </div>
      <PermissionSelect name={x.tool} value={props.permissions.tools[x.name] ?? null} allowAll={props.permissions.allowAll} onChange={(p) => props.setPermission(x.name, p)} />
    </li>
  )
}

// Default (the class rule, or allow when allow-all is on), allow, ask or off.
export function PermissionSelect({ name, value, allowAll, onChange }: { name: string; value: Permission | null; allowAll: boolean; onChange: (p: Permission | null) => void }) {
  const t = useT()
  const options = [
    { value: "default", label: allowAll ? t("agents.perm.defaultAllow") : t("agents.perm.default") },
    { value: "allow", label: t("agents.perm.allow") },
    { value: "ask", label: t("agents.perm.ask") },
    { value: "deny", label: t("agents.perm.deny") },
  ]
  return (
    <SimpleSelect
      size="sm"
      className="w-40 max-w-full"
      aria-label={t("agents.perm.for", { name })}
      value={value ?? "default"}
      options={options}
      onChange={(v) => onChange(v === "allow" || v === "ask" || v === "deny" ? v : null)}
    />
  )
}

// How many tools a closed group holds, and how many are destructive.
export function GroupMeta({ list, permissions, className }: { list: ToolItem[]; permissions?: ToolsViewProps["permissions"]; className?: string }) {
  const t = useT()
  const count = useCount()
  const destructive = countClass(list, "destructive")
  const allowed = permissions ? list.filter((x) => permissions.tools[x.name] === "allow").length : 0
  const off = permissions ? list.filter((x) => permissions.tools[x.name] === "deny").length : 0
  return (
    <span className={cn("flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-caption font-normal text-muted-foreground", className)}>
      <span className="tabular-nums">{count(list.length, "agents.tools.count.one", "agents.tools.count.many")}</span>
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

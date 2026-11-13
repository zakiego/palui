// A flow's steps drawn as a graph: triggers on top, each step as a card, its
// branches as labelled arrows, and a small "ends" wherever a run can finish.
// Picking a step shows everything about it under the graph, with its branches
// as links to the steps they lead to, so nothing from the old list is lost.
import * as React from "react"
import type { LucideIcon } from "lucide-react"
import {
  CalendarClockIcon,
  ClockIcon,
  CornerDownRightIcon,
  HandIcon,
  MessageCircleQuestionMarkIcon,
  MessageSquareIcon,
  MessagesSquareIcon,
  RefreshCwIcon,
  RepeatIcon,
  SparklesIcon,
  SplitIcon,
  VariableIcon,
  WebhookIcon,
  WrenchIcon,
  ZapIcon,
} from "lucide-react"

import { ClampText } from "@/components/clamp-text"
import { TemplateText } from "@/components/template-text"
import { Badge } from "@/components/ui/badge"
import { StatusText } from "@/components/ui/status-text"
import { layoutFlow, stepKey, type EdgeTone, type LaidEdge, type LaidNode } from "@/lib/flow-graph"
import { useLabel, useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type GraphFlowStep = {
  id: string
  kind: string
  name: string | null
  summary: string
  write: boolean
  startOf: string[]
  edges: { label: string; to: string | null }[]
  ends: boolean
}
export type GraphFlowTrigger = { id: string; kind: string; start: string; summary: string }

const STEP_ICON: Record<string, LucideIcon> = {
  tool: WrenchIcon,
  if: SplitIcon,
  message: MessageSquareIcon,
  checkin: MessageCircleQuestionMarkIcon,
  wait: ClockIcon,
  prompt: SparklesIcon,
  set: VariableIcon,
  each: RepeatIcon,
}

const TRIGGER_ICON: Record<string, LucideIcon> = {
  schedule: CalendarClockIcon,
  webhook: WebhookIcon,
  chat: MessagesSquareIcon,
  poll: RefreshCwIcon,
  event: ZapIcon,
  manual: HandIcon,
}

// Code-like summaries read better in mono, as in the run log.
const MONO = new Set(["if", "tool", "set", "each"])

const TONE_STROKE: Record<EdgeTone, string> = {
  next: "stroke-muted-foreground/60",
  branch: "stroke-muted-foreground/60",
  body: "stroke-muted-foreground/60 [stroke-dasharray:4_3]",
  error: "stroke-destructive/70 [stroke-dasharray:4_3]",
}

export function FlowGraph({ steps, triggers }: { steps: GraphFlowStep[]; triggers: GraphFlowTrigger[] }) {
  const t = useT()
  const label = useLabel()
  const layout = React.useMemo(() => layoutFlow(steps, triggers), [steps, triggers])
  const byId = React.useMemo(() => new Map(steps.map((s) => [s.id, s])), [steps])
  const [selected, setSelected] = React.useState<string | null>(() => triggers[0]?.start ?? steps[0]?.id ?? null)
  const [hover, setHover] = React.useState<string | null>(null)
  const nodeRefs = React.useRef(new Map<string, HTMLButtonElement>())
  const current = selected ? byId.get(selected) : undefined
  // Edges touching the hovered (or else the picked) step are drawn darker.
  const focus = hover ?? selected
  const lit = (e: LaidEdge) => !!focus && (e.from === stepKey(focus) || e.to === stepKey(focus))

  // A wide or tall flow scrolls inside its own box. Start with the first step
  // in view, since a wide row of branches can push it off to the side.
  const boxRef = React.useRef<HTMLDivElement>(null)
  React.useLayoutEffect(() => {
    const box = boxRef.current
    const first = layout.nodes.find((n) => n.kind === "step")
    if (!box || !first) return
    box.scrollLeft = first.x + first.w / 2 - box.clientWidth / 2
    box.scrollTop = 0
  }, [layout])

  const pick = (id: string) => {
    setSelected(id)
    nodeRefs.current.get(id)?.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" })
  }

  return (
    <div className="flex flex-col gap-4">
      <div ref={boxRef} className="max-h-[70svh] overflow-auto overscroll-contain pb-1">
        <div role="group" aria-label={t("flow.steps")} className="relative mx-auto" style={{ width: layout.width, height: layout.height }}>
          <svg aria-hidden="true" className="absolute inset-0 overflow-visible" width={layout.width} height={layout.height}>
            <defs>
              <marker id="flow-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0 0 L8 4 L0 8 z" className="fill-muted-foreground/70" />
              </marker>
              <marker id="flow-arrow-lit" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0 0 L8 4 L0 8 z" className="fill-foreground" />
              </marker>
            </defs>
            {layout.edges.map((e) => (
              <path
                key={e.key}
                d={e.path}
                fill="none"
                strokeWidth={lit(e) ? 1.75 : 1.25}
                markerEnd={`url(#${lit(e) ? "flow-arrow-lit" : "flow-arrow"})`}
                className={cn("transition-[stroke,stroke-width] duration-150", lit(e) ? (e.tone === "error" ? "stroke-destructive" : "stroke-foreground") : TONE_STROKE[e.tone])}
              />
            ))}
          </svg>
          {layout.edges.map((e) =>
            e.labelAt ? (
              <span
                key={`label-${e.key}`}
                aria-hidden="true"
                className={cn(
                  "pointer-events-none absolute -translate-x-1/2 -translate-y-1/2 rounded-sm bg-card px-1 font-mono text-[11px] leading-4 whitespace-nowrap transition-colors duration-150",
                  e.tone === "error" ? "text-destructive-fg" : lit(e) ? "text-foreground" : "text-muted-foreground",
                )}
                style={{ left: e.labelAt.x, top: e.labelAt.y }}
              >
                {e.label}
              </span>
            ) : null,
          )}
          {layout.nodes.map((n) =>
            n.kind === "step" ? (
              <StepNode
                key={n.key}
                node={n}
                step={byId.get(n.ref)!}
                selected={selected === n.ref}
                onSelect={() => setSelected(n.ref)}
                onHover={(on) => setHover(on ? n.ref : null)}
                ref={(el) => {
                  if (el) nodeRefs.current.set(n.ref, el)
                  else nodeRefs.current.delete(n.ref)
                }}
              />
            ) : n.kind === "trigger" ? (
              <TriggerNode key={n.key} node={n} triggers={triggers.filter((tr) => tr.start === n.ref)} />
            ) : (
              <span
                key={n.key}
                className="absolute flex items-center justify-center rounded-full border bg-muted text-caption text-muted-foreground"
                style={{ left: n.x, top: n.y, width: n.w, height: n.h }}
              >
                {t("flow.ends")}
              </span>
            ),
          )}
        </div>
      </div>
      {current && <StepDetail step={current} label={label} onPick={pick} />}
    </div>
  )
}

function StepNode({
  node,
  step,
  selected,
  onSelect,
  onHover,
  ref,
}: {
  node: LaidNode
  step: GraphFlowStep
  selected: boolean
  onSelect: () => void
  onHover: (on: boolean) => void
  ref: React.Ref<HTMLButtonElement>
}) {
  const t = useT()
  const label = useLabel()
  const Icon = STEP_ICON[step.kind] ?? WrenchIcon
  return (
    <button
      ref={ref}
      type="button"
      aria-pressed={selected}
      onClick={onSelect}
      onPointerEnter={() => onHover(true)}
      onPointerLeave={() => onHover(false)}
      onFocus={() => onHover(true)}
      onBlur={() => onHover(false)}
      className={cn(
        "absolute flex flex-col gap-0.5 rounded-lg border bg-card px-2.5 py-2 text-start shadow-xs outline-none transition-[border-color,box-shadow,background-color] duration-150",
        "hover:bg-accent/50 focus-visible:ring-3 focus-visible:ring-ring/50",
        selected && "border-primary ring-3 ring-primary/15",
      )}
      style={{ left: node.x, top: node.y, width: node.w, height: node.h }}
    >
      <span className="flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
        <Icon aria-hidden="true" className="size-3.5 shrink-0" />
        <span className="shrink-0">{label("flow.kind", step.kind)}</span>
        <span className="min-w-0 truncate font-mono">{step.id}</span>
        {step.write && (
          <StatusText tone="warning" className="ms-auto" title={t("flow.write")}>
            {t("flow.write")}
          </StatusText>
        )}
      </span>
      <span dir="auto" className="truncate text-sm font-medium">
        {step.name || step.id}
      </span>
      <span dir="auto" className={cn("truncate text-xs text-muted-foreground", MONO.has(step.kind) && "font-mono")}>
        <TemplateText text={step.summary} />
      </span>
    </button>
  )
}

// Every trigger that starts at the same step, as one pill: its kind and when
// for a single trigger, the kinds for several, and all of them on hover.
function TriggerNode({ node, triggers }: { node: LaidNode; triggers: GraphFlowTrigger[] }) {
  const label = useLabel()
  const first = triggers[0]!
  const Icon = TRIGGER_ICON[first.kind] ?? ZapIcon
  const kinds = [...new Set(triggers.map((tr) => label("flow.trigger", tr.kind)))].join(" · ")
  return (
    <span
      title={triggers.map((tr) => `${tr.id}: ${tr.summary}`).join("\n")}
      className="absolute flex items-center gap-1.5 rounded-full border border-dashed bg-card px-3 text-xs text-muted-foreground"
      style={{ left: node.x, top: node.y, width: node.w, height: node.h }}
    >
      <Icon aria-hidden="true" className="size-3.5 shrink-0" />
      {triggers.length === 1 ? (
        <>
          <span className="shrink-0 font-medium text-foreground">{kinds}</span>
          <span dir="auto" className="min-w-0 truncate">
            <TemplateText text={first.summary} />
          </span>
        </>
      ) : (
        <>
          <span className="shrink-0 font-medium text-foreground tabular-nums">{triggers.length}×</span>
          <span className="min-w-0 truncate">{kinds}</span>
        </>
      )}
    </span>
  )
}

// Everything about the picked step, in full: the graph only has room for a line.
function StepDetail({ step, label, onPick }: { step: GraphFlowStep; label: ReturnType<typeof useLabel>; onPick: (id: string) => void }) {
  const t = useT()
  return (
    <div aria-live="polite" className="flex min-w-0 flex-col gap-1 border-t pt-4">
      <span className="flex flex-wrap items-center gap-1.5">
        <span className="font-mono text-sm break-all">{step.id}</span>
        <Badge variant="outline">{label("flow.kind", step.kind)}</Badge>
        {step.write && <StatusText tone="warning">{t("flow.write")}</StatusText>}
        {step.startOf.length > 0 && <span className="text-caption text-muted-foreground">{t("flow.startOf", { triggers: step.startOf.join(", ") })}</span>}
      </span>
      {step.name && (
        <span dir="auto" className="text-sm font-medium break-words">
          {step.name}
        </span>
      )}
      <ClampText key={step.id} text={step.summary} lines={3} className={cn("text-sm break-words whitespace-pre-wrap text-muted-foreground", MONO.has(step.kind) && "font-mono text-xs")}>
        <TemplateText text={step.summary} />
      </ClampText>
      <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
        {step.edges.map((e) =>
          e.to ? (
            <button
              key={`${e.label}-${e.to}`}
              type="button"
              onClick={() => onPick(e.to!)}
              className="inline-flex min-w-0 items-center gap-1 rounded-sm underline-offset-4 outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <CornerDownRightIcon aria-hidden="true" className="size-3 shrink-0" />
              <span className="break-all">
                {e.label} → {e.to}
              </span>
            </button>
          ) : (
            <span key={`${e.label}-end`} className="inline-flex items-center gap-1">
              <CornerDownRightIcon aria-hidden="true" className="size-3 shrink-0" />
              {e.label} → {t("flow.ends")}
            </span>
          ),
        )}
        {step.ends && <span>{t("flow.ends")}</span>}
      </span>
    </div>
  )
}

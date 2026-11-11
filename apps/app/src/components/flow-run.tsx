// A flow run on the run page (RFC flows 10): what started it, its status and
// skip reason, the trigger data, and the step log, each step with its input,
// output, the branch it took and its error. Tool and prompt steps that used a
// turn link to it.
import { Link } from "@tanstack/react-router"
import { ArrowLeftIcon, WorkflowIcon } from "lucide-react"

import { FLOW_RUN_TONE } from "@/components/flow-badges"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { StatusText } from "@/components/ui/status-text"
import { duration, inZone } from "@/lib/format"
import { useLabel, useT } from "@/lib/i18n"

export type FlowRunData = {
  id: string
  flowId: string
  flowName: string | null
  status: string
  skipReason: string | null
  trigger: string
  triggerKind: string
  runKey: string
  startedAt: number
  endedAt: number | null
  scheduledAt: number | null
  wakeAt: number | null
  waiting: string | null
  error: string | null
  tainted: boolean
  taint: string[]
  parentRunId: string | null
  payload: string
  steps: { seq: number; stepId: string; kind: string; status: string; branch: string | null; error: string | null; ms: number | null; tainted: boolean; attempt: number; input: string | null; output: string | null; turnRunId: string | null }[]
  children: { id: string; status: string; item: number | null }[]
}

const STEP_TONE: Record<string, "success" | "destructive" | "secondary" | "warning" | "outline"> = {
  ok: "success",
  error: "destructive",
  skipped: "secondary",
  waiting: "outline",
  running: "outline",
  stubbed: "secondary",
}

function Json({ title, text }: { title: string; text: string }) {
  return (
    <details className="group min-w-0">
      <summary className="cursor-pointer text-caption text-muted-foreground select-none">{title}</summary>
      <pre className="mt-1 max-h-72 overflow-auto rounded-md bg-muted p-2 font-mono text-xs break-words whitespace-pre-wrap">{text}</pre>
    </details>
  )
}

export function FlowRunView({ run, timezone }: { run: FlowRunData; timezone: string }) {
  const t = useT()
  const label = useLabel()
  const stats: { label: string; value: string; wide?: boolean }[] = [
    { label: t("table.started"), value: inZone(run.startedAt, timezone) },
    ...(run.scheduledAt ? [{ label: t("flowrun.scheduled"), value: inZone(run.scheduledAt, timezone) }] : []),
    ...(run.endedAt ? [{ label: t("flowrun.ended"), value: inZone(run.endedAt, timezone) }] : []),
    { label: t("table.time"), value: duration(run.endedAt ? run.endedAt - run.startedAt : null) },
    { label: t("run.steps"), value: String(run.steps.length) },
    { label: t("flowrun.runKey"), value: run.runKey, wide: true },
  ]
  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <div className="flex flex-col gap-3">
        <Button variant="ghost" size="sm" className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/flows" />}>
          <ArrowLeftIcon data-icon="inline-start" />
          {t("nav.flows")}
        </Button>
        <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
          <div className="flex min-w-0 flex-col gap-2">
            <h1 dir="auto" className="line-clamp-3 font-heading text-title break-words" title={run.flowName ?? undefined}>
              {run.flowName ?? t("flowrun.title")}
            </h1>
            <div className="flex flex-wrap items-center gap-1.5">
              <StatusText tone={FLOW_RUN_TONE[run.status] ?? "outline"}>
                {label("flow.status", run.status)}
                {run.skipReason ? ` · ${label("flow.skip", run.skipReason)}` : ""}
              </StatusText>
              <Badge variant="outline">{label("flow.trigger", run.triggerKind)}</Badge>
              {run.tainted && (
                <StatusText tone="warning" title={run.taint.join(", ")}>
                  {t("run.tainted")}
                </StatusText>
              )}
            </div>
            {run.waiting && run.wakeAt && <p className="text-sm text-muted-foreground tabular-nums">{t("flowrun.waiting", { kind: run.waiting, time: inZone(run.wakeAt, timezone) })}</p>}
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" render={<Link to="/flows/$id" params={{ id: run.flowId }} />}>
              <WorkflowIcon data-icon="inline-start" />
              {t("flowrun.openFlow")}
            </Button>
            {run.parentRunId && (
              <Button variant="outline" size="sm" render={<Link to="/activity/runs/$id" params={{ id: run.parentRunId }} />}>
                {t("flowrun.title")}
              </Button>
            )}
          </div>
        </div>
      </div>
      <Card size="sm">
        <CardContent>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-4 @xl/main:grid-cols-3">
            {stats.map((s) => (
              <div key={s.label} className={s.wide ? "col-span-full min-w-0" : "min-w-0"}>
                <dt className="text-caption text-muted-foreground">{s.label}</dt>
                <dd className="mt-0.5 font-mono text-sm break-all tabular-nums">{s.value}</dd>
              </div>
            ))}
          </dl>
        </CardContent>
      </Card>
      {run.error && (
        <Alert variant="destructive" role="note" className="min-w-0">
          <AlertDescription className="min-w-0">
            {/* A stack trace has file paths with no place to wrap: break anywhere. */}
            <pre className="max-h-80 min-w-0 overflow-auto font-mono text-xs break-all whitespace-pre-wrap">{run.error}</pre>
          </AlertDescription>
        </Alert>
      )}
      <Card size="sm">
        <CardHeader>
          <CardTitle className="tabular-nums">
            {t("flowrun.steps")} · {run.steps.length}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {run.steps.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("flowrun.noSteps")}</p>
          ) : (
            <ol className="flex flex-col divide-y">
              {run.steps.map((s) => (
                <li key={s.seq} className="flex min-w-0 flex-col gap-1.5 py-3 first:pt-0 last:pb-0">
                  <span className="flex flex-wrap items-center gap-1.5">
                    <span className="font-mono text-caption text-muted-foreground tabular-nums">{s.seq}</span>
                    <span className="font-mono text-sm break-all">{s.stepId}</span>
                    <Badge variant="outline">{label("flow.kind", s.kind)}</Badge>
                    <StatusText tone={STEP_TONE[s.status] ?? "outline"}>{s.status}</StatusText>
                    {s.branch && <span className="font-mono text-caption text-muted-foreground">→ {s.branch}</span>}
                    {s.attempt > 1 && <span className="text-caption text-muted-foreground">{t("flowrun.attempt", { n: String(s.attempt) })}</span>}
                    {s.tainted && <StatusText tone="warning">{t("run.tainted")}</StatusText>}
                    {s.ms != null && <span className="text-caption text-muted-foreground tabular-nums">{duration(s.ms)}</span>}
                  </span>
                  {/* An ok step can carry a note (items dropped over an each's max). */}
                  {s.error && <p className={`text-sm break-words ${s.status === "error" ? "text-destructive" : "text-muted-foreground"}`}>{s.error}</p>}
                  {s.input && <Json title={t("flowrun.input")} text={s.input} />}
                  {s.output && <Json title={t("flowrun.output")} text={s.output} />}
                  {s.turnRunId && (
                    <Link to="/activity/runs/$id" params={{ id: s.turnRunId }} className="self-start text-caption underline underline-offset-4">
                      {t("flowrun.openTurn")}
                    </Link>
                  )}
                </li>
              ))}
            </ol>
          )}
        </CardContent>
      </Card>
      {run.children.length > 0 && (
        <Card size="sm">
          <CardHeader>
            <CardTitle className="tabular-nums">
              {t("flowrun.items")} · {run.children.length}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="flex flex-wrap gap-x-4 gap-y-2">
              {run.children.map((c) => (
                <li key={c.id}>
                  <Link to="/activity/runs/$id" params={{ id: c.id }} className="inline-flex items-center gap-1.5 underline-offset-4 hover:underline">
                    <span className="text-sm tabular-nums">{t("flowrun.item", { n: String((c.item ?? 0) + 1) })}</span>
                    <StatusText tone={FLOW_RUN_TONE[c.status] ?? "outline"}>{label("flow.status", c.status)}</StatusText>
                  </Link>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
      <Card size="sm">
        <CardHeader>
          <CardTitle>{t("flowrun.payload")}</CardTitle>
        </CardHeader>
        <CardContent>
          <pre className="max-h-72 overflow-auto font-mono text-xs break-words whitespace-pre-wrap">{run.payload}</pre>
        </CardContent>
      </Card>
    </div>
  )
}

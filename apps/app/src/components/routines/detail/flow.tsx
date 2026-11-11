// The routine detail page (picked in the 2026-11-11 Explore, see DESIGN.md).
// The routine is drawn as the steps it runs, one under the other on a wire:
// When, then Ask or Send, then what happens with no answer, then the recap.
// It's the shape a multi-step Flow will have, so this page grows into that one
// by adding steps. Activity (next times, done per item) sits beside it and the
// log is below.
import type * as React from "react"
import { ChartColumnIcon, CircleAlertIcon, CircleXIcon, ClockIcon, PlugZapIcon, RepeatIcon } from "lucide-react"

import { actionIcon, useRecapText } from "@/components/routines/routine-parts"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Progress } from "@/components/ui/progress"
import { inOtherZone, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"
import { LogCard, Notices, RawConfig, type Config, type Detail, type SetRange } from "./parts"
import { daysText, reAskText, RoutineHeader, triggerText } from "./summary"

export function FlowLayout({ detail, timezone, setRange }: { detail: Detail; timezone: string; setRange: SetRange }) {
  const t = useT()
  return (
    <div className="flex w-full max-w-5xl flex-col gap-8">
      <RoutineHeader detail={detail} timezone={timezone} />
      <Notices detail={detail} timezone={timezone} />
      <div className="grid items-start gap-8 @4xl/main:grid-cols-[minmax(0,1fr)_18rem]">
        {detail.config ? (
          <Steps detail={detail} config={detail.config} timezone={timezone} />
        ) : (
          <Alert variant="destructive" role="note">
            <CircleAlertIcon aria-hidden="true" />
            <AlertTitle>{t("routines.invalidConfig")}</AlertTitle>
            <AlertDescription>
              {t("routines.invalidConfigFix")}
              {detail.configProblem && <span className="mt-1 block font-mono text-xs [overflow-wrap:anywhere]">{detail.configProblem}</span>}
            </AlertDescription>
          </Alert>
        )}
        <div className="flex flex-col gap-6">
          <UpNext detail={detail} timezone={timezone} />
          <DonePerItem detail={detail} />
        </div>
      </div>
      <LogCard detail={detail} timezone={timezone} setRange={setRange} />
      <RawConfig json={detail.rawJson} />
    </div>
  )
}

// A main step sits on the wire; a branch (what happens with no answer) is
// indented off it and drawn lighter.
type Step = { key: string; icon: React.ReactNode; title: string; branch?: boolean; body: React.ReactNode }

function Steps({ detail, config, timezone }: { detail: Detail; config: Config; timezone: string }) {
  const t = useT()
  const recap = useRecapText()
  const s = config.source
  const a = config.action
  const ActionIcon = actionIcon(a.kind)
  const steps: Step[] = [
    {
      key: "when",
      icon: s.kind === "tool" ? <PlugZapIcon /> : <ClockIcon />,
      title: t("routines.flow.when"),
      body: (
        <>
          <p className="text-sm">{triggerText(t, config)}</p>
          <Meta>
            {s.kind === "tool" ? t("routines.flow.reads", { source: `${s.connectorId}.${s.tool}`, time: s.refreshAt }) : s.kind === "rrule" ? s.rrule : daysText(t, config.days)}
            {" · "}
            {config.tz}
          </Meta>
        </>
      ),
    },
    {
      key: "do",
      icon: <ActionIcon />,
      title: a.kind === "checkin" ? t("routines.flow.ask") : a.kind === "message" ? t("routines.flow.send") : t("routines.flow.prompt"),
      body: (
        <>
          <Quote>{a.kind === "prompt" ? a.prompt : a.text}</Quote>
          {a.kind === "checkin" && (
            // Answer options are real tags: what the owner can reply.
            <div className="flex flex-wrap gap-1.5">
              {a.answers.map((x) => (
                <Badge key={x.id} variant="outline" title={x.closes ? t("routines.flow.closes") : t("routines.flow.keepsAsking")}>
                  {x.label}
                  {!x.closes && <RepeatIcon data-icon="inline-end" aria-label={t("routines.flow.keepsAsking")} />}
                </Badge>
              ))}
            </div>
          )}
        </>
      ),
    },
  ]
  if (a.kind === "checkin") {
    steps.push({
      key: "reask",
      icon: <RepeatIcon />,
      title: t("routines.flow.noAnswer"),
      branch: true,
      body: (
        <>
          <p className="text-sm">{reAskText(t, config)}</p>
          {a.followUp.text && <Quote>{a.followUp.text}</Quote>}
        </>
      ),
    })
  }
  if (a.kind === "checkin" && a.missedText) {
    steps.push({ key: "missed", icon: <CircleXIcon />, title: t("routines.flow.missed"), branch: true, body: <Quote>{a.missedText}</Quote> })
  }
  if (config.recap.kind !== "off") {
    steps.push({
      key: "recap",
      icon: <ChartColumnIcon />,
      title: t("routines.flow.recap"),
      body: (
        <>
          <p className="text-sm">{recap(config.recap)}</p>
          {detail.nextRecapAt && <Meta>{t("routines.next", { time: inZone(detail.nextRecapAt, timezone) })}</Meta>}
        </>
      ),
    })
  }

  return (
    <ol className="flex flex-col gap-5">
      {steps.map((step, i) => (
        <li key={step.key} className={cn("relative flex gap-4", step.branch && "ps-6")}>
          {/* The wire: a segment from this step's icon down to the next one, so it ends at the last step. */}
          {i < steps.length - 1 && <span aria-hidden="true" className="absolute start-[1.1875rem] top-7 -bottom-13 w-px bg-border" />}
          {/* A branch hangs off the wire by a short stub. */}
          {step.branch && <span aria-hidden="true" className="absolute start-[1.1875rem] top-7 h-px w-2 bg-border" />}
          <span
            aria-hidden="true"
            className={cn(
              "z-10 flex shrink-0 items-center justify-center rounded-full border bg-card [&>svg]:size-4",
              step.branch ? "mt-3 size-8 text-muted-foreground" : "mt-2.5 size-10 text-foreground",
            )}
          >
            {step.icon}
          </span>
          <Card size="sm" className={cn("min-w-0 flex-1", step.branch && "bg-transparent shadow-none ring-0")}>
            <CardContent className="flex flex-col gap-3">
              <h2 className="text-overline text-muted-foreground uppercase">{step.title}</h2>
              {step.body}
            </CardContent>
          </Card>
        </li>
      ))}
    </ol>
  )
}

function Quote({ children }: { children: React.ReactNode }) {
  return (
    <p dir="auto" className="line-clamp-3 rounded-md bg-muted px-3 py-2 text-sm [overflow-wrap:anywhere]">
      {children}
    </p>
  )
}

function Meta({ children }: { children: React.ReactNode }) {
  return <p className="text-caption text-muted-foreground [overflow-wrap:anywhere]">{children}</p>
}

function UpNext({ detail, timezone }: { detail: Detail; timezone: string }) {
  const t = useT()
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>{t("routines.flow.upNext")}</CardTitle>
      </CardHeader>
      <CardContent>
        {detail.preview.length ? (
          <ol className="flex flex-col gap-3">
            {detail.preview.slice(0, 6).map((o) => (
              <li key={o.at} className="flex items-baseline justify-between gap-3 text-sm">
                <span className="min-w-0 truncate" title={o.label}>
                  {o.label}
                  {o.fallback && <span className="ms-1.5 text-caption text-muted-foreground">({t("routines.fallback")})</span>}
                </span>
                <span className="shrink-0 font-mono text-caption whitespace-nowrap text-muted-foreground tabular-nums" title={inOtherZone(o.at, detail.routine.tz, timezone) ?? undefined}>
                  {inZone(o.at, timezone)}
                </span>
              </li>
            ))}
          </ol>
        ) : (
          <p className="text-sm text-muted-foreground">{detail.routine.active ? t("routines.noNext") : t("routines.pausedNoNext")}</p>
        )}
      </CardContent>
    </Card>
  )
}

// Done out of what ran, per item, for the log's date range: the recap, live.
function DonePerItem({ detail }: { detail: Detail }) {
  const t = useT()
  if (!detail.tally.length) return null
  const total = detail.tally.reduce((n, x) => n + x.counted, 0)
  const done = detail.tally.reduce((n, x) => n + x.done, 0)
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle className="tabular-nums">{t("routines.flow.doneOf", { done, total })}</CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="flex flex-col gap-4">
          {detail.tally.map((x) => (
            <li key={x.label} className="flex flex-col gap-1.5">
              <span className="flex items-baseline justify-between gap-3 text-sm">
                <span className="min-w-0 truncate" title={x.label}>
                  {x.label}
                </span>
                <span className="font-mono text-caption tabular-nums">
                  {x.done}/{x.counted}
                </span>
              </span>
              <Progress value={x.counted ? (x.done / x.counted) * 100 : 0} aria-label={t("routines.flow.progress", { label: x.label, done: x.done, total: x.counted })} />
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

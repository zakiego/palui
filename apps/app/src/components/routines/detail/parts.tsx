// Routine detail pieces: notices, the log with its date range, and the raw JSON.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { ArrowLeftIcon, ChevronRightIcon, CircleAlertIcon, MessageCircleQuestionIcon } from "lucide-react"

import { ClampText } from "@/components/clamp-text"
import { ConnectorNotice, OccurrenceStatus } from "@/components/routines/routine-parts"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { getRoutine } from "@/lib/fns/routines"
import { inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"

type Data = Awaited<ReturnType<typeof getRoutine>>
export type Detail = NonNullable<Data["detail"]>
export type Config = NonNullable<Detail["config"]>

export function BackButton() {
  const t = useT()
  return (
    <Button variant="ghost" size="sm" nativeButton={false} className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/routines" />}>
      <ArrowLeftIcon data-icon="inline-start" />
      {t("nav.routines")}
    </Button>
  )
}

// A failing source, its connector off, and check-ins waiting for an answer.
export function Notices({ detail, timezone }: { detail: Detail; timezone: string }) {
  const t = useT()
  const r = detail.routine
  const newest = detail.open[0]
  return (
    <>
      {r.lastError && (
        // A stored error, not a live one: no role="alert", so it isn't announced on every visit.
        <Alert variant="destructive" role="note">
          <CircleAlertIcon aria-hidden="true" />
          <AlertTitle>{t("routines.sourceError")}</AlertTitle>
          <AlertDescription>
            <pre className="max-h-60 overflow-auto font-mono text-xs break-words whitespace-pre-wrap">{r.lastError}</pre>
          </AlertDescription>
        </Alert>
      )}
      <ConnectorNotice connector={r.connector} />
      {newest && (
        // One notice for every waiting check-in; each one is also a row in the log.
        <Alert variant="warning">
          <MessageCircleQuestionIcon aria-hidden="true" />
          <AlertTitle className="line-clamp-2 [overflow-wrap:anywhere]">{t("routines.waitingTitle", { labels: [...new Set(detail.open.map((o) => o.label))].join(", ") })}</AlertTitle>
          <AlertDescription>
            {t("routines.askedAt", { time: inZone(newest.scheduledAt, timezone) })}
            {newest.nextFollowUpAt ? ` ${t("routines.nextReAsk", { time: inZone(newest.nextFollowUpAt, timezone) })}` : ""}
          </AlertDescription>
        </Alert>
      )}
    </>
  )
}

// --- Log ------------------------------------------------------------------------------

const STATUS_ORDER = ["open", "done", "missed", "sent", "skipped", "failed"] as const

export type SetRange = (next: { from?: string; to?: string }) => void

export function LogCard({ detail, timezone, setRange }: { detail: Detail; timezone: string; setRange: SetRange }) {
  const t = useT()
  const shown = STATUS_ORDER.filter((s) => detail.counts[s] > 0)
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <CardHeader className="border-b py-4">
        <CardTitle>{t("routines.section.log")}</CardTitle>
        {shown.length > 0 && (
          <CardDescription className="flex flex-wrap gap-x-4 gap-y-1">
            {shown.map((s) => (
              <OccurrenceStatus key={s} status={s} count={detail.counts[s]} />
            ))}
          </CardDescription>
        )}
      </CardHeader>
      {/* The date range, as its own toolbar row so it wraps cleanly on a phone. */}
      <div className="flex flex-wrap items-end gap-3 border-b px-4 py-4">
        <Field className="w-auto">
          <FieldLabel htmlFor="log-from">{t("routines.from")}</FieldLabel>
          <DateInput id="log-from" value={detail.range.from} max={detail.range.to} onCommit={(from) => setRange({ from })} />
        </Field>
        <Field className="w-auto">
          <FieldLabel htmlFor="log-to">{t("routines.to")}</FieldLabel>
          <DateInput id="log-to" value={detail.range.to} min={detail.range.from} onCommit={(to) => setRange({ to })} />
        </Field>
        <Button variant="outline" onClick={() => setRange({ from: undefined, to: undefined })}>
          {t("routines.last7")}
        </Button>
      </div>
      {detail.log.length === 0 ? (
        <p className="px-4 py-6 text-sm text-muted-foreground">{t("routines.logEmpty")}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-44">{t("routines.col.when")}</TableHead>
              <TableHead>{t("routines.col.item")}</TableHead>
              <TableHead className="hidden w-28 @xl/main:table-cell">{t("routines.col.status")}</TableHead>
              <TableHead className="hidden @3xl/main:table-cell">{t("routines.col.answer")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {detail.log.map((o) => (
              <TableRow key={o.id}>
                <TableCell className="align-top font-mono text-caption leading-5 whitespace-nowrap tabular-nums">{inZone(o.scheduledAt, timezone)}</TableCell>
                <TableCell className="max-w-0 align-top whitespace-normal">
                  <span dir="auto" className="block [overflow-wrap:anywhere]">
                    {o.label}
                  </span>
                  <span className="@xl/main:hidden">
                    <OccurrenceStatus status={o.status} />
                  </span>
                  <span className="block @3xl/main:hidden">
                    <LogAnswer o={o} timezone={timezone} />
                  </span>
                </TableCell>
                <TableCell className="hidden align-top leading-5 @xl/main:table-cell">
                  <OccurrenceStatus status={o.status} />
                </TableCell>
                <TableCell className="hidden max-w-0 align-top whitespace-normal @3xl/main:table-cell">
                  <LogAnswer o={o} timezone={timezone} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {detail.truncated && <p className="border-t px-4 py-3 text-caption text-muted-foreground">{t("routines.logTruncated")}</p>}
    </Card>
  )
}

// A date field that only reloads the log once the date is whole: on blur, on
// Enter, or when the picker sets a full date. Typing "2" into the year field
// doesn't fetch year 0002.
function DateInput({ id, value, min, max, onCommit }: { id: string; value: string; min?: string; max?: string; onCommit: (v: string) => void }) {
  const [draft, setDraft] = React.useState(value)
  React.useEffect(() => setDraft(value), [value])
  const whole = (v: string) => /^\d{4}-\d{2}-\d{2}$/.test(v) && Number(v.slice(0, 4)) >= 2000
  const commit = (v: string) => {
    if (!whole(v)) return setDraft(value)
    if (v !== value) onCommit(v)
  }
  return (
    <Input
      id={id}
      type="date"
      className="w-40"
      value={draft}
      min={min}
      max={max}
      onChange={(e) => {
        setDraft(e.target.value)
        // A date picked from the calendar (or a fully typed year) applies at once.
        if (whole(e.target.value) && e.target.value !== value) onCommit(e.target.value)
      }}
      onBlur={() => commit(draft)}
      onKeyDown={(e) => e.key === "Enter" && commit(draft)}
    />
  )
}

function LogAnswer({ o, timezone }: { o: Detail["log"][number]; timezone: string }) {
  const t = useT()
  const bits: string[] = []
  if (o.answeredAt) bits.push(t("routines.answeredAt", { time: inZone(o.answeredAt, timezone) }))
  if (o.reAsks) bits.push(o.reAsks === 1 ? t("routines.reAsk1") : t("routines.reAsks", { n: o.reAsks }))
  if (o.status === "open" && o.nextFollowUpAt) bits.push(t("routines.reAskAt", { time: inZone(o.nextFollowUpAt, timezone) }))
  if (o.status === "open" && o.deadlineAt) bits.push(t("routines.deadlineAt", { time: inZone(o.deadlineAt, timezone) }))
  if (o.fallback) bits.push(t("routines.fallback"))
  return (
    <span className="block min-w-0 text-caption">
      {o.answerLabel && <span className="font-medium">{o.answerLabel}</span>}
      {o.note && <ClampText text={`"${o.note}"`} lines={2} className="text-muted-foreground [overflow-wrap:anywhere]" />}
      {bits.length > 0 && <span className="block text-muted-foreground">{bits.join(" · ")}</span>}
    </span>
  )
}

export function RawConfig({ json }: { json: string }) {
  const t = useT()
  return (
    <Collapsible>
      <CollapsibleTrigger render={<Button variant="ghost" size="sm" className="group -ms-2.5" />}>
        <ChevronRightIcon data-icon="inline-start" aria-hidden="true" className="transition-transform duration-150 ease-emphasized group-data-[panel-open]:rotate-90 motion-reduce:transition-none rtl:-scale-x-100" />
        {t("routines.section.raw")}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className="mt-2 max-h-[32rem] overflow-auto rounded-lg border bg-muted p-3 font-mono text-xs">{json}</pre>
      </CollapsibleContent>
    </Collapsible>
  )
}


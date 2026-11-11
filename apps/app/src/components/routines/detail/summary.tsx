// A routine's config as short plain sentences ("Every day at 08:00, 20:00",
// "Asks Sudah or Belum"), and the header every routine page opens with.
import { useNavigate } from "@tanstack/react-router"

import { actionIcon, RoutineActions, RoutineState } from "@/components/routines/routine-parts"
import { ItemMedia } from "@/components/ui/item"
import { useT, type Key } from "@/lib/i18n"
import { BackButton, type Config, type Detail } from "./parts"

type T = ReturnType<typeof useT>

const WEEKDAYS = ["MO", "TU", "WE", "TH", "FR"]
const day = (t: T, code: string) => t(`routines.day.${code}` as Key)

export function daysText(t: T, days: string[] | null): string {
  if (!days || days.length === 7) return t("routines.everyDay")
  if (days.length === 5 && WEEKDAYS.every((d) => days.includes(d))) return t("routines.sum.weekdays")
  if (days.length === 2 && days.includes("SA") && days.includes("SU")) return t("routines.sum.weekends")
  return days.map((d) => day(t, d)).join(", ")
}

const FREQS = ["DAILY", "WEEKLY", "MONTHLY", "YEARLY"]
const pad = (n: string) => n.padStart(2, "0")

// The common RRULE shapes in words ("Every Fri at 16:00", "Every 2 weeks on
// day 1 at 09:00"). Anything else says only "Repeats"; the rule itself is on
// the step's second line.
function rruleText(t: T, rrule: string, start: string): string {
  const parts = Object.fromEntries(rrule.split(";").map((p) => p.split("=") as [string, string]))
  const freq = parts.FREQ ?? ""
  if (!FREQS.includes(freq)) return t("routines.sum.repeats")
  const interval = Number(parts.INTERVAL ?? 1)
  let when = interval > 1 ? t(`routines.sum.everyN.${freq}` as Key, { n: interval }) : t(`routines.sum.freq.${freq}` as Key)
  const byDay = parts.BYDAY?.split(",").map((d) => d.replace(/^[+-]?\d+/, ""))
  if (freq === "WEEKLY") {
    const days = byDay?.length ? byDay : []
    if (days.length && interval === 1) when = t("routines.sum.everyDays", { days: days.map((d) => day(t, d)).join(", ") })
    else if (days.length) when = t("routines.sum.onDays", { when, days: days.map((d) => day(t, d)).join(", ") })
  }
  if (freq === "MONTHLY" && parts.BYMONTHDAY) when = t("routines.sum.onMonthDay", { when, day: parts.BYMONTHDAY })
  // BYHOUR x BYMINUTE when the rule names them, else the start's time of day.
  const hours = parts.BYHOUR?.split(",") ?? [start.slice(11, 13)]
  const minutes = parts.BYMINUTE?.split(",") ?? [start.slice(14, 16)]
  const times = hours.flatMap((h) => minutes.map((m) => `${pad(h)}:${pad(m)}`))
  return t("routines.sum.at", { when, times: times.join(", ") })
}

export function triggerText(t: T, config: Config): string {
  const s = config.source
  switch (s.kind) {
    case "times":
      return t("routines.sum.at", { when: daysText(t, config.days), times: s.times.map((x) => x.time).join(", ") })
    case "rrule":
      return rruleText(t, s.rrule, s.start)
    case "tool":
      return s.items
        ? t("routines.sum.toolItems", { days: daysText(t, config.days), source: s.connectorId, items: s.items.map((i) => i.label ?? i.field).join(", ") })
        : t("routines.sum.toolList", { source: s.connectorId })
  }
}

export function reAskText(t: T, config: Config): string | null {
  const a = config.action
  if (a.kind !== "checkin") return null
  const until = a.deadline.kind === "next" ? t("routines.sum.untilNext") : t("routines.sum.forMin", { n: a.deadline.minutes })
  if (a.followUp.max === 0) return t("routines.sum.once", { until })
  return t("routines.sum.reAsk", { every: a.followUp.everyMin, max: a.followUp.max, until })
}

// Name, state, chat and the actions.
export function RoutineHeader({ detail, timezone }: { detail: Detail; timezone: string }) {
  const t = useT()
  const navigate = useNavigate()
  const r = detail.routine
  const Icon = actionIcon(r.actionKind)
  return (
    <div className="flex flex-col gap-3">
      <BackButton />
      <div className="flex flex-wrap items-start gap-4">
        <ItemMedia variant="icon" className="hidden size-10 @md/main:flex">
          <Icon aria-hidden="true" />
        </ItemMedia>
        <div className="flex min-w-[min(100%,16rem)] flex-1 flex-col gap-1">
          <h1 dir="auto" className="font-heading text-title [overflow-wrap:anywhere]">
            {r.name}
          </h1>
          <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
            <RoutineState routine={r} timezone={timezone} />
            <span className="min-w-0 [overflow-wrap:anywhere]">{r.chat ? `${r.chat.title} · ${r.chat.channel}` : t("routines.deletedChat")}</span>
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2 @md/main:ms-auto">
          <RoutineActions routine={r} timezone={timezone} layout="buttons" logCount={detail.logTotal} onDeleted={() => void navigate({ to: "/routines" })} />
        </div>
      </div>
    </div>
  )
}

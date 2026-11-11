// Pieces the Routines list and detail pages share: the active/paused state,
// an occurrence status, the source connector warning, and the pause, resume
// and delete actions. Routines are shown as trigger + action + log, so a
// multi-step flow can reuse the same parts later.
import * as React from "react"
import { Link, useNavigate, useRouter } from "@tanstack/react-router"
import { CirclePauseIcon, EllipsisIcon, ListChecksIcon, MessageCircleQuestionIcon, MessageSquareTextIcon, PlugZapIcon, SendIcon, SkipForwardIcon, PlayIcon, SparklesIcon, WorkflowIcon, type LucideIcon } from "lucide-react"
import { toast } from "sonner"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { StatusText, type StatusTone } from "@/components/ui/status-text"
import { deleteRoutineFn, pauseRoutineFn, resumeRoutineFn, type getRoutines } from "@/lib/fns/routines"
import { clip, errorMessage, inZone } from "@/lib/format"
import { useLabel, useT, type Key } from "@/lib/i18n"

export type RoutineSummary = Awaited<ReturnType<typeof getRoutines>>["routines"][number]
type Recap = RoutineSummary["recap"]

export function RoutineState({ routine, timezone }: { routine: Pick<RoutineSummary, "active" | "pausedUntil">; timezone: string }) {
  const t = useT()
  if (routine.active) {
    return (
      <StatusText tone="success">
        {t("routines.active")}
      </StatusText>
    )
  }
  return (
    <StatusText tone="secondary" icon={<CirclePauseIcon aria-hidden="true" />} className="whitespace-normal">
      {routine.pausedUntil ? t("routines.pausedUntil", { time: inZone(routine.pausedUntil, timezone) }) : t("routines.paused")}
    </StatusText>
  )
}

// One icon per action kind, so a row reads at a glance.
const ACTION_ICON: Record<string, LucideIcon> = { message: MessageSquareTextIcon, checkin: ListChecksIcon, prompt: SparklesIcon }

export function actionIcon(kind: string): LucideIcon {
  return ACTION_ICON[kind] ?? WorkflowIcon
}

const OCC_TONE: Record<string, StatusTone> = { open: "warning", done: "success", missed: "destructive", sent: "success", skipped: "secondary", failed: "destructive" }
const OCC_ICON: Record<string, React.ReactNode> = {
  open: <MessageCircleQuestionIcon aria-hidden="true" />,
  sent: <SendIcon aria-hidden="true" />,
  skipped: <SkipForwardIcon aria-hidden="true" />,
}

export function OccurrenceStatus({ status, count }: { status: string; count?: number }) {
  const label = useLabel()
  return (
    <StatusText tone={OCC_TONE[status] ?? "outline"} icon={OCC_ICON[status]}>
      {label("routine.status", status)}
      {count !== undefined && <span className="text-foreground tabular-nums">{count.toLocaleString("en")}</span>}
    </StatusText>
  )
}

export function useRecapText() {
  const t = useT()
  return React.useCallback(
    (recap: Recap) => {
      if (!recap || recap.kind === "off") return t("routines.recap.off")
      const time = recap.time.replace(":", ".")
      if (recap.kind === "daily") return t("routines.recap.daily", { time })
      return t("routines.recap.weekly", { day: t(`routines.day.${recap.day}` as Key), time })
    },
    [t],
  )
}

export function useKindLabel() {
  const label = useLabel()
  return React.useCallback((part: "source" | "action", kind: string) => label(`routines.kind.${part}`, kind), [label])
}

// The connector a tool source reads from, when it can't right now.
export function ConnectorNotice({ connector, compact = false }: { connector: RoutineSummary["connector"]; compact?: boolean }) {
  const t = useT()
  if (!connector || connector.state === "ok") return null
  const name = connector.label ?? connector.id
  const title = t(`routines.connector.${connector.state}`, { id: connector.id, name })
  if (compact) {
    return (
      <StatusText tone="warning" icon={<PlugZapIcon aria-hidden="true" />} className="max-w-full whitespace-normal">
        <span className="min-w-0 break-words">{title}</span>
      </StatusText>
    )
  }
  return (
    <Alert variant="warning">
      <PlugZapIcon aria-hidden="true" />
      <AlertTitle className="break-words">{title}</AlertTitle>
      {/* Off needs no words: the title says it and the action turns it back on. */}
      {connector.state !== "disabled" && <AlertDescription className="break-words">{connector.state === "missing" ? t("routines.connector.missingBody") : connector.detail}</AlertDescription>}
      {connector.state !== "missing" && (
        <AlertAction>
          <Button size="sm" variant="outline" render={<Link to="/connectors/$id" params={{ id: connector.id }} />}>
            {t("routines.connector.open")}
          </Button>
        </AlertAction>
      )}
    </Alert>
  )
}

// --- Actions --------------------------------------------------------------------

type Target = Pick<RoutineSummary, "id" | "name" | "active" | "tz" | "chat" | "pausedUntil">

// Pause never asks: it applies at once and the toast offers Undo (DESIGN.md).
function useRoutineWrites(routine: Target, timezone: string) {
  const t = useT()
  const router = useRouter()
  const refresh = () => void router.invalidate()
  const undo = (fn: () => Promise<unknown>) => ({ label: t("common.undo"), onClick: () => void fn().then(refresh, (err: unknown) => toast.error(errorMessage(err))) })
  const pause = async (until: string | null) => {
    const res = await pauseRoutineFn({ data: { id: routine.id, until } })
    toast.success(res.pausedUntil ? t("routines.pausedUntilToast", { name: routine.name, time: inZone(res.pausedUntil, timezone) }) : t("routines.pausedToast", { name: routine.name }), {
      action: undo(() => resumeRoutineFn({ data: { id: routine.id } })),
    })
    refresh()
  }
  const resume = async () => {
    try {
      await resumeRoutineFn({ data: { id: routine.id } })
      // Undo puts the old pause back, end time included.
      const until = routine.pausedUntil ? localInput(routine.pausedUntil, routine.tz) : null
      toast.success(t("routines.resumedToast", { name: routine.name }), { action: undo(() => pauseRoutineFn({ data: { id: routine.id, until } })) })
      refresh()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return { pause, resume }
}

// Pause, pause until, resume and delete. `layout="menu"` is the row's "…" menu;
// `layout="buttons"` is the detail page header.
export function RoutineActions({ routine, timezone, logCount, layout, onDeleted }: { routine: Target; timezone: string; logCount?: number; layout: "menu" | "buttons"; onDeleted?: () => void }) {
  const t = useT()
  const navigate = useNavigate()
  const { pause, resume } = useRoutineWrites(routine, timezone)
  const [until, setUntil] = React.useState(false)
  const [confirm, setConfirm] = React.useState(false)
  const pauseNow = () => pause(null).catch((err) => toast.error(errorMessage(err)))

  const dialogs = (
    <>
      <PauseUntilDialog open={until} onOpenChange={setUntil} routine={routine} onPause={pause} />
      <DeleteDialog open={confirm} onOpenChange={setConfirm} routine={routine} logCount={logCount} onDeleted={onDeleted} />
    </>
  )

  const menu = (trigger: React.ReactElement) => (
    <DropdownMenu>
      <DropdownMenuTrigger render={trigger}>
        <EllipsisIcon aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        {layout === "menu" && (
          <>
            <DropdownMenuGroup>
              <DropdownMenuItem onClick={() => void navigate({ to: "/routines/$id", params: { id: routine.id } })}>{t("routines.view")}</DropdownMenuItem>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuGroup>
          {layout === "menu" &&
            (routine.active ? (
              <DropdownMenuItem onClick={() => void pauseNow()}>{t("routines.pause")}</DropdownMenuItem>
            ) : (
              <DropdownMenuItem onClick={() => void resume()}>{t("routines.resume")}</DropdownMenuItem>
            ))}
          {routine.active && <DropdownMenuItem onClick={() => setUntil(true)}>{t("routines.pauseUntil")}</DropdownMenuItem>}
          {routine.chat && <DropdownMenuItem onClick={() => void navigate({ to: "/chats", search: { chat: routine.chat!.id } })}>{t("routines.editInChat")}</DropdownMenuItem>}
        </DropdownMenuGroup>
        {(layout === "menu" || routine.active || routine.chat) && <DropdownMenuSeparator />}
        <DropdownMenuGroup>
          <DropdownMenuItem variant="destructive" onClick={() => setConfirm(true)}>
            {t("routines.delete")}
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )

  if (layout === "buttons") {
    // One visible action; the rest (pause until, edit in chat, delete) sit in the menu.
    return (
      <>
        <Button variant="outline" onClick={() => void (routine.active ? pauseNow() : resume())}>
          {routine.active ? <CirclePauseIcon data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
          {routine.active ? t("routines.pause") : t("routines.resume")}
        </Button>
        {menu(<Button variant="outline" size="icon" aria-label={t("routines.moreFor", { name: clip(routine.name, 40) })} />)}
        {dialogs}
      </>
    )
  }

  return (
    <>
      {menu(<Button variant="ghost" size="icon-sm" aria-label={t("routines.actionsFor", { name: clip(routine.name, 40) })} />)}
      {dialogs}
    </>
  )
}

// A UTC time as a datetime-local value in the routine's zone.
function localInput(ms: number, tz: string): string {
  const parts = new Intl.DateTimeFormat("en-CA", { timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(ms)
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ""
  return `${get("year")}-${get("month")}-${get("day")}T${get("hour")}:${get("minute")}`
}

// Tomorrow 07:00 in the routine's zone.
function defaultResume(tz: string): string {
  return `${localInput(Date.now() + 24 * 3600_000, tz).slice(0, 10)}T07:00`
}

function PauseUntilDialog({ open, onOpenChange, routine, onPause }: { open: boolean; onOpenChange: (v: boolean) => void; routine: Target; onPause: (until: string) => Promise<void> }) {
  const t = useT()
  const [value, setValue] = React.useState(() => defaultResume(routine.tz))
  const [error, setError] = React.useState<string | null>(null)
  const [busy, setBusy] = React.useState(false)
  React.useEffect(() => {
    if (open) {
      setValue(defaultResume(routine.tz))
      setError(null)
    }
  }, [open, routine.tz])
  const submit = async () => {
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return setError(t("routines.pauseUntilPast"))
    setBusy(true)
    try {
      await onPause(value)
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="break-words">{t("routines.pauseUntilTitle", { name: routine.name })}</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <Field data-invalid={!!error || undefined}>
            <FieldLabel htmlFor={`pause-until-${routine.id}`}>{t("routines.pauseUntilField", { tz: routine.tz })}</FieldLabel>
            <Input
              id={`pause-until-${routine.id}`}
              type="datetime-local"
              required
              value={value}
              aria-invalid={!!error || undefined}
              onChange={(e) => {
                setValue(e.target.value.slice(0, 16))
                setError(null)
              }}
            />
            {error && <FieldError>{error}</FieldError>}
          </Field>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner data-icon="inline-start" />}
              {t("routines.pause")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DeleteDialog({ open, onOpenChange, routine, logCount, onDeleted }: { open: boolean; onOpenChange: (v: boolean) => void; routine: Target; logCount?: number; onDeleted?: () => void }) {
  const t = useT()
  const router = useRouter()
  const remove = async () => {
    try {
      await deleteRoutineFn({ data: { id: routine.id } })
      toast.success(t("routines.deletedToast", { name: routine.name }))
      if (onDeleted) onDeleted()
      else void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="break-words">{t("routines.deleteTitle", { name: routine.name })}</AlertDialogTitle>
          <AlertDialogDescription>{!logCount ? t("routines.deleteBodyNoCount") : t("routines.deleteBody", { n: logCount.toLocaleString("en") })}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
          <AlertDialogAction variant="danger" onClick={() => void remove()}>
            {t("routines.delete")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// Reminders (PLAN 6, 11.4): Upcoming, Recurring and Done. The New reminder
// dialog takes natural language and previews the parsed time live, with the
// same echo the bot sends ("Besok, Rabu 11 Nov, 09.00 WITA").
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { BellIcon, BellPlusIcon, ClockAlertIcon, EllipsisIcon, RepeatIcon, SparklesIcon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { ListCapNote } from "@/components/list-cap-note"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, TableSkeleton, TabsSkeleton } from "@/components/page-pending"
import { SimpleSelect } from "@/components/simple-select"
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
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { StatusText } from "@/components/ui/status-text"
import { Card } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { cancelReminderFn, createReminderFn, getReminders, parseReminder, snoozeReminderFn } from "@/lib/fns/reminders"
import { clip, errorMessage, inOtherZone, inZone } from "@/lib/format"
import { useLabel, useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/reminders")({
  validateSearch: z.object({ new: z.boolean().optional(), tab: z.enum(["upcoming", "recurring", "done"]).optional() }),
  loader: () => getReminders(),
  head: () => ({ meta: [{ title: "Reminders · Palui" }] }),
  component: RemindersPage,
  pendingComponent: RemindersSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getReminders>>
type Row = Data["upcoming"][number]

function RemindersPage() {
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  const tab = search.tab ?? "upcoming"
  const setNew = (open: boolean) => void navigate({ search: (p) => ({ ...p, new: open || undefined }), replace: true })

  return (
    <>
      <PageHeader
        title={t("reminders.title")}
        description={t("reminders.description")}
        actions={
          <Button onClick={() => setNew(true)}>
            <BellPlusIcon data-icon="inline-start" />
            {t("reminders.new")}
          </Button>
        }
      />
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: (p) => ({ ...p, tab: v as typeof tab }), replace: true })} className="gap-4">
        <TabsList>
          <TabsTrigger value="upcoming">
            {t("reminders.upcoming")} <span className="font-mono text-muted-foreground tabular-nums">{data.totals.upcoming.toLocaleString("en")}</span>
          </TabsTrigger>
          <TabsTrigger value="recurring">
            {t("reminders.recurring")} <span className="font-mono text-muted-foreground tabular-nums">{data.totals.recurring.toLocaleString("en")}</span>
          </TabsTrigger>
          <TabsTrigger value="done">{t("reminders.done")}</TabsTrigger>
        </TabsList>
        <TabsContent value="upcoming">
          <ReminderTable rows={data.upcoming} total={data.totals.upcoming} timezone={data.timezone} onNew={() => setNew(true)} />
        </TabsContent>
        <TabsContent value="recurring">
          <ReminderTable rows={data.recurring} total={data.totals.recurring} timezone={data.timezone} onNew={() => setNew(true)} />
        </TabsContent>
        <TabsContent value="done">
          <ReminderTable rows={data.done} total={data.totals.done} timezone={data.timezone} done />
        </TabsContent>
      </Tabs>
      <NewReminderDialog open={!!search.new} onOpenChange={setNew} data={data} />
    </>
  )
}

// A pending reminder this far past its time didn't fire when it should have.
const OVERDUE_AFTER_MS = 2 * 60_000

function ReminderTable({ rows, total, timezone, done, onNew }: { rows: Row[]; total: number; timezone: string; done?: boolean; onNew?: () => void }) {
  const t = useT()
  const label = useLabel()
  const now = Date.now()
  if (rows.length === 0) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <BellIcon />
          </EmptyMedia>
          <EmptyTitle>{t("reminders.empty")}</EmptyTitle>
          {!done && <EmptyDescription>{t("reminders.emptyUpcoming")}</EmptyDescription>}
        </EmptyHeader>
        {onNew && (
          <EmptyContent>
            <Button variant="outline" onClick={onNew}>
              {t("reminders.new")}
            </Button>
          </EmptyContent>
        )}
      </Empty>
    )
  }
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("table.reminder")}</TableHead>
            <TableHead className="w-52 max-sm:hidden">{done ? t("table.status") : t("table.fires")}</TableHead>
            <TableHead className="w-56 max-md:hidden">{t("table.chat")}</TableHead>
            {!done && (
              <TableHead className="w-12">
                <span className="sr-only">{t("table.actions")}</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => {
            // Every time on this page is in the owner's zone, so the list reads in order.
            // A reminder set in another zone adds its own clock next to it.
            const elsewhere = inOtherZone(r.fireAt, r.tz, timezone)
            const overdue = r.status === "pending" && r.fireAt < now - OVERDUE_AFTER_MS
            const chat = r.chat?.title || r.chat?.channel || t("runs.deletedChat")
            return (
              <TableRow key={r.id}>
                <TableCell className="max-w-0 whitespace-normal">
                  <ClampText text={r.text} lines={3} />
                  <span className="mt-1 flex flex-wrap items-center gap-1.5 text-caption text-muted-foreground">
                    {!done && (
                      <span className="font-mono tabular-nums sm:hidden">
                        {inZone(r.fireAt, timezone)}
                        {elsewhere ? ` (${elsewhere})` : ""}
                      </span>
                    )}
                    {overdue && (
                      <StatusText tone="warning" icon={<ClockAlertIcon aria-hidden="true" />}>
                        {t("reminders.overdue")}
                      </StatusText>
                    )}
                    {r.status === "firing" && <StatusText tone="outline">{label("reminder.status", r.status)}</StatusText>}
                    {done && <StatusText tone={r.status === "done" ? "success" : "secondary"} className="sm:hidden">{label("reminder.status", r.status)}</StatusText>}
                    {r.rrule && (
                      <Badge variant="outline" title={r.rrule}>
                        <RepeatIcon aria-hidden="true" />
                        <span className="max-w-48 truncate font-mono">{r.rrule}</span>
                      </Badge>
                    )}
                    {r.smart && (
                      <Badge variant="soft">
                        <SparklesIcon aria-hidden="true" />
                        {t("reminders.smart")}
                      </Badge>
                    )}
                    {/* The chat column is gone below md; three reminders called "Standup" still need telling apart. */}
                    <span dir="auto" className="max-w-full min-w-0 truncate md:hidden" title={chat}>
                      {chat}
                    </span>
                  </span>
                </TableCell>
                <TableCell className="font-mono text-caption whitespace-normal tabular-nums max-sm:hidden">
                  {done ? <StatusText tone={r.status === "done" ? "success" : "secondary"}>{label("reminder.status", r.status)}</StatusText> : inZone(r.fireAt, timezone)}
                  {!done && elsewhere && <span className="block text-caption break-words text-muted-foreground">{elsewhere}</span>}
                </TableCell>
                <TableCell dir="auto" className="max-w-0 truncate text-left max-md:hidden" title={chat}>
                  {chat}
                </TableCell>
                {!done && (
                  <TableCell>
                    <RowActions row={r} timezone={timezone} />
                  </TableCell>
                )}
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      <ListCapNote shown={rows.length} total={total} />
    </Card>
  )
}

function RowActions({ row, timezone }: { row: Row; timezone: string }) {
  const t = useT()
  const router = useRouter()
  const [confirm, setConfirm] = React.useState(false)
  const snooze = async (preset: "10m" | "1h" | "tomorrow") => {
    try {
      const res = await snoozeReminderFn({ data: { id: row.id, preset } })
      toast.success(t("reminders.snoozed", { time: inZone(res.fireAt, timezone) }))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  const cancel = async () => {
    try {
      await cancelReminderFn({ data: { id: row.id } })
      toast.success(t("reminders.cancelled"))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("reminders.actionsFor", { text: clip(row.text, 40) })} />}>
          <EllipsisIcon aria-hidden="true" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-48">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("reminders.snooze")}</DropdownMenuLabel>
            <DropdownMenuItem onClick={() => void snooze("10m")}>{t("reminders.snooze10m")}</DropdownMenuItem>
            <DropdownMenuItem onClick={() => void snooze("1h")}>{t("reminders.snooze1h")}</DropdownMenuItem>
            <DropdownMenuItem onClick={() => void snooze("tomorrow")}>{t("reminders.snoozeTomorrow")}</DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem variant="destructive" onClick={() => setConfirm(true)}>
              {t("reminders.cancel")}
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("reminders.cancelTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {row.rrule ? t("reminders.cancelRecurring") : t("reminders.cancelOnce")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
            <AlertDialogAction variant="danger" onClick={() => void cancel()}>
              {t("reminders.cancel")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

type Parsed = Awaited<ReturnType<typeof parseReminder>>

function NewReminderDialog({ open, onOpenChange, data }: { open: boolean; onOpenChange: (o: boolean) => void; data: Data }) {
  const t = useT()
  const router = useRouter()
  const [text, setText] = React.useState("")
  const [chatId, setChatId] = React.useState(data.defaultChatId ?? "")
  const [parsed, setParsed] = React.useState<Parsed | null>(null)
  const [parsing, setParsing] = React.useState(false)
  const [error, setError] = React.useState<string>()
  const [saving, setSaving] = React.useState(false)

  React.useEffect(() => {
    if (!open) {
      setText("")
      setParsed(null)
      setError(undefined)
    }
  }, [open])

  // Parse 600 ms after typing stops; a newer keystroke drops the older answer.
  React.useEffect(() => {
    const value = text.trim()
    if (!value) {
      setParsed(null)
      setError(undefined)
      return
    }
    let alive = true
    const timer = setTimeout(async () => {
      setParsing(true)
      try {
        const res = await parseReminder({ data: { text: value, tz: data.timezone } })
        if (alive) {
          setParsed(res)
          setError(undefined)
        }
      } catch (err) {
        if (alive) {
          setParsed(null)
          setError(errorMessage(err))
        }
      } finally {
        if (alive) setParsing(false)
      }
    }, 600)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [text, data.timezone])

  const ok = !!parsed?.ok && !!parsed.fireAtIso
  const create = async () => {
    if (!parsed?.ok || !parsed.fireAtIso || !chatId) return
    setSaving(true)
    try {
      await createReminderFn({ data: { chatId, text: parsed.text || text.trim(), fireAtIso: parsed.fireAtIso, tz: parsed.tz, rrule: parsed.rrule ?? null } })
      toast.success(t("reminders.set"), { description: parsed.echo })
      onOpenChange(false)
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("reminders.new")}</DialogTitle>
          <DialogDescription>{t("reminders.inputHelp")}</DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void create()
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="reminder-text">{t("reminders.input")}</FieldLabel>
              <Textarea
                id="reminder-text"
                autoFocus
                rows={2}
                maxLength={500}
                placeholder="ingetin bayar listrik besok jam 9"
                value={text}
                className="text-base md:text-sm"
                onChange={(e) => setText(e.target.value)}
                aria-describedby="reminder-preview"
              />
            </Field>
            <div id="reminder-preview" role="status" aria-live="polite" className="min-h-14 rounded-lg bg-muted px-3 py-2">
              <p className="text-overline text-muted-foreground uppercase">{t("reminders.preview")}</p>
              {parsing ? (
                <p className="flex items-center gap-2 text-sm text-muted-foreground">
                  <Spinner aria-hidden="true" />
                  {t("reminders.parsing")}
                </p>
              ) : parsed?.ok ? (
                <p className="text-sm font-medium text-pretty">
                  {parsed.echo ?? parsed.fireAtIso}
                  {parsed.rrule && <span className="ms-2 font-mono text-caption font-normal text-muted-foreground">{parsed.rrule}</span>}
                </p>
              ) : parsed && !parsed.ok ? (
                <p className="text-sm text-destructive-fg">{parsed.error ?? t("reminders.noTime")}</p>
              ) : (
                <p className="text-sm text-muted-foreground">{t("reminders.typeHint")}</p>
              )}
            </div>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <Field>
              <FieldLabel htmlFor="reminder-chat">{t("reminders.chat")}</FieldLabel>
              {data.chats.length ? (
                <SimpleSelect id="reminder-chat" className="w-full" value={chatId} options={data.chats.map((c) => ({ value: c.id, label: `${c.title} (${c.channel})` }))} onChange={setChatId} />
              ) : (
                <FieldDescription>{t("common.noChatsYet")}</FieldDescription>
              )}
            </Field>
          </FieldGroup>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!ok || !chatId || saving}>
              {saving && <Spinner data-icon="inline-start" />}
              {t("reminders.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RemindersSkeleton() {
  return (
    <PagePending page="nav.reminders">
      <PageHeaderSkeleton title="w-36" description="w-80" action="w-36" />
      <div className="flex flex-col gap-4">
        <TabsSkeleton className="w-64" />
        <TableSkeleton
          rows={8}
          columns={[
            { head: "w-16", sub: "sm:hidden" },
            { className: "w-52 max-sm:hidden", head: "w-10", text: "font-mono text-caption", short: "w-28", sub: true },
            { className: "w-56 max-md:hidden", head: "w-8", short: "w-28" },
            { className: "w-12", controls: ["size-4 rounded-sm"] },
          ]}
        />
      </div>
    </PagePending>
  )
}

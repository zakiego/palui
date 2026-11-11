// Jobs (PLAN 7, 11.4): schedule, next run, last result, Run now, Pause, daily
// cap, with a create/edit Sheet (a Drawer on a phone).
import * as React from "react"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { CalendarClockIcon, PencilIcon, PlayIcon, PlusIcon } from "lucide-react"
import { toast } from "sonner"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { ResponsiveSheet } from "@/components/responsive-sheet"
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Card } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { deleteJobFn, getJobPreset, getJobs, runJobNow, saveJob, setJobEnabledFn } from "@/lib/fns/jobs"
import { compact, errorMessage, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/jobs")({
  loader: () => getJobs(),
  head: () => ({ meta: [{ title: "Jobs · Palui" }] }),
  component: JobsPage,
  pendingComponent: JobsSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getJobs>>
type Job = Data["jobs"][number]

function JobsPage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  // `editing` stays set while the sheet closes, so it keeps its content (and
  // its key) through the exit animation; `sheetOpen` drives open and closed.
  const [editing, setEditingTarget] = React.useState<Job | "new" | null>(null)
  const [sheetOpen, setSheetOpen] = React.useState(false)
  const setEditing = (next: Job | "new") => {
    setEditingTarget(next)
    setSheetOpen(true)
  }
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "jobs" || e.kind.startsWith("job"))

  return (
    <>
      <PageHeader
        title={t("jobs.title")}
        description={t("jobs.description")}
        actions={
          <Button onClick={() => setEditing("new")}>
            <PlusIcon data-icon="inline-start" />
            {t("jobs.new")}
          </Button>
        }
      />
      {data.jobs.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <CalendarClockIcon />
            </EmptyMedia>
            <EmptyTitle>{t("jobs.empty")}</EmptyTitle>
            <EmptyDescription>{t("jobs.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={() => setEditing("new")}>{t("jobs.new")}</Button>
          </EmptyContent>
        </Empty>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("table.job")}</TableHead>
                <TableHead className="w-40 max-lg:hidden">{t("table.nextRun")}</TableHead>
                <TableHead className="w-48 max-md:hidden">{t("table.lastResult")}</TableHead>
                <TableHead className="w-36 max-xl:hidden">{t("table.dailyCap")}</TableHead>
                <TableHead className="w-44 text-end">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.jobs.map((j) => (
                <JobRow key={j.id} job={j} onEdit={() => setEditing(j)} />
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <JobSheet key={editing === "new" ? "new" : (editing?.id ?? "none")} open={sheetOpen} target={editing} onClose={() => setSheetOpen(false)} data={data} />
    </>
  )
}

const STATUS_TONE: Record<string, "success" | "destructive" | "secondary" | "warning" | "outline"> = {
  ok: "success",
  error: "destructive",
  aborted: "secondary",
  budget: "warning",
  awaiting_approval: "warning",
}

function JobRow({ job, onEdit }: { job: Job; onEdit: () => void }) {
  const t = useT()
  const router = useRouter()
  const [enabled, setEnabled] = React.useState(job.enabled)
  React.useEffect(() => setEnabled(job.enabled), [job.enabled])
  const pct = Math.min(100, Math.round((job.tokensToday / Math.max(1, job.dailyTokenCap)) * 100))

  const runNow = () => {
    const p = runJobNow({ data: { id: job.id } })
    toast.promise(p, {
      loading: `Starting ${job.name}`,
      success: `${job.name} is running. The result lands in ${job.chatTitle}.`,
      error: (err) => errorMessage(err),
    })
    void p.then(() => router.invalidate()).catch(() => {})
  }

  const toggle = async (next: boolean) => {
    setEnabled(next)
    try {
      await setJobEnabledFn({ data: { id: job.id, enabled: next } })
      toast.success(next ? `${job.name} resumed` : `${job.name} paused`, {
        action: { label: t("common.undo"), onClick: () => void setJobEnabledFn({ data: { id: job.id, enabled: !next } }).then(() => router.invalidate()) },
      })
      void router.invalidate()
    } catch (err) {
      setEnabled(!next)
      toast.error(errorMessage(err))
    }
  }

  return (
    <TableRow>
      <TableCell className="max-w-0 whitespace-normal">
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="min-w-0 font-medium break-words">{job.name}</span>
          {job.kind !== "job" && <Badge variant="outline">{job.kind}</Badge>}
          {!job.enabled && <Badge variant="secondary">paused</Badge>}
        </span>
        <span className="block truncate font-mono text-caption text-muted-foreground" title={job.rrule}>
          {job.rrule} · {job.tz}
        </span>
        <span className="block truncate text-caption text-muted-foreground lg:hidden">Next {job.nextRunAt ? inZone(job.nextRunAt, job.tz) : "not scheduled yet"}</span>
      </TableCell>
      <TableCell className="font-mono text-caption tabular-nums max-lg:hidden">{job.nextRunAt ? inZone(job.nextRunAt, job.tz) : <span className="text-muted-foreground">Not scheduled yet</span>}</TableCell>
      <TableCell className="max-w-0 whitespace-normal max-md:hidden">
        {job.lastRunAt ? (
          <>
            <Badge variant={STATUS_TONE[job.lastStatus ?? ""] ?? "outline"}>{job.lastStatus ?? "unknown"}</Badge>
            {job.lastResult && <span className="mt-1 line-clamp-2 text-caption break-words text-muted-foreground">{job.lastResult}</span>}
          </>
        ) : (
          <span className="text-caption text-muted-foreground">{t("common.never")}</span>
        )}
      </TableCell>
      <TableCell className="max-xl:hidden">
        <span className="font-mono text-caption tabular-nums">
          {compact(job.tokensToday)} / {compact(job.dailyTokenCap)}
        </span>
        <Progress value={pct} aria-label={`${pct}% of today's cap used`} className="mt-1" />
      </TableCell>
      <TableCell>
        <span className="flex items-center justify-end gap-1">
          <Button variant="outline" size="sm" onClick={runNow} disabled={job.status === "running"}>
            {job.status === "running" ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
            <span className="max-sm:sr-only">{t("jobs.runNow")}</span>
          </Button>
          <span className="flex size-11 items-center justify-center">
            <Switch checked={enabled} onCheckedChange={(v) => void toggle(v)} aria-label={enabled ? `Pause ${job.name}` : `Resume ${job.name}`} className="after:-inset-x-1.5 after:-inset-y-[13px]" />
          </span>
          <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={`Edit ${job.name}`}>
            <PencilIcon aria-hidden="true" />
          </Button>
        </span>
      </TableCell>
    </TableRow>
  )
}

type Form = { name: string; kind: "job" | "heartbeat" | "briefing"; rrule: string; tz: string; localStart: string; prompt: string; chatId: string; modelRole: "default" | "fast" | "vision"; dailyTokenCap: string }

function JobSheet({ open, target, onClose, data }: { open: boolean; target: Job | "new" | null; onClose: () => void; data: Data }) {
  const t = useT()
  const router = useRouter()
  const isNew = target === "new"
  const job = target && target !== "new" ? target : null
  const [form, setForm] = React.useState<Form>(() => ({
    name: job?.name ?? "",
    kind: job?.kind ?? "job",
    rrule: job?.rrule ?? "FREQ=DAILY;BYHOUR=7;BYMINUTE=0",
    tz: job?.tz ?? data.timezone,
    localStart: (job?.localStart ?? `${new Date().toISOString().slice(0, 10)}T07:00:00`).slice(0, 16),
    prompt: job?.prompt ?? "",
    chatId: job?.chatId ?? data.chats[0]?.id ?? "",
    modelRole: job?.modelRole ?? "default",
    dailyTokenCap: String(job?.dailyTokenCap ?? 50_000),
  }))
  const [errors, setErrors] = React.useState<Partial<Record<keyof Form, string>>>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete" | "preset">(null)
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => ({ ...f, [k]: v }))

  const applyPreset = async (preset: "dailyBriefing" | "heartbeat") => {
    if (!form.chatId) return toast.error(t("jobs.pickChat"))
    setBusy("preset")
    try {
      const p = await getJobPreset({ data: { preset, chatId: form.chatId } })
      setForm((f) => ({ ...f, name: p.name, kind: p.kind ?? "job", rrule: p.rrule, tz: p.tz, localStart: (p.localStart ?? f.localStart).slice(0, 16), prompt: p.prompt, modelRole: p.modelRole ?? "default", dailyTokenCap: String(p.dailyTokenCap ?? 50_000) }))
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const submit = async () => {
    const next: typeof errors = {}
    if (!form.name.trim()) next.name = t("jobs.err.name")
    if (!/^FREQ=/.test(form.rrule.trim())) next.rrule = t("jobs.err.rrule")
    if (!form.prompt.trim()) next.prompt = t("jobs.err.prompt")
    if (!form.chatId) next.chatId = t("jobs.err.chat")
    const cap = Number(form.dailyTokenCap)
    if (!Number.isInteger(cap) || cap < 1000) next.dailyTokenCap = t("jobs.err.cap")
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy("save")
    try {
      await saveJob({
        data: {
          id: job?.id,
          name: form.name.trim(),
          kind: form.kind,
          rrule: form.rrule.trim(),
          tz: form.tz.trim(),
          localStart: form.localStart.length === 16 ? `${form.localStart}:00` : form.localStart,
          prompt: form.prompt.trim(),
          chatId: form.chatId,
          modelRole: form.modelRole,
          dailyTokenCap: cap,
        },
      })
      toast.success(isNew ? t("jobs.created") : t("jobs.saved"))
      onClose()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    if (!job) return
    setBusy("delete")
    try {
      await deleteJobFn({ data: { id: job.id } })
      toast.success(`${job.name} deleted`)
      onClose()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <ResponsiveSheet
      open={open && target !== null}
      onOpenChange={(o) => !o && onClose()}
      title={isNew ? t("jobs.new") : (job?.name ?? "")}
      description={t("jobs.sheetBody")}
      footer={
        <div className="flex w-full gap-2">
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button className="ms-auto" disabled={busy !== null} onClick={() => void submit()}>
            {busy === "save" && <Spinner data-icon="inline-start" />}
            {t("common.save")}
          </Button>
        </div>
      }
    >
      {isNew && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => void applyPreset("dailyBriefing")}>
            Daily briefing preset
          </Button>
          <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => void applyPreset("heartbeat")}>
            Heartbeat preset
          </Button>
        </div>
      )}
      <FieldGroup>
        <Field data-invalid={!!errors.name}>
          <FieldLabel htmlFor="job-name">{t("connectors.field.name")}</FieldLabel>
          <Input id="job-name" value={form.name} maxLength={80} aria-invalid={!!errors.name} onChange={(e) => set("name", e.target.value)} />
          {errors.name && <FieldError>{errors.name}</FieldError>}
        </Field>
        <Field data-invalid={!!errors.rrule}>
          <FieldLabel htmlFor="job-rrule">Schedule (RRULE)</FieldLabel>
          <Input id="job-rrule" className="font-mono" spellCheck={false} autoCapitalize="characters" value={form.rrule} aria-invalid={!!errors.rrule} onChange={(e) => set("rrule", e.target.value)} />
          <FieldDescription>Wall-clock time in the zone below, so 07:00 stays 07:00 across DST.</FieldDescription>
          {errors.rrule && <FieldError>{errors.rrule}</FieldError>}
        </Field>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="job-start">{t("jobs.field.start")}</FieldLabel>
            <Input id="job-start" type="datetime-local" value={form.localStart} onChange={(e) => set("localStart", e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="job-tz">{t("jobs.field.tz")}</FieldLabel>
            <Input id="job-tz" className="font-mono" spellCheck={false} value={form.tz} onChange={(e) => set("tz", e.target.value)} />
          </Field>
        </div>
        <Field data-invalid={!!errors.prompt}>
          <FieldLabel htmlFor="job-prompt">{t("jobs.field.prompt")}</FieldLabel>
          <Textarea id="job-prompt" rows={5} maxLength={4000} value={form.prompt} aria-invalid={!!errors.prompt} onChange={(e) => set("prompt", e.target.value)} />
          {errors.prompt && <FieldError>{errors.prompt}</FieldError>}
        </Field>
        <Field data-invalid={!!errors.chatId}>
          <FieldLabel htmlFor="job-chat">{t("jobs.field.chat")}</FieldLabel>
          {data.chats.length ? (
            <SimpleSelect id="job-chat" className="w-full" value={form.chatId} options={data.chats.map((c) => ({ value: c.id, label: `${c.title} (${c.channel})` }))} onChange={(v) => set("chatId", v)} />
          ) : (
            <FieldDescription>{t("common.noChatsYet")}</FieldDescription>
          )}
          {errors.chatId && <FieldError>{errors.chatId}</FieldError>}
        </Field>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="job-role">{t("jobs.field.role")}</FieldLabel>
            <SimpleSelect
              id="job-role"
              className="w-full"
              value={form.modelRole}
              options={[
                { value: "default", label: "default" },
                { value: "fast", label: "fast" },
                { value: "vision", label: "vision" },
              ]}
              onChange={(v) => set("modelRole", v as Form["modelRole"])}
            />
          </Field>
          <Field data-invalid={!!errors.dailyTokenCap}>
            <FieldLabel htmlFor="job-cap">{t("jobs.field.cap")}</FieldLabel>
            <Input id="job-cap" type="number" inputMode="numeric" min={1000} step={1000} className="font-mono" value={form.dailyTokenCap} aria-invalid={!!errors.dailyTokenCap} onChange={(e) => set("dailyTokenCap", e.target.value)} />
            {errors.dailyTokenCap && <FieldError>{errors.dailyTokenCap}</FieldError>}
          </Field>
        </div>
      </FieldGroup>
      {job && (
        <AlertDialog>
          <AlertDialogTrigger render={<Button variant="destructive" className="self-start" disabled={busy !== null} />}>
            {busy === "delete" && <Spinner data-icon="inline-start" />}
            Delete job
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete {job.name}?</AlertDialogTitle>
              <AlertDialogDescription>{t("jobs.deleteBody")}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
              <AlertDialogAction variant="danger" onClick={() => void remove()}>
                {t("common.delete")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </ResponsiveSheet>
  )
}

function JobsSkeleton() {
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8 w-24" />
          <Skeleton className="h-4 w-96 max-w-full" />
        </div>
        <Skeleton className="h-8 w-28" />
      </div>
      <div className="flex flex-col gap-2 rounded-xl p-2 shadow-(--shadow-border)">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-14" />
        ))}
      </div>
    </>
  )
}

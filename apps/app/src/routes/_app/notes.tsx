// Notes: the owner's standing instructions and key-value preferences. Both go
// into every turn's system prompt; Palui can also save them from chat (the
// notes skill), instructions only after an approval.
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { z } from "zod"
import { NotebookPenIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending } from "@/components/page-pending"
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
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { deleteInstructionFn, deletePreferenceFn, getNotes, saveInstructionFn, setInstructionEnabledFn, setPreferenceFn } from "@/lib/fns/notes"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/notes")({
  validateSearch: z.object({ agent: z.string().max(100).optional() }),
  loaderDeps: ({ search }) => ({ agent: search.agent }),
  loader: ({ deps }) => getNotes({ data: { agentId: deps.agent } }),
  head: () => ({ meta: [{ title: "Notes · Palui" }] }),
  component: NotesPage,
  pendingComponent: NotesSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getNotes>>
type Instruction = Data["instructions"][number]

const TITLE_MAX = 80
const BODY_MAX = 1000
const VALUE_MAX = 300
const KEY_RE = /^[a-z][a-z0-9_]{0,39}$/

function NotesPage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  // `editing` stays set while the sheet closes, so it keeps its content through the exit animation.
  const [editing, setEditingTarget] = React.useState<Instruction | "new" | null>(null)
  const [sheetOpen, setSheetOpen] = React.useState(false)
  const setEditing = (next: Instruction | "new") => {
    setEditingTarget(next)
    setSheetOpen(true)
  }
  // The notes tools log an event whenever chat changes an instruction or preference.
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "notes")

  return (
    <>
      <PageHeader
        title={t("notes.title")}
        actions={
          <Button onClick={() => setEditing("new")}>
            <PlusIcon data-icon="inline-start" />
            {t("notes.newInstruction")}
          </Button>
        }
      />
      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader>
            <CardTitle>{t("notes.instructions")}</CardTitle>
            <CardDescription>{data.agents.length > 1 ? t("notes.instructionsFor", { name: data.agent.name }) : t("notes.instructionsBody")}</CardDescription>
            {data.agents.length > 1 && (
              <SimpleSelect
                className="mt-2 w-full max-w-64"
                aria-label={t("notes.agent")}
                value={data.agent.id}
                options={data.agents.map((a) => ({ value: a.id, label: a.name }))}
                onChange={(id) => void navigate({ search: { agent: id }, replace: true })}
              />
            )}
          </CardHeader>
          <CardContent>
            {data.instructions.length === 0 ? (
              <Empty className="border border-dashed p-6">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <NotebookPenIcon />
                  </EmptyMedia>
                  <EmptyTitle>{t("notes.emptyInstructions")}</EmptyTitle>
                  <EmptyDescription>{t("notes.emptyInstructionsBody")}</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ul className="flex flex-col divide-y">
                {data.instructions.map((i) => (
                  <InstructionRow key={i.id} item={i} onEdit={() => setEditing(i)} />
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
        <PreferencesCard data={data} />
      </div>
      <InstructionSheet key={editing === "new" ? "new" : (editing?.id ?? "none")} open={sheetOpen} target={editing} agentId={data.agent.id} onClose={() => setSheetOpen(false)} />
    </>
  )
}

function InstructionRow({ item, onEdit }: { item: Instruction; onEdit: () => void }) {
  const t = useT()
  const router = useRouter()
  const [enabled, setEnabled] = React.useState(item.enabled)
  React.useEffect(() => setEnabled(item.enabled), [item.enabled])

  const toggle = async (next: boolean) => {
    setEnabled(next)
    try {
      await setInstructionEnabledFn({ data: { id: item.id, enabled: next } })
      void router.invalidate()
    } catch (err) {
      setEnabled(!next)
      toast.error(errorMessage(err))
    }
  }

  return (
    <li className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span dir="auto" className="min-w-0 text-left font-medium [overflow-wrap:anywhere]">
            {item.title}
          </span>
          {!enabled && <StatusText tone="secondary">{t("notes.off")}</StatusText>}
        </span>
        <ClampText text={item.body} lines={3} className="text-sm text-muted-foreground [overflow-wrap:anywhere]" />
      </div>
      <span className="flex w-max shrink-0 items-center gap-1">
        <span className="flex size-11 items-center justify-center">
          <Switch checked={enabled} onCheckedChange={(v) => void toggle(v)} aria-label={t("notes.toggleInstruction", { name: item.title })} className="after:-inset-x-1.5 after:-inset-y-[13px]" />
        </span>
        <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={t("notes.editInstruction", { name: item.title })}>
          <PencilIcon aria-hidden="true" />
        </Button>
      </span>
    </li>
  )
}

function InstructionSheet({ open, target, agentId, onClose }: { open: boolean; target: Instruction | "new" | null; agentId: string; onClose: () => void }) {
  const t = useT()
  const router = useRouter()
  const item = target && target !== "new" ? target : null
  const [title, setTitle] = React.useState(item?.title ?? "")
  const [body, setBody] = React.useState(item?.body ?? "")
  const [errors, setErrors] = React.useState<{ title?: string; body?: string }>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete">(null)

  const submit = async () => {
    const next: typeof errors = {}
    if (!title.trim()) next.title = t("notes.err.title")
    if (!body.trim()) next.body = t("notes.err.body")
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy("save")
    try {
      await saveInstructionFn({ data: { id: item?.id, agentId, title: title.trim(), body: body.trim() } })
      toast.success(t("notes.instructionSaved"))
      onClose()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    if (!item) return
    setBusy("delete")
    try {
      await deleteInstructionFn({ data: { id: item.id } })
      toast.success(t("notes.instructionDeleted", { name: item.title }))
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
      title={item ? item.title : t("notes.newInstruction")}
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
      <FieldGroup>
        <Field data-invalid={!!errors.title}>
          <FieldLabel htmlFor="ins-title">{t("notes.field.title")}</FieldLabel>
          <Input id="ins-title" value={title} maxLength={TITLE_MAX} aria-invalid={!!errors.title} onChange={(e) => setTitle(e.target.value)} />
          {errors.title && <FieldError>{errors.title}</FieldError>}
        </Field>
        <Field data-invalid={!!errors.body}>
          <FieldLabel htmlFor="ins-body">{t("notes.field.body")}</FieldLabel>
          <Textarea id="ins-body" rows={8} maxLength={BODY_MAX} value={body} aria-invalid={!!errors.body} onChange={(e) => setBody(e.target.value)} />
          <FieldDescription className="tabular-nums">{t("notes.chars", { n: body.length.toLocaleString(), max: BODY_MAX.toLocaleString() })}</FieldDescription>
          {errors.body && <FieldError>{errors.body}</FieldError>}
        </Field>
      </FieldGroup>
      {item && (
        <AlertDialog>
          <AlertDialogTrigger render={<Button variant="destructive" className="self-start" disabled={busy !== null} />}>
            {busy === "delete" && <Spinner data-icon="inline-start" />}
            {t("notes.deleteInstruction")}
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle className="[overflow-wrap:anywhere]">{t("notes.deleteInstructionTitle", { name: item.title })}</AlertDialogTitle>
              <AlertDialogDescription>{t("notes.deleteInstructionBody")}</AlertDialogDescription>
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

function PreferencesCard({ data }: { data: Data }) {
  const t = useT()
  const router = useRouter()
  const [key, setKey] = React.useState("")
  const [value, setValue] = React.useState("")
  const [errors, setErrors] = React.useState<{ key?: string; value?: string }>({})
  const [busy, setBusy] = React.useState(false)
  const keyRef = React.useRef<HTMLInputElement>(null)

  const save = async () => {
    const k = key.trim().toLowerCase().replace(/[\s-]+/g, "_")
    const next: typeof errors = {}
    if (!KEY_RE.test(k)) next.key = t("notes.err.key")
    if (!value.trim()) next.value = t("notes.err.value")
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy(true)
    try {
      await setPreferenceFn({ data: { key: k, value: value.trim() } })
      toast.success(t("notes.preferenceSaved", { key: k }))
      setKey("")
      setValue("")
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (k: string, old: string) => {
    try {
      await deletePreferenceFn({ data: { key: k } })
      toast.success(t("notes.preferenceDeleted", { key: k }), {
        action: { label: t("common.undo"), onClick: () => void setPreferenceFn({ data: { key: k, value: old } }).then(() => router.invalidate()) },
      })
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("notes.preferences")}</CardTitle>
        <CardDescription>
          {t("notes.preferencesBody")}
          {data.agents.length > 1 && ` ${t("notes.preferencesPalui")}`}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {data.preferences.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("notes.emptyPreferences")}</p>
        ) : (
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                {/* On a narrow page the value sits under its key instead of in a squeezed column. */}
                <TableHead className="@xl/main:w-2/5">{t("notes.field.key")}</TableHead>
                <TableHead className="hidden @xl/main:table-cell">{t("notes.field.value")}</TableHead>
                <TableHead className="w-24 text-end">
                  <span className="sr-only">{t("table.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.preferences.map((p) => (
                <TableRow key={p.key} className="[&>td]:align-top">
                  <TableCell className="whitespace-normal">
                    <span className="block font-mono text-caption break-all">{p.key}</span>
                    <span dir="auto" className="mt-1 block text-left text-sm [overflow-wrap:anywhere] @xl/main:hidden">
                      <ClampText text={p.value} lines={3} />
                    </span>
                  </TableCell>
                  <TableCell dir="auto" className="hidden text-left whitespace-normal [overflow-wrap:anywhere] @xl/main:table-cell">
                    <ClampText text={p.value} lines={3} />
                  </TableCell>
                  <TableCell>
                    <span className="ms-auto flex w-max items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("notes.editPreference", { key: p.key })}
                        onClick={() => {
                          setKey(p.key)
                          setValue(p.value)
                          keyRef.current?.focus()
                        }}
                      >
                        <PencilIcon aria-hidden="true" />
                      </Button>
                      <Button variant="ghost" size="icon-sm" aria-label={t("notes.deletePreference", { key: p.key })} onClick={() => void remove(p.key, p.value)}>
                        <Trash2Icon aria-hidden="true" />
                      </Button>
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        <form
          className="grid grid-cols-1 items-start gap-3 @2xl/main:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto]"
          onSubmit={(e) => {
            e.preventDefault()
            void save()
          }}
        >
          <Field data-invalid={!!errors.key}>
            <FieldLabel htmlFor="pref-key">{t("notes.field.key")}</FieldLabel>
            <Input
              ref={keyRef}
              id="pref-key"
              className="font-mono"
              spellCheck={false}
              autoCapitalize="none"
              autoComplete="off"
              placeholder="default_city"
              maxLength={40}
              value={key}
              aria-invalid={!!errors.key}
              onChange={(e) => setKey(e.target.value)}
            />
            {errors.key ? <FieldError>{errors.key}</FieldError> : <FieldDescription>{t("notes.keyHint")}</FieldDescription>}
          </Field>
          <Field data-invalid={!!errors.value}>
            <FieldLabel htmlFor="pref-value">{t("notes.field.value")}</FieldLabel>
            <Input id="pref-value" autoComplete="off" placeholder="Banjarmasin" maxLength={VALUE_MAX} value={value} aria-invalid={!!errors.value} onChange={(e) => setValue(e.target.value)} />
            {errors.value && <FieldError>{errors.value}</FieldError>}
          </Field>
          <Button type="submit" variant="outline" className="@2xl/main:mt-7" disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            {t("notes.setPreference")}
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}

function NotesSkeleton() {
  return (
    <PagePending page="nav.notes">
      <PageHeaderSkeleton title="w-20" description="w-96" action="w-36" />
      <Skeleton className="h-48 w-full rounded-xl" />
      <Skeleton className="h-48 w-full rounded-xl" />
    </PagePending>
  )
}

// Notes: the owner's standing instructions and key-value preferences. Both go
// into every turn's system prompt; Palui can also save them from chat (the
// notes skill), instructions only after an approval.
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { z } from "zod"
import { PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { InstructionList, InstructionSheet, useInstructionSheet } from "@/components/instructions"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending } from "@/components/page-pending"
import { SimpleSelect } from "@/components/simple-select"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { deletePreferenceFn, getNotes, setPreferenceFn } from "@/lib/api/notes"
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
const VALUE_MAX = 300
const KEY_RE = /^[a-z][a-z0-9_]{0,39}$/

function NotesPage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  const sheet = useInstructionSheet()
  // The notes tools log an event whenever chat changes an instruction or preference.
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "notes")

  return (
    <>
      <PageHeader
        title={t("notes.title")}
        actions={
          <Button onClick={() => sheet.edit("new")}>
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
            <InstructionList items={data.instructions} onEdit={sheet.edit} />
          </CardContent>
        </Card>
        <PreferencesCard data={data} />
      </div>
      <InstructionSheet key={sheet.key} open={sheet.open} target={sheet.target} agentId={data.agent.id} onClose={sheet.close} />
    </>
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
            <Input id="pref-value" autoComplete="off" placeholder="Jakarta" maxLength={VALUE_MAX} value={value} aria-invalid={!!errors.value} onChange={(e) => setValue(e.target.value)} />
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
      <PageHeaderSkeleton title="w-20" action="w-36" />
      <Skeleton className="h-48 w-full rounded-xl" />
      <Skeleton className="h-48 w-full rounded-xl" />
    </PagePending>
  )
}

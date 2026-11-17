// Commands: the owner's custom chat commands (PLAN 8.3). Each one is a prompt
// saved under a /name, for every agent or one agent. Typing the name in a chat
// runs a normal turn with that prompt, so it can use the agent's tools.
import * as React from "react"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { FileWarningIcon, PencilIcon, PlusIcon, SquareSlashIcon } from "lucide-react"
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
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { deleteCommandFn, getCommandsPage, saveCommandFn, setCommandEnabledFn, type CommandsPage, type CustomCommand } from "@/lib/api/commands"
import { checkCommand, DESCRIPTION_MAX, NAME_MAX, normalizeName, PROMPT_MAX, type CommandErrors } from "@/lib/commands"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/commands")({
  loader: () => getCommandsPage(),
  head: () => ({ meta: [{ title: "Commands · Palui" }] }),
  component: CommandsPageView,
  pendingComponent: CommandsSkeleton,
  errorComponent: AppError,
})

const ALL = "all"

function CommandsPageView() {
  const data = Route.useLoaderData()
  const t = useT()
  const [target, setTarget] = React.useState<CustomCommand | "new" | null>(null)
  const [open, setOpen] = React.useState(false)
  const edit = (next: CustomCommand | "new") => {
    setTarget(next)
    setOpen(true)
  }
  const agentName = (id: string | null) => (id === null ? t("commands.allAgents") : (data.agents.find((a) => a.id === id)?.name ?? t("commands.deletedAgent")))

  return (
    <>
      <PageHeader
        title={t("commands.title")}
        actions={
          <Button onClick={() => edit("new")}>
            <PlusIcon data-icon="inline-start" />
            {t("commands.new")}
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>{t("commands.listTitle")}</CardTitle>
          <CardDescription>{t("commands.listBody")}</CardDescription>
        </CardHeader>
        <CardContent>
          {data.commands.length === 0 ? (
            <Empty className="border border-dashed p-6">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <SquareSlashIcon />
                </EmptyMedia>
                <EmptyTitle>{t("commands.empty")}</EmptyTitle>
                <EmptyDescription>{t("commands.emptyBody")}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <ul className="flex flex-col divide-y">
              {data.commands.map((c) => (
                <CommandRow key={c.id} item={c} scope={agentName(c.agentId)} onEdit={() => edit(c)} />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      <CommandSheet key={target === "new" ? "new" : (target?.id ?? "none")} open={open} target={target} data={data} onClose={() => setOpen(false)} />
    </>
  )
}

function CommandRow({ item, scope, onEdit }: { item: CustomCommand; scope: string; onEdit: () => void }) {
  const t = useT()
  const router = useRouter()
  const [enabled, setEnabled] = React.useState(item.enabled)
  React.useEffect(() => setEnabled(item.enabled), [item.enabled])

  const toggle = async (next: boolean) => {
    setEnabled(next)
    try {
      await setCommandEnabledFn({ data: { id: item.id, enabled: next } })
      void router.invalidate()
    } catch (err) {
      setEnabled(!next)
      toast.error(errorMessage(err))
    }
  }

  return (
    <li className="flex items-start gap-3 py-4 first:pt-0 last:pb-0">
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 font-mono font-medium [overflow-wrap:anywhere]">/{item.name}</span>
          <span dir="auto" className="min-w-0 text-sm text-muted-foreground [overflow-wrap:anywhere]">
            {scope}
          </span>
          {!enabled && <StatusText tone="secondary">{t("commands.off")}</StatusText>}
        </span>
        {item.description && (
          <span dir="auto" className="text-sm [overflow-wrap:anywhere]">
            {item.description}
          </span>
        )}
        <ClampText text={item.prompt} lines={2} className="text-sm text-muted-foreground [overflow-wrap:anywhere]" />
        {item.attachReport && (
          <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <FileWarningIcon aria-hidden="true" className="size-4 shrink-0" />
            {t("commands.withReport")}
          </span>
        )}
      </div>
      <span className="flex w-max shrink-0 items-center gap-1">
        <span className="flex size-11 items-center justify-center">
          <Switch checked={enabled} onCheckedChange={(v) => void toggle(v)} aria-label={t("commands.toggle", { name: `/${item.name}` })} className="after:-inset-x-1.5 after:-inset-y-[13px]" />
        </span>
        <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={t("commands.edit", { name: `/${item.name}` })}>
          <PencilIcon aria-hidden="true" />
        </Button>
      </span>
    </li>
  )
}

function CommandSheet({ open, target, data, onClose }: { open: boolean; target: CustomCommand | "new" | null; data: CommandsPage; onClose: () => void }) {
  const t = useT()
  const router = useRouter()
  const item = target && target !== "new" ? target : null
  const [form, setForm] = React.useState({
    name: item?.name ?? "",
    scope: item?.agentId ?? ALL,
    description: item?.description ?? "",
    prompt: item?.prompt ?? "",
    attachReport: item?.attachReport ?? false,
  })
  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [k]: v }))
  const [errors, setErrors] = React.useState<CommandErrors>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete">(null)

  const scopes = [{ value: ALL, label: t("commands.allAgents") }, ...data.agents.map((a) => ({ value: a.id, label: a.name }))]
  // An agent deleted since the page loaded still shows as the current value.
  if (form.scope !== ALL && !scopes.some((s) => s.value === form.scope)) scopes.push({ value: form.scope, label: t("commands.deletedAgent") })

  const submit = async () => {
    const next = checkCommand(form, data.builtins)
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy("save")
    try {
      const name = normalizeName(form.name)
      await saveCommandFn({
        data: {
          id: item?.id,
          agentId: form.scope === ALL ? null : form.scope,
          name,
          description: form.description.trim(),
          prompt: form.prompt.trim(),
          attachReport: form.attachReport,
          enabled: item?.enabled ?? true,
        },
      })
      toast.success(t("commands.saved", { name: `/${name}` }))
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
      await deleteCommandFn({ data: { id: item.id } })
      toast.success(t("commands.deleted", { name: `/${item.name}` }))
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
      title={item ? `/${item.name}` : t("commands.new")}
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
        <Field data-invalid={!!errors.name}>
          <FieldLabel htmlFor="cmd-name">{t("commands.field.name")}</FieldLabel>
          <InputGroup>
            <InputGroupAddon>
              <InputGroupText className="font-mono">/</InputGroupText>
            </InputGroupAddon>
            <InputGroupInput
              id="cmd-name"
              className="font-mono"
              placeholder="report-fix"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              maxLength={NAME_MAX + 1}
              value={form.name}
              aria-invalid={!!errors.name}
              onChange={(e) => set("name", e.target.value)}
            />
          </InputGroup>
          {errors.name ? <FieldError>{t(errors.name.key, { name: errors.name.name ?? "" })}</FieldError> : <FieldDescription>{t("commands.nameHint")}</FieldDescription>}
        </Field>
        <Field>
          <FieldLabel htmlFor="cmd-scope">{t("commands.field.scope")}</FieldLabel>
          <SimpleSelect id="cmd-scope" value={form.scope} options={scopes} onChange={(v) => set("scope", v)} />
          <FieldDescription>{t("commands.scopeHint")}</FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="cmd-description">{t("commands.field.description")}</FieldLabel>
          <Input id="cmd-description" maxLength={DESCRIPTION_MAX} value={form.description} onChange={(e) => set("description", e.target.value)} />
          <FieldDescription>{t("commands.descriptionHint")}</FieldDescription>
        </Field>
        <Field data-invalid={!!errors.prompt}>
          <FieldLabel htmlFor="cmd-prompt">{t("commands.field.prompt")}</FieldLabel>
          <Textarea id="cmd-prompt" rows={8} maxLength={PROMPT_MAX} value={form.prompt} aria-invalid={!!errors.prompt} onChange={(e) => set("prompt", e.target.value)} />
          <FieldDescription>{t("commands.promptHint")}</FieldDescription>
          <FieldDescription className="tabular-nums">{t("notes.chars", { n: form.prompt.length.toLocaleString(), max: PROMPT_MAX.toLocaleString() })}</FieldDescription>
          {errors.prompt && <FieldError>{t(errors.prompt.key)}</FieldError>}
        </Field>
        <Field orientation="horizontal">
          <FieldContent>
            <FieldLabel htmlFor="cmd-report">{t("commands.field.report")}</FieldLabel>
            <FieldDescription>{t("commands.reportHint")}</FieldDescription>
          </FieldContent>
          <Switch id="cmd-report" checked={form.attachReport} onCheckedChange={(v) => set("attachReport", v)} />
        </Field>
      </FieldGroup>
      {item && (
        <AlertDialog>
          <AlertDialogTrigger render={<Button variant="destructive" className="self-start" disabled={busy !== null} />}>
            {busy === "delete" && <Spinner data-icon="inline-start" />}
            {t("commands.delete")}
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle className="[overflow-wrap:anywhere]">{t("commands.deleteTitle", { name: `/${item.name}` })}</AlertDialogTitle>
              <AlertDialogDescription>{t("commands.deleteBody")}</AlertDialogDescription>
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

function CommandsSkeleton() {
  return (
    <PagePending page="nav.commands">
      <PageHeaderSkeleton title="w-28" action="w-36" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </PagePending>
  )
}

// The connector sheet (PLAN 11.4 Connectors): the setup wizard generated from the
// manifest's `setup` steps and zod schemas, Test, Connect, recent events, and
// the danger area. A Drawer under 768px. Closing it keeps progress as a draft.
import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { toast } from "sonner"
import type { ConnectorManifest, SetupStep } from "@palui/core/connectors/define"

import { ConnectorStatusBadge } from "@/components/connector-status"
import { ResponsiveSheet } from "@/components/responsive-sheet"
import { SimpleSelect } from "@/components/simple-select"
import { CircleAlertIcon } from "lucide-react"
import { RuntimeAlert } from "@/components/runtime-alert"
import { WizardAction } from "@/components/connectors/wizard-actions"
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
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldSet, FieldLegend } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { usePoll } from "@/hooks/use-poll"
import { connectConnectorFn, getConnectorDetail, removeConnectorFn, saveConnectorDraft, testConnectorFn } from "@/lib/fns/connectors"
import { errorMessage, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"
import type { JsonObject } from "@/lib/json"
import { type FieldStep, type WizardField, wizardFieldInfo as fieldInfo, wizardInitialValues, wizardPayload } from "@/lib/zod-fields"

export type SheetTarget = {
  id: string
  type: string
  label: string
  isNew: boolean
  status: string
  statusDetail: string | null
  config: JsonObject
  secrets: Record<string, string>
  secretsError: string | null
  trust: "own" | "external"
}

type ActionStep = Extract<SetupStep, { action: string }>

export function ConnectorSheet({
  open,
  onOpenChange,
  target,
  manifest,
  appUrl,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  target: SheetTarget | null
  manifest: ConnectorManifest | undefined
  appUrl: string
}) {
  const t = useT()
  if (!target || !manifest) {
    return (
      <ResponsiveSheet open={open} onOpenChange={onOpenChange} title={t("common.loading")}>
        <Skeleton className="h-40" />
      </ResponsiveSheet>
    )
  }
  return <SheetBody key={target.id} open={open} onOpenChange={onOpenChange} target={target} manifest={manifest} appUrl={appUrl} />
}

function SheetBody({
  open,
  onOpenChange,
  target,
  manifest: m,
  appUrl,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  target: SheetTarget
  manifest: ConnectorManifest
  appUrl: string
}) {
  const t = useT()
  const router = useRouter()
  const [values, setValues] = React.useState(() => wizardInitialValues(m, target.config))
  const [label, setLabel] = React.useState(target.label)
  const [trust, setTrust] = React.useState(target.trust)
  const [dirty, setDirty] = React.useState(false)
  const [errors, setErrors] = React.useState<Record<string, string>>({})
  const [busy, setBusy] = React.useState<null | "save" | "test" | "connect" | "remove">(null)
  const [saved, setSaved] = React.useState(!target.isNew)

  const detail = usePoll(open ? () => getConnectorDetail({ data: { id: target.id } }) : null, { intervalMs: 3000, deps: [target.id] })
  const status = target.status

  const set = (k: string, v: string | boolean) => {
    setValues((prev) => ({ ...prev, [k]: v }))
    setDirty(true)
    setErrors((prev) => {
      if (!prev[k]) return prev
      const next = { ...prev }
      delete next[k]
      return next
    })
  }

  const payload = () => {
    const { config, secrets } = wizardPayload(m, values)
    return { id: target.id, type: m.id, label: label.trim() || m.name, config, secrets, trust: m.multiple ? trust : undefined }
  }

  const clearSecretInputs = () =>
    setValues((prev) => {
      const next = { ...prev }
      for (const step of m.setup) if ("field" in step && fieldInfo(m, step).secret) next[step.field] = ""
      return next
    })

  const save = async (quiet = false): Promise<boolean> => {
    const res = await saveConnectorDraft({ data: payload() })
    if (!res.ok) {
      setErrors(res.fields)
      if (!quiet) toast.error(res.message)
      return false
    }
    setErrors({})
    setDirty(false)
    setSaved(true)
    clearSecretInputs()
    void router.invalidate()
    return true
  }

  const onSave = async () => {
    setBusy("save")
    try {
      if (await save()) toast.success(t("common.saved"))
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const onTest = async () => {
    setBusy("test")
    try {
      if (dirty || !saved) {
        if (!(await save())) return
      }
      const p = testConnectorFn({ data: { id: target.id } })
      toast.promise(p, {
        loading: t("connectors.testing", { name: label || m.name }),
        success: (r) => r.detail || t("connectors.works"),
        error: (err) => errorMessage(err),
      })
      await p.catch(() => {})
      void detail.refresh()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const onConnect = async () => {
    setBusy("connect")
    try {
      const p = connectConnectorFn({ data: payload() }).then((r) => {
        if (!r.ok) {
          setErrors(r.fields)
          throw new Error(r.message)
        }
        return r
      })
      toast.promise(p, {
        loading: `Connecting ${label || m.name}`,
        success: (r) => (r.runtimeReachable ? `${label || m.name} is connecting` : `Saved. ${t("runtime.down")}`),
        error: (err) => errorMessage(err),
      })
      await p
      setDirty(false)
      setSaved(true)
      clearSecretInputs()
      void router.invalidate()
      void detail.refresh()
    } catch {
      // the toast already says why
    } finally {
      setBusy(null)
    }
  }

  const onRemove = async () => {
    setBusy("remove")
    try {
      const res = await removeConnectorFn({ data: { id: target.id } })
      toast.success(`${label || m.name} removed`, { description: res.detail })
      onOpenChange(false)
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  // Wizard progress survives closing the sheet (PLAN 11.5): save a draft.
  const close = (next: boolean) => {
    if (!next && dirty && busy === null) {
      void save(true).then((ok) => {
        if (!ok) toast.warning(t("connectors.partialSave"), { description: t("connectors.partialSaveFix") })
      })
    }
    onOpenChange(next)
  }

  const fieldSteps = m.setup.filter((s): s is FieldStep => "field" in s)
  const actionSteps = m.setup.filter((s): s is ActionStep => "action" in s)
  const live = detail.data?.live ?? {}

  return (
    <ResponsiveSheet
      open={open}
      onOpenChange={close}
      title={
        <span className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="min-w-0 break-words">{label || m.name}</span>
          <ConnectorStatusBadge status={status} />
        </span>
      }
      description={m.summary}
      footer={
        <div className="flex w-full flex-wrap gap-2">
          {fieldSteps.length > 0 && (
            <Button variant="outline" disabled={busy !== null || !dirty} onClick={() => void onSave()}>
              {busy === "save" && <Spinner data-icon="inline-start" />}
              {t("connectors.saveDraft")}
            </Button>
          )}
          {!target.isNew || saved ? (
            <Button variant="outline" disabled={busy !== null} onClick={() => void onTest()}>
              {busy === "test" && <Spinner data-icon="inline-start" />}
              {t("connectors.test")}
            </Button>
          ) : null}
          <Button className="ms-auto" disabled={busy !== null} onClick={() => void onConnect()}>
            {busy === "connect" && <Spinner data-icon="inline-start" />}
            {status === "on" || status === "connecting" ? t("connectors.saveReconnect") : t("connectors.connect")}
          </Button>
        </div>
      }
    >
      {detail.data && !detail.data.runtimeUp && <RuntimeAlert />}
      {target.statusDetail && status === "error" && (
        <Alert variant="destructive">
          <CircleAlertIcon aria-hidden="true" />
          <AlertTitle>{t("connectors.errorTitle", { name: label || m.name })}</AlertTitle>
          <AlertDescription className="break-words">
            <span className="block">{target.statusDetail}</span>
            <span className="block">{t("connectors.errorFix")}</span>
          </AlertDescription>
        </Alert>
      )}
      {target.secretsError && (
        <Alert variant="destructive">
          <AlertDescription>{target.secretsError} {t("connectors.secretsFix")}</AlertDescription>
        </Alert>
      )}
      {m.description && <p className="text-sm text-pretty text-muted-foreground">{m.description}</p>}
      {m.pauseNote && <p className="text-caption text-pretty text-muted-foreground">{m.pauseNote}</p>}

      {(m.multiple || fieldSteps.length > 0) && (
      <FieldGroup>
        {m.multiple && (
          <Field data-invalid={!!errors.label}>
            <FieldLabel htmlFor={`${target.id}-label`}>{t("connectors.field.name")}</FieldLabel>
            <Input id={`${target.id}-label`} value={label} maxLength={80} onChange={(e) => (setLabel(e.target.value), setDirty(true))} />
            <FieldDescription>
              Id <code className="font-mono">{target.id}</code>, used in tool names and <code className="font-mono">/connectors</code>.
            </FieldDescription>
          </Field>
        )}
        {fieldSteps.map((step) => (
          <WizardField
            key={step.field}
            id={`${target.id}-${step.field}`}
            step={step}
            info={fieldInfo(m, step)}
            value={values[step.field]}
            stored={target.secrets[step.field]}
            error={errors[step.field]}
            onChange={(v) => set(step.field, v)}
          />
        ))}
      </FieldGroup>
      )}

      {actionSteps.map((step) => (
        <FieldSet key={step.action} className="gap-3">
          <FieldLegend variant="label">{step.label}</FieldLegend>
          {step.help && <FieldDescription>{step.help}</FieldDescription>}
          <WizardAction
            step={step}
            connectorId={target.id}
            saved={saved && !dirty}
            status={status}
            live={live}
            owners={detail.data?.owners}
            config={target.config}
            appUrl={appUrl}
            onSaveFirst={() => save()}
            onConnect={onConnect}
            refresh={() => void detail.refresh()}
          />
        </FieldSet>
      ))}

      {!target.isNew && (
        <>
          <Separator />
          <section aria-labelledby={`${target.id}-events`} className="flex flex-col gap-2">
            <h3 id={`${target.id}-events`} className="text-heading">
              {t("connectors.events")}
            </h3>
            {!detail.data && !detail.error ? (
              <div className="flex flex-col gap-2">
                <Skeleton className="h-4 w-3/4" />
                <Skeleton className="h-4 w-1/2" />
              </div>
            ) : detail.error ? (
              <Alert variant="destructive">
                <AlertDescription>{detail.error}</AlertDescription>
              </Alert>
            ) : detail.data!.events.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("connectors.eventsEmpty")}</p>
            ) : (
              <ol className="flex flex-col gap-1.5">
                {detail.data!.events.map((e) => (
                  <li key={e.id} className="flex min-w-0 items-baseline gap-2 text-sm">
                    <span className="shrink-0 font-mono text-caption text-muted-foreground tabular-nums">{inZone(e.at, detail.data?.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}</span>
                    {e.level !== "info" && <Badge variant={e.level === "error" ? "destructive" : e.level === "warn" ? "warning" : "outline"}>{e.level}</Badge>}
                    <span className="min-w-0 break-words">{e.message}</span>
                  </li>
                ))}
              </ol>
            )}
          </section>

          <Separator />
          <section aria-labelledby={`${target.id}-danger`} className="flex flex-col gap-3">
            <h3 id={`${target.id}-danger`} className="text-heading">
              {t("connectors.danger")}
            </h3>
            <p className="text-sm text-pretty text-muted-foreground">{m.removeWarning ?? t("connectors.removeDefault")}</p>
            <AlertDialog>
              <AlertDialogTrigger render={<Button variant="destructive" className="self-start" disabled={busy !== null} />}>
                {busy === "remove" && <Spinner data-icon="inline-start" />}
                {t("connectors.remove")} {label || m.name}
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t("connectors.removeTitle", { name: label || m.name })}</AlertDialogTitle>
                  <AlertDialogDescription>{m.removeWarning ?? t("connectors.removeDefault")}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
                  <AlertDialogAction variant="danger" onClick={() => void onRemove()}>
                    {t("connectors.remove")}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </section>
        </>
      )}
      {m.multiple && (
        <Field orientation="horizontal">
          <Switch id={`${target.id}-trust`} checked={trust === "external"} onCheckedChange={(v) => (setTrust(v ? "external" : "own"), setDirty(true))} />
          <FieldLabel htmlFor={`${target.id}-trust`} className="font-normal">
            {t("connectors.trustLabel")}
          </FieldLabel>
        </Field>
      )}
    </ResponsiveSheet>
  )
}

function WizardField({
  id,
  step,
  info,
  value,
  stored,
  error,
  onChange,
}: {
  id: string
  step: FieldStep
  info: WizardField
  value: string | boolean | undefined
  stored: string | undefined
  error: string | undefined
  onChange: (v: string | boolean) => void
}) {
  const describedBy = [step.help ? `${id}-help` : null, error ? `${id}-error` : null].filter(Boolean).join(" ") || undefined
  if (info.kind === "boolean") {
    return (
      <Field orientation="horizontal" data-invalid={!!error}>
        <Switch id={id} checked={value === true} onCheckedChange={(v) => onChange(v)} aria-describedby={describedBy} />
        <div className="flex flex-col gap-1">
          <FieldLabel htmlFor={id}>{step.label}</FieldLabel>
          {step.help && <FieldDescription id={`${id}-help`}>{step.help}</FieldDescription>}
          {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
        </div>
      </Field>
    )
  }
  if (info.kind === "enum" && info.options) {
    return (
      <Field data-invalid={!!error}>
        <FieldLabel htmlFor={id}>{step.label}</FieldLabel>
        <SimpleSelect
          id={id}
          className="w-full"
          value={typeof value === "string" ? value : ""}
          options={info.options.map((o) => ({ value: o, label: o }))}
          onChange={(v) => onChange(v)}
        />
        {step.help && <FieldDescription id={`${id}-help`}>{step.help}</FieldDescription>}
        {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
      </Field>
    )
  }
  return (
    <Field data-invalid={!!error}>
      <FieldLabel htmlFor={id}>
        {step.label}
        {info.optional && <span className="font-normal text-muted-foreground">(optional)</span>}
      </FieldLabel>
      <Input
        id={id}
        type={info.secret ? "password" : info.kind === "number" ? "number" : "text"}
        inputMode={info.kind === "number" ? "numeric" : undefined}
        autoComplete={info.secret ? "new-password" : "off"}
        spellCheck={false}
        autoCapitalize="none"
        placeholder={info.secret && stored ? stored : step.placeholder}
        value={typeof value === "string" ? value : ""}
        aria-invalid={!!error}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
        className={info.secret || info.kind !== "string" ? "font-mono" : undefined}
      />
      {(step.help || (info.secret && stored)) && (
        <FieldDescription id={`${id}-help`}>
          {step.help}
          {info.secret && stored ? `${step.help ? " " : ""}Saved as ${stored}. Leave empty to keep it.` : ""}
        </FieldDescription>
      )}
      {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
    </Field>
  )
}

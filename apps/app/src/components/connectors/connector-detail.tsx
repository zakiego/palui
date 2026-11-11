// The connector page (PLAN 11.4 Connectors): the setup wizard generated from the
// manifest's `setup` steps and zod schemas, Test, Connect, recent events, and
// the danger area. Leaving the page with unsaved edits keeps them as a draft.
import * as React from "react"
import { Link, useNavigate, useRouter } from "@tanstack/react-router"
import { ArrowLeftIcon } from "lucide-react"
import { toast } from "sonner"
import type { ConnectorManifest, SetupStep } from "@palui/core/connectors/define"

import { ConnectorStatusBadge } from "@/components/connector-status"
import { SimpleSelect } from "@/components/simple-select"
import { CircleAlertIcon } from "lucide-react"
import { RuntimeAlert } from "@/components/runtime-alert"
import { WizardAction } from "@/components/connectors/wizard-actions"
import { CopyButton } from "@/components/copy-button"
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
import { StatusText } from "@/components/ui/status-text"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldSet, FieldLegend } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import type { ConnectorTarget } from "@/components/connectors/targets"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { usePoll } from "@/hooks/use-poll"
import { connectConnectorFn, getConnectorDetail, removeConnectorFn, saveConnectorDraft, testConnectorFn } from "@/lib/fns/connectors"
import { errorMessage, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { type FieldStep, type WizardField, wizardFieldInfo as fieldInfo, wizardInitialValues, wizardPayload } from "@/lib/zod-fields"

type ActionStep = Extract<SetupStep, { action: string }>

export function ConnectorDetail({ target, manifest: m, appUrl }: { target: ConnectorTarget; manifest: ConnectorManifest; appUrl: string }) {
  const t = useT()
  const router = useRouter()
  const navigate = useNavigate()
  const [values, setValues] = React.useState(() => wizardInitialValues(m, target.config))
  const [label, setLabel] = React.useState(target.label)
  const [trust, setTrust] = React.useState(target.trust)
  const [dirty, setDirty] = React.useState(false)
  const [errors, setErrors] = React.useState<Record<string, string>>({})
  const [busy, setBusy] = React.useState<null | "save" | "test" | "connect" | "remove">(null)
  const [saved, setSaved] = React.useState(!target.isNew)

  const detail = usePoll(() => getConnectorDetail({ data: { id: target.id } }), { intervalMs: 3000, deps: [target.id] })
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
      removed.current = true
      void navigate({ to: "/connectors" })
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  // Wizard progress survives leaving the page (PLAN 11.5): save a draft.
  const removed = React.useRef(false)
  const latest = React.useRef({ dirty, busy, save, t })
  latest.current = { dirty, busy, save, t }
  React.useEffect(
    () => () => {
      const { dirty, busy, save, t } = latest.current
      if (!dirty || busy !== null || removed.current) return
      void save(true).then((ok) => {
        if (!ok) toast.warning(t("connectors.partialSave"), { description: t("connectors.partialSaveFix") })
      })
    },
    [],
  )

  const fieldSteps = m.setup.filter((s): s is FieldStep => "field" in s)
  const actionSteps = m.setup.filter((s): s is ActionStep => "action" in s)
  const live = detail.data?.live ?? {}

  return (
    <>
      <div className="flex flex-col gap-3">
        <Button variant="ghost" size="sm" className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/connectors" />}>
          <ArrowLeftIcon data-icon="inline-start" />
          {t("nav.connectors")}
        </Button>
        <div className="flex min-w-0 flex-col gap-1.5">
          <h1 className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 font-heading text-title">
            <span className="min-w-0 break-words">{label || m.name}</span>
            <ConnectorStatusBadge status={status} />
          </h1>
          <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">{m.summary}</p>
        </div>
      </div>
      <div className="flex w-full max-w-2xl flex-col gap-6">
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

      {m.multiple && (
        <Field orientation="horizontal">
          <Switch id={`${target.id}-trust`} checked={trust === "external"} onCheckedChange={(v) => (setTrust(v ? "external" : "own"), setDirty(true))} />
          <FieldLabel htmlFor={`${target.id}-trust`} className="font-normal">
            {t("connectors.trustLabel")}
          </FieldLabel>
        </Field>
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
            <h2 id={`${target.id}-events`} className="text-heading">
              {t("connectors.events")}
            </h2>
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
                    {e.level !== "info" && <StatusText tone={e.level === "error" ? "destructive" : e.level === "warn" ? "warning" : "outline"}>{e.level}</StatusText>}
                    <span className="min-w-0 break-words">{e.message}</span>
                  </li>
                ))}
              </ol>
            )}
          </section>

          <Separator />
          <section aria-labelledby={`${target.id}-danger`} className="flex flex-col gap-3">
            <h2 id={`${target.id}-danger`} className="text-heading">
              {t("connectors.danger")}
            </h2>
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
      </div>
      <div className="sticky bottom-0 z-10 -mx-4 -mb-10 border-t bg-background/95 px-4 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur md:-mx-6 md:px-6 lg:-mx-8 lg:px-8">
        <div className="flex w-full max-w-2xl flex-wrap gap-2">
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
      </div>
    </>
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
      {step.command && <CommandBlock command={step.command} />}
      {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
    </Field>
  )
}

// A command the field's help asks you to run, with a copy button so nobody has
// to select it out of a sentence.
function CommandBlock({ command }: { command: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-muted py-1.5 ps-3 pe-1.5">
      <code className="min-w-0 flex-1 font-mono text-sm break-all">{command}</code>
      <CopyButton text={command} label="Copy" variant="ghost" className="shrink-0" />
    </div>
  )
}

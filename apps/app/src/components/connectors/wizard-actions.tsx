// The special wizard steps a manifest can ask for (define.ts SetupStep actions).
// Each one talks to the runtime through POST /control/action/:id or reads the
// connector's live state from /control/status.
import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { CircleCheckIcon, ExternalLinkIcon, LinkIcon, RefreshCwIcon, SearchIcon } from "lucide-react"
import { toast } from "sonner"
import type { SetupStep } from "@palui/core/connectors/define"

import { CopyButton } from "@/components/copy-button"
import { QrCode } from "@/components/qr-code"
import { SimpleSelect } from "@/components/simple-select"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader } from "@/components/ui/empty"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { connectorActionFn, saveConnectorDraft, setWhatsAppOwnerFn } from "@/lib/fns/connectors"
import { errorMessage } from "@/lib/format"
import type { Json, JsonObject } from "@/lib/json"

type ActionStep = Extract<SetupStep, { action: string }>

export type WizardActionProps = {
  step: ActionStep
  connectorId: string
  saved: boolean
  status: string
  live: JsonObject
  owners: { telegram: boolean; whatsapp: string | null } | undefined
  config: JsonObject
  appUrl: string
  onSaveFirst: () => Promise<boolean>
  onConnect: () => Promise<void>
  refresh: () => void
}

export function WizardAction(props: WizardActionProps) {
  switch (props.step.action) {
    case "claimOwner":
      return <ClaimOwner {...props} />
    case "ownerPhone":
      return <OwnerPhone {...props} />
    case "pairWhatsApp":
      return <PairWhatsApp {...props} />
    case "pickContainer":
      return <PickContainer {...props} />
    case "listTools":
      return <ListTools {...props} />
    case "oauth":
      return <OAuth {...props} />
    default:
      return null
  }
}

const str = (v: Json | undefined): string | undefined => (typeof v === "string" && v ? v : undefined)

// Runs an action, saving the form first when it has unsaved changes.
function useAction(props: WizardActionProps) {
  const [busy, setBusy] = React.useState(false)
  const run = async (name: string, input: Record<string, unknown> = {}): Promise<JsonObject | null> => {
    setBusy(true)
    try {
      if (!props.saved && !(await props.onSaveFirst())) return null
      return await connectorActionFn({ data: { id: props.connectorId, name, input } })
    } catch (err) {
      toast.error(errorMessage(err))
      return null
    } finally {
      setBusy(false)
    }
  }
  return { busy, run }
}

// Telegram: a single-use t.me link with a QR, and a live status line until the
// owner identity exists (PLAN 5.1).
function ClaimOwner(props: WizardActionProps) {
  const { busy, run } = useAction(props)
  const [link, setLink] = React.useState<{ url: string; expiresAt: number } | null>(null)
  const claimed = !!props.owners?.telegram
  const notRunning = props.status !== "on"

  if (claimed && !link) {
    return (
      <p className="flex items-center gap-2 text-sm">
        <CircleCheckIcon className="text-success" aria-hidden="true" />
        Owner claimed.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-3">
      {notRunning && !link && <p className="text-caption text-muted-foreground">Connect the bot first, then make the link.</p>}
      {!link ? (
        <Button
          variant="outline"
          className="self-start"
          disabled={busy || notRunning}
          onClick={async () => {
            const res = await run("claimLink")
            const url = str(res?.url)
            if (url) setLink({ url, expiresAt: Number(res?.expiresAt ?? 0) })
          }}
        >
          {busy ? <Spinner data-icon="inline-start" /> : <LinkIcon data-icon="inline-start" />}
          {claimed ? "Make a new owner link" : "Make the claim link"}
        </Button>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <a href={link.url} target="_blank" rel="noreferrer" className="min-w-0 font-mono text-sm break-all text-primary-fg underline underline-offset-4">
              {link.url}
            </a>
            <CopyButton text={link.url} />
          </div>
          <QrCode value={link.url} label="QR code for the Telegram claim link" />
          <p className="text-caption text-muted-foreground">Single use. Expires {link.expiresAt ? `at ${new Date(link.expiresAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "in 10 minutes"}.</p>
          <p role="status" aria-live="polite" className="flex items-center gap-2 text-sm">
            {claimed ? (
              <>
                <CircleCheckIcon className="text-success" aria-hidden="true" />
                Claimed. You're the owner.
              </>
            ) : (
              <>
                <Spinner aria-hidden="true" />
                Waiting for /start from your Telegram account
              </>
            )}
          </p>
        </div>
      )}
    </div>
  )
}

// WhatsApp step 1: the owner's number. Messages from it are the only ones answered.
function OwnerPhone(props: WizardActionProps) {
  const router = useRouter()
  const [phone, setPhone] = React.useState("")
  const [error, setError] = React.useState<string>()
  const [busy, setBusy] = React.useState(false)
  const current = props.owners?.whatsapp
  const id = `${props.connectorId}-owner-phone`
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        setError(undefined)
        try {
          await setWhatsAppOwnerFn({ data: { phone } })
          setPhone("")
          toast.success("Owner number saved")
          props.refresh()
          void router.invalidate()
        } catch (err) {
          setError(errorMessage(err).replace(/^.*?"message":"([^"]+)".*$/s, "$1"))
        } finally {
          setBusy(false)
        }
      }}
    >
      <Field data-invalid={!!error}>
        <FieldLabel htmlFor={id} className="sr-only">
          Phone number
        </FieldLabel>
        <div className="flex gap-2">
          <Input
            id={id}
            type="tel"
            inputMode="tel"
            autoComplete="tel"
            className="font-mono"
            placeholder={current ? `+${current}` : "0812 3456 7890"}
            value={phone}
            aria-invalid={!!error}
            onChange={(e) => setPhone(e.target.value)}
          />
          <Button type="submit" variant="outline" disabled={busy || !phone.trim()}>
            {busy && <Spinner data-icon="inline-start" />}
            Save
          </Button>
        </div>
        {current && !error && <p className="text-caption text-muted-foreground">Current owner: <span className="font-mono">+{current}</span></p>}
        {error && <FieldError>{error}</FieldError>}
      </Field>
    </form>
  )
}

// WhatsApp step 2: link by phone-number code, QR as the fallback tab. The code
// and QR come from the runtime's live state, which rotates them (PLAN 5.1).
function PairWhatsApp(props: WizardActionProps) {
  const code = str(props.live.pairingCode)
  const qr = str(props.live.qr)
  const expires = str(props.live.expiresHint)
  const linked = props.status === "on"
  const waiting = props.status === "connecting"

  if (linked) {
    return (
      <p className="flex items-center gap-2 text-sm">
        <CircleCheckIcon className="text-success" aria-hidden="true" />
        Linked.
      </p>
    )
  }
  if (!props.owners?.whatsapp) return <p className="text-caption text-muted-foreground">Save your phone number first.</p>
  if (!waiting) {
    return (
      <Button variant="outline" className="self-start" onClick={() => void props.onConnect()}>
        Start pairing
      </Button>
    )
  }
  if (!code && !qr) {
    return (
      <div className="flex flex-col gap-3">
        <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner aria-hidden="true" />
          Asking WhatsApp for a pairing code
        </p>
        <PairPhone {...props} />
      </div>
    )
  }
  return (
    <Tabs defaultValue={code ? "code" : "qr"} className="gap-3">
      <TabsList>
        <TabsTrigger value="code" disabled={!code}>
          Code
        </TabsTrigger>
        <TabsTrigger value="qr" disabled={!qr}>
          QR
        </TabsTrigger>
      </TabsList>
      <TabsContent value="code" className="flex flex-col gap-3">
        {code && (
          <>
            <p className="font-mono text-display tracking-[0.12em] tabular-nums select-all" aria-label={`Pairing code ${code.split("").join(" ")}`}>
              {code}
            </p>
            <CopyButton text={code.replace("-", "")} label="Copy code" className="self-start" />
            <p className="text-caption text-muted-foreground">WhatsApp, Settings, Linked devices, Link a device, Link with phone number instead.</p>
          </>
        )}
      </TabsContent>
      <TabsContent value="qr" className="flex flex-col gap-3">
        {qr && <QrCode value={qr} label="WhatsApp pairing QR code" className="w-56" />}
        <p className="text-caption text-muted-foreground">WhatsApp, Settings, Linked devices, Link a device, then scan.</p>
      </TabsContent>
      {/* Not a live region: the expiry text changes too often to announce. */}
      {expires && <p className="text-caption text-muted-foreground">{expires}</p>}
      {!code && <PairPhone {...props} />}
    </Tabs>
  )
}

// The pairing code is for the number being LINKED (Palui's own number, PLAN 5.1),
// not the owner's. Without it, pairing is QR only.
function PairPhone(props: WizardActionProps) {
  const { run } = useAction(props)
  const [phone, setPhone] = React.useState(str(props.config.pairPhone) ?? "")
  const [error, setError] = React.useState<string>()
  const [busy, setBusy] = React.useState(false)
  const id = `${props.connectorId}-pair-phone`
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        setError(undefined)
        try {
          const res = await run("pair", { phone })
          if (res && res.ok === false) setError(str(res.error) ?? "Pairing didn't start.")
          else props.refresh()
        } finally {
          setBusy(false)
        }
      }}
    >
      <Field data-invalid={!!error}>
        <FieldLabel htmlFor={id}>Number to link</FieldLabel>
        <div className="flex gap-2">
          <Input
            id={id}
            type="tel"
            inputMode="tel"
            className="font-mono"
            placeholder="0812 3456 7890"
            value={phone}
            aria-invalid={!!error}
            onChange={(e) => setPhone(e.target.value)}
          />
          <Button type="submit" variant="outline" disabled={busy || !phone.trim()}>
            {busy && <Spinner data-icon="inline-start" />}
            Get code
          </Button>
        </div>
        <p className="text-caption text-muted-foreground">Leave empty to pair with the QR.</p>
        {error && <FieldError>{error}</FieldError>}
      </Field>
    </form>
  )
}

// recall: pick the container from GET /v1/containers through the runtime.
function PickContainer(props: WizardActionProps) {
  const { busy, run } = useAction(props)
  const router = useRouter()
  const [options, setOptions] = React.useState<string[] | null>(null)
  const current = str(props.config.container) ?? "default"
  const load = async () => {
    const res = await run("listContainers")
    if (!res) return
    const list = Array.isArray(res.containers) ? res.containers : []
    setOptions(
      list
        .map((c) => (typeof c === "string" ? c : c && typeof c === "object" && !Array.isArray(c) ? (str(c.name) ?? str(c.id) ?? "") : ""))
        .filter(Boolean),
    )
  }
  const choose = async (container: string) => {
    const res = await saveConnectorDraft({ data: { id: props.connectorId, type: "recall", config: { container } } })
    if (!res.ok) toast.error(res.message)
    else {
      toast.success(`Container set to ${container}`)
      void router.invalidate()
    }
  }
  return (
    <div className="flex flex-col gap-2">
      {options ? (
        options.length ? (
          <SimpleSelect aria-label="recall container" className="w-full" value={current} options={[...new Set([current, ...options])].map((o) => ({ value: o, label: o }))} onChange={(v) => void choose(v)} />
        ) : (
          <p className="text-sm text-muted-foreground">No containers yet. Palui uses "default".</p>
        )
      ) : (
        <p className="text-sm">
          Using <span className="font-mono">{current}</span>
        </p>
      )}
      <Button variant="outline" size="sm" className="self-start" disabled={busy} onClick={() => void load()}>
        {busy ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
        {options ? "Reload containers" : "Load containers"}
      </Button>
    </div>
  )
}

// `saved`: the owner has saved a pref for this tool. Until then the runtime
// offers it as destructive, whatever the table shows (fail closed).
type ToolRow = { name: string; title: string; description: string; suggestedClass: ToolClass; enabled: boolean; class: ToolClass; saved: boolean }
type ToolClass = "read" | "write" | "destructive"
const CLASSES: ToolClass[] = ["read", "write", "destructive"]
const asClass = (v: Json | undefined): ToolClass => (v === "read" || v === "write" ? v : "destructive")

// MCP: every tool with a Switch and a class (PLAN 5.4). Unknown is destructive.
function ListTools(props: WizardActionProps) {
  const { busy, run } = useAction(props)
  const router = useRouter()
  const [tools, setTools] = React.useState<ToolRow[] | null>(null)
  const [query, setQuery] = React.useState("")
  const [dirty, setDirty] = React.useState(false)
  const [saving, setSaving] = React.useState(false)

  const load = async () => {
    const res = await run("listTools")
    if (!res) return
    const list = Array.isArray(res.tools) ? res.tools : []
    setTools(
      list
        .filter((t): t is JsonObject => !!t && typeof t === "object" && !Array.isArray(t))
        .map((t) => ({
          name: str(t.name) ?? "",
          title: str(t.title) ?? "",
          description: str(t.description) ?? "",
          suggestedClass: asClass(t.suggestedClass),
          enabled: t.enabled === true,
          class: asClass(t.class ?? t.suggestedClass),
          saved: t.saved === true,
        }))
        .filter((t) => t.name),
    )
    setDirty(false)
  }

  const update = (name: string, patch: Partial<ToolRow>) => {
    setTools((prev) => prev?.map((t) => (t.name === name ? { ...t, ...patch } : t)) ?? null)
    setDirty(true)
  }

  const save = async () => {
    if (!tools) return
    setSaving(true)
    try {
      const toolPrefs = Object.fromEntries(tools.map((t) => [t.name, { enabled: t.enabled, class: t.class }]))
      const res = await saveConnectorDraft({ data: { id: props.connectorId, type: "mcp", config: { toolPrefs } } })
      if (!res.ok) toast.error(res.message)
      else {
        toast.success("Tool settings saved")
        setTools((prev) => prev?.map((t) => ({ ...t, saved: true })) ?? null)
        setDirty(false)
        void router.invalidate()
      }
    } finally {
      setSaving(false)
    }
  }

  if (!tools) {
    return (
      <Button variant="outline" size="sm" className="self-start" disabled={busy} onClick={() => void load()}>
        {busy ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
        Load tools
      </Button>
    )
  }

  const q = query.trim().toLowerCase()
  const shown = q ? tools.filter((t) => t.name.toLowerCase().includes(q) || t.description.toLowerCase().includes(q)) : tools
  const enabledCount = tools.filter((t) => t.enabled).length
  const unsaved = tools.filter((t) => !t.saved).length

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <InputGroup className="min-w-0 flex-1 basis-48">
          <InputGroupAddon>
            <SearchIcon aria-hidden="true" />
          </InputGroupAddon>
          <InputGroupInput aria-label="Search tools" placeholder="Search tools" value={query} onChange={(e) => setQuery(e.target.value)} />
        </InputGroup>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setTools((prev) => prev?.map((t) => (t.class === "read" ? { ...t, enabled: true } : t)) ?? null)
            setDirty(true)
          }}
        >
          Enable all read tools
        </Button>
      </div>
      <p className="text-caption text-muted-foreground tabular-nums">
        {enabledCount} of {tools.length} enabled
      </p>
      {shown.length === 0 ? (
        <Empty className="border border-dashed p-4">
          <EmptyHeader>
            <EmptyDescription>{tools.length ? "No tool matches that search." : "This server lists no tools."}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Tool</TableHead>
              <TableHead className="w-32">Class</TableHead>
              <TableHead className="w-14 text-end">On</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((t) => (
              <TableRow key={t.name}>
                <TableCell className="max-w-0 whitespace-normal">
                  {t.title && <span className="block text-sm">{t.title}</span>}
                  <span className="block font-mono text-caption break-all">{t.name}</span>
                  {t.description && <span className="line-clamp-2 text-caption text-muted-foreground">{t.description}</span>}
                  {t.class !== t.suggestedClass && <Badge variant="outline">suggested {t.suggestedClass}</Badge>}
                </TableCell>
                <TableCell>
                  <SimpleSelect aria-label={`Class for ${t.name}`} size="sm" className="w-full" value={t.class} options={CLASSES.map((c) => ({ value: c, label: c }))} onChange={(v) => update(t.name, { class: v as ToolClass })} />
                </TableCell>
                <TableCell className="text-end">
                  <Switch aria-label={`${t.enabled ? "Disable" : "Enable"} ${t.name}`} checked={t.enabled} onCheckedChange={(v) => update(t.name, { enabled: v })} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {unsaved > 0 && (
        <p className="text-caption text-muted-foreground">
          {unsaved === 1 ? "1 tool isn't saved yet" : `${unsaved} tools aren't saved yet`}. Until you save, Palui treats{" "}
          {unsaved === 1 ? "it" : "them"} as destructive and asks before every call.
        </p>
      )}
      <div className="flex gap-2">
        <Button size="sm" disabled={(!dirty && unsaved === 0) || saving} onClick={() => void save()}>
          {saving && <Spinner data-icon="inline-start" />}
          Save tool settings
        </Button>
        <Button variant="ghost" size="sm" disabled={busy} onClick={() => void load()}>
          Reload
        </Button>
      </div>
    </div>
  )
}

// Google Calendar: open the consent screen the runtime builds.
function OAuth(props: WizardActionProps) {
  const { busy, run } = useAction(props)
  const connected = props.status === "on"
  return (
    <div className="flex flex-col gap-2">
      {connected && (
        <p className="flex items-center gap-2 text-sm">
          <CircleCheckIcon className="text-success" aria-hidden="true" />
          Connected to Google.
        </p>
      )}
      <Button
        variant="outline"
        className="self-start"
        disabled={busy}
        onClick={async () => {
          const res = await run("authUrl", { redirectUri: `${props.appUrl}/api/oauth/google/callback` })
          const url = str(res?.url)
          if (url) window.location.assign(url)
          else if (res) toast.error("The runtime didn't return a Google URL.")
        }}
      >
        {busy ? <Spinner data-icon="inline-start" /> : <ExternalLinkIcon data-icon="inline-start" />}
        {connected ? "Reconnect Google" : "Connect Google"}
      </Button>
    </div>
  )
}

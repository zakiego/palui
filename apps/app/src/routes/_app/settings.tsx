// Settings (PLAN 11.4): persona, timezone, language, model roles and failover,
// budgets, passkeys, MCP access, backup status, and inbound hooks.
import * as React from "react"
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import { BoxIcon, DatabaseBackupIcon, FingerprintIcon, GaugeIcon, PlusIcon, Settings2Icon, ShieldIcon, TrashIcon, WebhookIcon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { McpAccessCard } from "@/components/mcp-access-card"
import { connectorOptions, ModelChain, ROLE_HELP, ROLES, stripKeys, useCatalogs, useLinkKeys, type KeyedLink, type Role } from "@/components/model-chain"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, LineTabsSkeleton, SkeletonButton, SkeletonText } from "@/components/page-pending"
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
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { StatusText } from "@/components/ui/status-text"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { authClient, passkeyCancelled } from "@/lib/auth-client"
import { removePasskey } from "@/lib/api/session"
import { getMcpAccess } from "@/lib/api/mcp"
import { createHookFn, getSettingsPage, saveSettings } from "@/lib/api/settings"
import { ago, errorMessage, fullDate } from "@/lib/format"
import { useLabel, useT } from "@/lib/i18n"

const TABS = ["general", "models", "limits", "security", "hooks"] as const

// The settings page plus the MCP access list, fetched side by side.
async function loadSettings() {
  const [page, mcp] = await Promise.all([getSettingsPage(), getMcpAccess()])
  return { ...page, mcp }
}

export const Route = createFileRoute("/_app/settings")({
  validateSearch: z.object({ tab: z.enum(TABS).optional() }),
  loader: loadSettings,
  head: () => ({ meta: [{ title: "Settings · Palui" }] }),
  component: SettingsPage,
  pendingComponent: SettingsSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof loadSettings>>
type SaveInput = Parameters<typeof saveSettings>[0]["data"]

function useSave() {
  const router = useRouter()
  const t = useT()
  const [busy, setBusy] = React.useState(false)
  const save = async (patch: SaveInput) => {
    setBusy(true)
    try {
      await saveSettings({ data: patch })
      toast.success(t("common.saved"))
      await router.invalidate()
      return true
    } catch (err) {
      toast.error(errorMessage(err))
      return false
    } finally {
      setBusy(false)
    }
  }
  return { busy, save }
}

function SettingsPage() {
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  const tab = search.tab ?? "general"
  return (
    <>
      <PageHeader title={t("settings.title")} />
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: { tab: v as (typeof TABS)[number] }, replace: true })} className="gap-6">
        <TabsList variant="line" aria-label={t("settings.title")}>
          <TabsTrigger value="general">
            <Settings2Icon aria-hidden="true" className="hidden @xl/main:block" />
            {t("settings.tab.general")}
          </TabsTrigger>
          <TabsTrigger value="models">
            <BoxIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("settings.tab.models")}
          </TabsTrigger>
          <TabsTrigger value="limits">
            <GaugeIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("settings.tab.limits")}
          </TabsTrigger>
          <TabsTrigger value="security">
            <ShieldIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("settings.tab.security")}
          </TabsTrigger>
          <TabsTrigger value="hooks">
            <WebhookIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("settings.tab.hooks")}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="general" keepMounted>
          <GeneralTab data={data} />
        </TabsContent>
        <TabsContent value="models" keepMounted>
          <ModelsTab data={data} />
        </TabsContent>
        <TabsContent value="limits" keepMounted>
          <LimitsTab data={data} />
        </TabsContent>
        <TabsContent value="security" keepMounted className="flex flex-col gap-4">
          <PasskeysCard passkeys={data.passkeys} />
          <McpAccessCard access={data.mcp} />
          <BackupCard backup={data.backup} />
        </TabsContent>
        <TabsContent value="hooks" keepMounted>
          <HooksCard data={data} />
        </TabsContent>
      </Tabs>
    </>
  )
}

function GeneralTab({ data }: { data: Data }) {
  const s = data.settings
  const [timezone, setTimezone] = React.useState(s.timezone)
  const [language, setLanguage] = React.useState(s.language)
  const [uiLanguage, setUiLanguage] = React.useState(s.uiLanguage)
  const [fallback, setFallback] = React.useState(s.fallbackChannel)
  const [timezoneError, setTimezoneError] = React.useState<string>()
  const [zones] = React.useState<string[]>(() => {
    try {
      return Intl.supportedValuesOf("timeZone")
    } catch {
      // Older engines without supportedValuesOf: the field still takes free text.
      return []
    }
  })
  const timezoneRef = React.useRef<HTMLInputElement>(null)
  const t = useT()
  const { busy, save } = useSave()
  const submit = () => {
    const tz = timezone.trim()
    // The same check the server runs, so a typo shows up at the field instead of in a toast.
    try {
      if (!tz) throw new RangeError("empty time zone")
      new Intl.DateTimeFormat("en", { timeZone: tz })
    } catch {
      setTimezoneError(t("settings.tz.invalid"))
      timezoneRef.current?.focus()
      return
    }
    void save({ timezone: tz, language, uiLanguage, fallbackChannel: fallback })
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.general.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        <FieldGroup>
          {/* Persona moved to each agent (PLAN 19.7). "palui" is the default agent's fixed slug. */}
          <p className="text-sm text-muted-foreground">
            {t("settings.personaMoved")}{" "}
            <Link to="/agents/$slug" params={{ slug: "palui" }} className="underline underline-offset-4">
              {t("settings.personaMovedLink")}
            </Link>
          </p>
          <div className="grid grid-cols-1 gap-4 @2xl/main:grid-cols-2">
            <Field data-invalid={timezoneError ? true : undefined}>
              <FieldLabel htmlFor="timezone">{t("jobs.field.tz")}</FieldLabel>
              <Input
                ref={timezoneRef}
                id="timezone"
                className="font-mono"
                spellCheck={false}
                autoCapitalize="none"
                autoCorrect="off"
                autoComplete="off"
                list="timezone-options"
                aria-invalid={timezoneError ? true : undefined}
                aria-describedby={timezoneError ? "timezone-error" : undefined}
                value={timezone}
                onChange={(e) => {
                  setTimezone(e.target.value)
                  setTimezoneError(undefined)
                }}
              />
              <datalist id="timezone-options">
                {zones.map((z) => (
                  <option key={z} value={z} />
                ))}
              </datalist>
              {timezoneError && <FieldError id="timezone-error" role={undefined}>{timezoneError}</FieldError>}
            </Field>
            <Field>
              <FieldLabel htmlFor="language">{t("settings.language")}</FieldLabel>
              <SimpleSelect
                id="language"
                className="w-full"
                value={language}
                options={[
                  { value: "auto", label: t("settings.language.auto") },
                  { value: "id", label: t("settings.language.id") },
                  { value: "en", label: t("settings.language.en") },
                ]}
                onChange={(v) => setLanguage(v as typeof language)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="ui-language">{t("settings.uiLanguage")}</FieldLabel>
              <SimpleSelect
                id="ui-language"
                className="w-full"
                value={uiLanguage}
                options={[
                  { value: "en", label: "English" },
                  { value: "id", label: "Bahasa Indonesia" },
                ]}
                onChange={(v) => setUiLanguage(v as typeof uiLanguage)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="fallback">{t("settings.fallback")}</FieldLabel>
              <SimpleSelect
                id="fallback"
                className="w-full"
                value={fallback}
                options={[
                  { value: "telegram", label: "Telegram" },
                  { value: "whatsapp", label: "WhatsApp" },
                  { value: "none", label: t("settings.fallback.none") },
                ]}
                onChange={(v) => setFallback(v as typeof fallback)}
              />
              <FieldDescription>{t("settings.fallback.help")}</FieldDescription>
            </Field>
          </div>
        </FieldGroup>
      </CardContent>
      <CardFooter className="justify-end border-t">
        <Button disabled={busy} onClick={submit}>
          {busy && <Spinner data-icon="inline-start" />}
          {t("common.save")}
        </Button>
      </CardFooter>
    </Card>
  )
}

function ModelsTab({ data }: { data: Data }) {
  const withKeys = useLinkKeys()
  const [roles, setRoles] = React.useState(() => {
    const r = data.settings.modelRoles
    return { default: withKeys(r.default), fast: withKeys(r.fast), vision: withKeys(r.vision) }
  })
  const t = useT()
  const { busy, save } = useSave()
  const options = connectorOptions(data.models)
  const catalogs = useCatalogs(ROLES.map((r) => roles[r]), new Set(options.map((o) => o.value)))
  const setChain = (role: Role, chain: KeyedLink[]) => setRoles((r) => ({ ...r, [role]: chain }))

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.models.title")}</CardTitle>
        <CardDescription>{t("settings.models.body")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {options.length === 0 && (
          <Alert>
            <AlertTitle>{t("settings.models.none")}</AlertTitle>
            <AlertDescription>{t("settings.models.noneBody")}</AlertDescription>
          </Alert>
        )}
        {ROLES.map((role) => (
          <FieldSet key={role} className="gap-2">
            <FieldLegend variant="label" className="font-mono">
              {role}
            </FieldLegend>
            <FieldDescription>{t(ROLE_HELP[role])}</FieldDescription>
            <ModelChain role={role} chain={roles[role]} onChange={(c) => setChain(role, c)} options={options} catalogs={catalogs} newKey={withKeys} />
          </FieldSet>
        ))}
      </CardContent>
      <CardFooter className="justify-end border-t">
        <Button
          disabled={busy}
          onClick={() => {
            const bad = ROLES.find((r) => roles[r].some((x) => !x.model.trim()))
            if (bad) return toast.error(t("settings.models.fillIn", { role: bad }))
            void save({ modelRoles: { default: stripKeys(roles.default), fast: stripKeys(roles.fast), vision: stripKeys(roles.vision) } })
          }}
        >
          {busy && <Spinner data-icon="inline-start" />}
          {t("common.save")}
        </Button>
      </CardFooter>
    </Card>
  )
}

function LimitsTab({ data }: { data: Data }) {
  const b = data.settings.budgets
  const [form, setForm] = React.useState({
    maxSteps: String(b.maxSteps),
    turnTimeoutS: String(Math.round(b.turnTimeoutMs / 1000)),
    maxTools: String(b.maxTools),
    dailyTokenCap: String(b.dailyTokenCap),
    maxRunsPerHour: String(b.maxRunsPerHour),
  })
  const t = useT()
  const { busy, save } = useSave()
  const field = (key: keyof typeof form, label: string, help: string, min: number, max: number) => (
    <Field>
      <FieldLabel htmlFor={`budget-${key}`}>{label}</FieldLabel>
      <Input id={`budget-${key}`} type="number" inputMode="numeric" min={min} max={max} className="font-mono" value={form[key]} onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }))} />
      <FieldDescription>{help}</FieldDescription>
    </Field>
  )
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.limits.title")}</CardTitle>
        <CardDescription>{t("settings.limits.body")}</CardDescription>
      </CardHeader>
      <CardContent>
        <FieldGroup className="grid grid-cols-1 gap-4 @2xl/main:grid-cols-2">
          {field("maxSteps", t("settings.limits.maxSteps"), t("settings.limits.maxStepsHelp"), 1, 50)}
          {field("turnTimeoutS", t("settings.limits.turnTimeoutS"), t("settings.limits.turnTimeoutSHelp"), 10, 600)}
          {field("maxTools", t("settings.limits.maxTools"), t("settings.limits.maxToolsHelp"), 1, 128)}
          {field("dailyTokenCap", t("settings.limits.dailyTokenCap"), t("settings.limits.dailyTokenCapHelp"), 0, 100_000_000)}
          {field("maxRunsPerHour", t("settings.limits.maxRunsPerHour"), t("settings.limits.maxRunsPerHourHelp"), 1, 1000)}
        </FieldGroup>
      </CardContent>
      <CardFooter className="justify-end border-t">
        <Button
          disabled={busy}
          onClick={() =>
            void save({
              budgets: {
                maxSteps: Number(form.maxSteps),
                turnTimeoutMs: Number(form.turnTimeoutS) * 1000,
                maxTools: Number(form.maxTools),
                dailyTokenCap: Number(form.dailyTokenCap),
                maxRunsPerHour: Number(form.maxRunsPerHour),
              },
            })
          }
        >
          {busy && <Spinner data-icon="inline-start" />}
          {t("common.save")}
        </Button>
      </CardFooter>
    </Card>
  )
}

function PasskeysCard({ passkeys }: { passkeys: Data["passkeys"] }) {
  const router = useRouter()
  const t = useT()
  const [adding, setAdding] = React.useState(false)
  const add = async () => {
    setAdding(true)
    try {
      const res = await authClient.passkey.addPasskey({ name: `Added ${new Date().toISOString().slice(0, 10)}` })
      if (res?.error) {
        if (!passkeyCancelled(res.error)) toast.error(res.error.message ?? t("settings.passkeys.failed"))
        return
      }
      toast.success(t("invite.passkeyAdded"))
      void router.invalidate()
    } catch (err) {
      if (!passkeyCancelled({ message: errorMessage(err) })) toast.error(errorMessage(err))
    } finally {
      setAdding(false)
    }
  }
  const remove = async (id: string) => {
    try {
      await removePasskey({ data: { id } })
      toast.success(t("settings.passkeys.removed"))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.passkeys.title")}</CardTitle>
        <CardDescription>{t("settings.passkeys.body")}</CardDescription>
        <CardAction>
          <Button variant="outline" size="sm" disabled={adding} onClick={() => void add()}>
            {adding ? <Spinner data-icon="inline-start" /> : <PlusIcon data-icon="inline-start" />}
            {t("invite.passkeyAdd")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {passkeys.length === 0 ? (
          <Empty className="border border-dashed p-6">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FingerprintIcon />
              </EmptyMedia>
              <EmptyTitle>{t("settings.passkeys.empty")}</EmptyTitle>
              <EmptyDescription>{t("settings.passkeys.emptyBody")}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup className="gap-0">
            {passkeys.map((p) => {
              // A name of only spaces is no name: it would leave a blank row.
              const name = p.name?.trim() || t("settings.passkeys.unnamed")
              return (
              <Item key={p.id} size="sm" className="px-0">
                <ItemMedia variant="icon">
                  <FingerprintIcon aria-hidden="true" />
                </ItemMedia>
                <ItemContent className="min-w-0">
                  {/* Wraps instead of truncating: two keys that share a long prefix differ at the end. */}
                  <ItemTitle className="line-clamp-none w-full min-w-0">
                    <span dir="auto" className="min-w-0 text-left [overflow-wrap:anywhere]">
                      {name}
                    </span>
                  </ItemTitle>
                  <ItemDescription>
                    {p.deviceType === "multiDevice" ? t("settings.passkeys.synced") : t("settings.passkeys.deviceOnly")}
                    {p.createdAt ? ` · ${t("settings.passkeys.added", { time: ago(p.createdAt) })}` : ""}
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <AlertDialog>
                    <AlertDialogTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("settings.passkeys.remove", { name })} disabled={passkeys.length <= 1} />}>
                      <TrashIcon aria-hidden="true" />
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>{t("settings.passkeys.removeTitle")}</AlertDialogTitle>
                        <AlertDialogDescription>{t("settings.passkeys.removeBody")}</AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
                        <AlertDialogAction variant="danger" onClick={() => void remove(p.id)}>
                          {t("connectors.remove")}
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                </ItemActions>
              </Item>
              )
            })}
          </ItemGroup>
        )}
        {passkeys.length === 1 && <p className="mt-3 text-caption text-muted-foreground">{t("settings.passkeys.lastOne")}</p>}
      </CardContent>
    </Card>
  )
}

function BackupCard({ backup }: { backup: Data["backup"] }) {
  const stale = backup ? Date.now() - backup.at > 7 * 86_400_000 : true
  const t = useT()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.backups.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        {backup ? (
          <Item size="sm" className="px-0">
            <ItemMedia variant="icon">
              <DatabaseBackupIcon aria-hidden="true" />
            </ItemMedia>
            <ItemContent className="min-w-0">
              <ItemTitle className="w-full min-w-0">
                <span className="truncate font-mono text-caption" title={backup.file}>
                  {backup.file}
                </span>
              </ItemTitle>
              <ItemDescription>
                {fullDate(backup.at)} · {(backup.bytes / 1_048_576).toFixed(1)} MB
              </ItemDescription>
            </ItemContent>
            <ItemActions>{stale ? <StatusText tone="warning">{t("settings.backups.stale")}</StatusText> : <StatusText tone="success">{t("settings.backups.recent")}</StatusText>}</ItemActions>
          </Item>
        ) : (
          <Alert>
            <AlertTitle>{t("settings.backups.none")}</AlertTitle>
            <AlertDescription>
              {t("settings.backups.noneIn")} <code>/data/backups</code>. {t("settings.backups.noneMake")} <code className="break-all">docker compose exec app palui backup /data/backups/palui.db.gz</code>.
            </AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}

function HooksCard({ data }: { data: Data }) {
  const router = useRouter()
  const [open, setOpen] = React.useState(false)
  const [name, setName] = React.useState("")
  const [chatId, setChatId] = React.useState(data.chats[0]?.id ?? "")
  const [mode, setMode] = React.useState<"forward" | "summarize">("forward")
  const [busy, setBusy] = React.useState(false)
  const [created, setCreated] = React.useState<{ url: string; secret: string } | null>(null)
  const t = useT()
  const label = useLabel()
  // A hook can outlive its chat: say so instead of showing the chat's id.
  const chatTitle = (id: string) => data.chats.find((c) => c.id === id)?.title ?? t("runs.deletedChat")

  const create = async () => {
    setBusy(true)
    try {
      const res = await createHookFn({ data: { name: name.trim(), chatId, mode } })
      setCreated(res)
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.hooks.title")}</CardTitle>
        <CardDescription>{t("settings.hooks.body")}</CardDescription>
        <CardAction>
          <Button variant="outline" size="sm" onClick={() => (setCreated(null), setName(""), setOpen(true))} disabled={!data.chats.length}>
            <PlusIcon data-icon="inline-start" />
            {t("settings.hooks.new")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {data.hooks.length === 0 ? (
          <Empty className="border border-dashed p-6">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <WebhookIcon />
              </EmptyMedia>
              <EmptyTitle>{t("settings.hooks.empty")}</EmptyTitle>
              {!data.chats.length && <EmptyDescription>{t("settings.hooks.emptyNoChat")}</EmptyDescription>}
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup className="gap-0">
            {data.hooks.map((h) => (
              <Item key={h.id} size="sm" className="px-0">
                <ItemContent className="min-w-0">
                  {/* The name wraps (60 characters at most), so it reads in full beside a narrow Copy button. */}
                  <ItemTitle className="line-clamp-none w-full min-w-0 flex-wrap">
                    <span dir="auto" className="min-w-0 text-left [overflow-wrap:anywhere]">
                      {h.name}
                    </span>
                    <Badge variant="outline">{label("hook.mode", h.mode)}</Badge>
                    {!h.enabled && <StatusText tone="secondary">{t("settings.hooks.off")}</StatusText>}
                  </ItemTitle>
                  <ItemDescription className="font-mono break-all">{`${data.appUrl}/api/hooks/${h.slug}`}</ItemDescription>
                  <ItemDescription dir="auto" className="text-left break-words" title={chatTitle(h.chatId)}>
                    {t("settings.hooks.to", { chat: chatTitle(h.chatId) })} · {h.lastAt ? t("settings.hooks.last", { time: ago(h.lastAt) }) : t("settings.hooks.never")}
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <CopyButton text={`${data.appUrl}/api/hooks/${h.slug}`} label={t("settings.hooks.copyUrl")} size="sm" variant="ghost" />
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{created ? t("settings.hooks.created") : t("settings.hooks.new")}</DialogTitle>
            {created && <DialogDescription>{t("settings.hooks.createdBody")}</DialogDescription>}
          </DialogHeader>
          {created ? (
            <FieldGroup>
              <Field>
                <FieldLabel>URL</FieldLabel>
                <div className="flex min-w-0 items-center gap-2">
                  <code className="min-w-0 flex-1 font-mono text-caption break-all">{created.url}</code>
                  <CopyButton text={created.url} />
                </div>
              </Field>
              <Field>
                <FieldLabel>Secret</FieldLabel>
                <div className="flex min-w-0 items-center gap-2">
                  <code className="min-w-0 flex-1 rounded-md bg-muted p-2 font-mono text-caption break-all">{created.secret}</code>
                  <CopyButton text={created.secret} />
                </div>
              </Field>
              <Field>
                <FieldLabel>{t("settings.hooks.tryIt")}</FieldLabel>
                <div className="flex min-w-0 items-start gap-2">
                  <code className="min-w-0 flex-1 rounded-md bg-muted p-2 font-mono text-caption break-all">{curlFor(created.url, created.secret)}</code>
                  <CopyButton text={curlFor(created.url, created.secret)} />
                </div>
              </Field>
            </FieldGroup>
          ) : (
            <form
              id="new-hook"
              onSubmit={(e) => {
                e.preventDefault()
                if (name.trim() && chatId) void create()
              }}
            >
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="hook-name">{t("connectors.field.name")}</FieldLabel>
                  <Input id="hook-name" autoFocus maxLength={60} value={name} onChange={(e) => setName(e.target.value)} placeholder="GitHub deploys" />
                </Field>
                <Field>
                  <FieldLabel htmlFor="hook-chat">{t("settings.hooks.postTo")}</FieldLabel>
                  <SimpleSelect id="hook-chat" className="w-full" value={chatId} options={data.chats.map((c) => ({ value: c.id, label: `${c.title} (${c.channel})` }))} onChange={setChatId} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="hook-mode">{t("settings.hooks.mode")}</FieldLabel>
                  <SimpleSelect
                    id="hook-mode"
                    className="w-full"
                    value={mode}
                    options={[
                      { value: "forward", label: t("settings.hooks.modeForward") },
                      { value: "summarize", label: t("settings.hooks.modeSummarize") },
                    ]}
                    onChange={(v) => setMode(v as typeof mode)}
                  />
                </Field>
              </FieldGroup>
            </form>
          )}
          <DialogFooter>
            {created ? (
              <Button onClick={() => setOpen(false)}>{t("settings.hooks.done")}</Button>
            ) : (
              <>
                <Button variant="outline" onClick={() => setOpen(false)}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" form="new-hook" disabled={busy || !name.trim() || !chatId}>
                  {busy && <Spinner data-icon="inline-start" />}
                  {t("settings.hooks.create")}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}

// The General tab's card, the one most visits land on: a line pointing to the
// agents' personas, then four fields in two columns, and Save in the footer. Other tabs get a card
// of plain fields.
function SettingsSkeleton() {
  const { tab = "general" } = Route.useSearch()
  return (
    <PagePending page="nav.settings">
      <PageHeaderSkeleton title="w-28" />
      <div className="flex flex-col gap-6">
        <LineTabsSkeleton widths={["w-14", "w-12", "w-12", "w-16", "w-10"]} />
        <Card>
          <CardHeader>
            <SkeletonText className="font-heading text-heading" width="w-36" />
            <SkeletonText className="text-sm" width="w-64" />
          </CardHeader>
          <CardContent>
            <FieldGroup>
              {tab === "general" && <SkeletonText className="text-sm" width="w-72" />}
              <div className="grid grid-cols-1 gap-4 @2xl/main:grid-cols-2">
                {[
                  { label: "w-20", help: "w-56" },
                  { label: "w-24" },
                  { label: "w-32" },
                  { label: "w-28", help: "w-52" },
                ].map((f, i) => (
                  <Field key={i}>
                    <SkeletonText className="text-sm" width={f.label} />
                    <Skeleton className="h-8 w-full rounded-lg pointer-coarse:h-10" />
                    {f.help && <SkeletonText className="text-sm" width={f.help} />}
                  </Field>
                ))}
              </div>
            </FieldGroup>
          </CardContent>
          <CardFooter className="justify-end border-t">
            <SkeletonButton className="w-14" />
          </CardFooter>
        </Card>
      </div>
    </PagePending>
  )
}

// A ready-to-run signed request for a new hook, so the secret can be tested
// right away. Signing is HMAC-SHA256 over "<timestamp>.<raw body>".
function curlFor(url: string, secret: string): string {
  return `BODY='Hello from curl'; TS=$(date +%s); curl -X POST '${url}' -H "x-palui-timestamp: $TS" -H "x-palui-signature: sha256=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac '${secret}' -hex | sed 's/^.* //')" -d "$BODY"`
}

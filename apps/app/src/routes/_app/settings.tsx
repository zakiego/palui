// Settings (PLAN 11.4): persona, timezone, language, model roles and failover,
// budgets, passkeys, backup status, and inbound hooks.
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { ArrowDownIcon, ArrowUpIcon, DatabaseBackupIcon, FingerprintIcon, PlusIcon, TrashIcon, WebhookIcon, XIcon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { PageHeader } from "@/components/page-header"
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
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { authClient, passkeyCancelled } from "@/lib/auth-client"
import { removePasskey } from "@/lib/fns/session"
import { createHookFn, getSettingsPage, saveSettings } from "@/lib/fns/settings"
import { ago, errorMessage, fullDate } from "@/lib/format"
import { useT } from "@/lib/i18n"

const TABS = ["general", "models", "limits", "security", "hooks"] as const

export const Route = createFileRoute("/_app/settings")({
  validateSearch: z.object({ tab: z.enum(TABS).optional() }),
  loader: () => getSettingsPage(),
  head: () => ({ meta: [{ title: "Settings · Palui" }] }),
  component: SettingsPage,
  pendingComponent: SettingsSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getSettingsPage>>
type SaveInput = Parameters<typeof saveSettings>[0]["data"]

function useSave() {
  const router = useRouter()
  const [busy, setBusy] = React.useState(false)
  const save = async (patch: SaveInput, okText = "Saved") => {
    setBusy(true)
    try {
      await saveSettings({ data: patch })
      toast.success(okText)
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
      <PageHeader title={t("settings.title")} description={t("settings.description")} />
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: { tab: v as (typeof TABS)[number] }, replace: true })} className="gap-4">
        <div className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
          <TabsList>
            <TabsTrigger value="general">General</TabsTrigger>
            <TabsTrigger value="models">Models</TabsTrigger>
            <TabsTrigger value="limits">Limits</TabsTrigger>
            <TabsTrigger value="security">Security</TabsTrigger>
            <TabsTrigger value="hooks">Hooks</TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="general">
          <GeneralTab data={data} />
        </TabsContent>
        <TabsContent value="models">
          <ModelsTab data={data} />
        </TabsContent>
        <TabsContent value="limits">
          <LimitsTab data={data} />
        </TabsContent>
        <TabsContent value="security" className="flex flex-col gap-4">
          <PasskeysCard passkeys={data.passkeys} />
          <BackupCard backup={data.backup} />
        </TabsContent>
        <TabsContent value="hooks">
          <HooksCard data={data} />
        </TabsContent>
      </Tabs>
    </>
  )
}

function GeneralTab({ data }: { data: Data }) {
  const s = data.settings
  const [persona, setPersona] = React.useState(s.persona)
  const [timezone, setTimezone] = React.useState(s.timezone)
  const [language, setLanguage] = React.useState(s.language)
  const [uiLanguage, setUiLanguage] = React.useState(s.uiLanguage)
  const [fallback, setFallback] = React.useState(s.fallbackChannel)
  const { busy, save } = useSave()
  return (
    <Card>
      <CardHeader>
        <CardTitle>How Palui talks</CardTitle>
        <CardDescription>The persona goes at the top of every turn.</CardDescription>
      </CardHeader>
      <CardContent>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="persona">Persona</FieldLabel>
            <Textarea id="persona" rows={8} maxLength={8000} value={persona} onChange={(e) => setPersona(e.target.value)} />
            <FieldDescription>
              <span className="tabular-nums">{persona.length.toLocaleString()}</span> of 8,000 characters.
            </FieldDescription>
          </Field>
          <div className="grid gap-4 @2xl/main:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="timezone">Time zone</FieldLabel>
              <Input id="timezone" className="font-mono" spellCheck={false} value={timezone} onChange={(e) => setTimezone(e.target.value)} />
              <FieldDescription>IANA name. New reminders and "today" use it.</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="language">Bot language</FieldLabel>
              <SimpleSelect
                id="language"
                className="w-full"
                value={language}
                options={[
                  { value: "auto", label: "Casual Indonesian, English when I write English" },
                  { value: "id", label: "Always Indonesian" },
                  { value: "en", label: "Always English" },
                ]}
                onChange={(v) => setLanguage(v as typeof language)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="ui-language">Dashboard language</FieldLabel>
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
              <FieldLabel htmlFor="fallback">Fallback channel</FieldLabel>
              <SimpleSelect
                id="fallback"
                className="w-full"
                value={fallback}
                options={[
                  { value: "telegram", label: "Telegram" },
                  { value: "whatsapp", label: "WhatsApp" },
                  { value: "none", label: "None" },
                ]}
                onChange={(v) => setFallback(v as typeof fallback)}
              />
              <FieldDescription>Where a reminder goes after two failed sends.</FieldDescription>
            </Field>
          </div>
        </FieldGroup>
      </CardContent>
      <CardFooter className="justify-end border-t">
        <Button disabled={busy} onClick={() => void save({ persona, timezone: timezone.trim(), language, uiLanguage, fallbackChannel: fallback })}>
          {busy && <Spinner data-icon="inline-start" />}
          Save
        </Button>
      </CardFooter>
    </Card>
  )
}

type Role = "default" | "fast" | "vision"
const ROLE_HELP: Record<Role, string> = {
  default: "Every normal turn.",
  fast: "Titles, summaries, reminder parsing.",
  vision: "Messages with photos.",
}

function ModelsTab({ data }: { data: Data }) {
  const [roles, setRoles] = React.useState(data.settings.modelRoles)
  const { busy, save } = useSave()
  const options = data.models.map((m) => ({ value: m.id, label: m.label }))
  const known = new Set(options.map((o) => o.value))
  const setChain = (role: Role, chain: { connectorId: string; model: string }[]) => setRoles((r) => ({ ...r, [role]: chain }))

  return (
    <Card>
      <CardHeader>
        <CardTitle>Model roles</CardTitle>
        <CardDescription>Each role tries its chain top to bottom. On a 5xx, a 429 or a quota error, the next one takes over.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {options.length === 0 && (
          <Alert>
            <AlertTitle>No model connectors yet</AlertTitle>
            <AlertDescription>Add CLIProxyAPI or ai-gateway in Saklar. Chains below keep their ids until then.</AlertDescription>
          </Alert>
        )}
        {(["default", "fast", "vision"] as Role[]).map((role) => {
          const chain = roles[role]
          return (
            <FieldSet key={role} className="gap-2">
              <FieldLegend variant="label" className="font-mono">
                {role}
              </FieldLegend>
              <FieldDescription>{ROLE_HELP[role]}</FieldDescription>
              <ol className="flex flex-col gap-2">
                {chain.map((link, i) => (
                  <li key={i} className="flex flex-wrap items-center gap-2">
                    <span className="w-5 font-mono text-caption text-muted-foreground tabular-nums">{i + 1}.</span>
                    <SimpleSelect
                      aria-label={`${role} step ${i + 1} connector`}
                      className="w-40"
                      value={link.connectorId}
                      options={known.has(link.connectorId) ? options : [{ value: link.connectorId, label: `${link.connectorId} (missing)` }, ...options]}
                      onChange={(v) => setChain(role, chain.map((x, j) => (j === i ? { ...x, connectorId: v } : x)))}
                    />
                    <Input
                      aria-label={`${role} step ${i + 1} model`}
                      className="min-w-40 flex-1 font-mono"
                      spellCheck={false}
                      value={link.model}
                      onChange={(e) => setChain(role, chain.map((x, j) => (j === i ? { ...x, model: e.target.value } : x)))}
                    />
                    <span className="flex gap-1">
                      <Button variant="ghost" size="icon-sm" aria-label={`Move ${role} step ${i + 1} up`} disabled={i === 0} onClick={() => setChain(role, swap(chain, i, i - 1))}>
                        <ArrowUpIcon aria-hidden="true" />
                      </Button>
                      <Button variant="ghost" size="icon-sm" aria-label={`Move ${role} step ${i + 1} down`} disabled={i === chain.length - 1} onClick={() => setChain(role, swap(chain, i, i + 1))}>
                        <ArrowDownIcon aria-hidden="true" />
                      </Button>
                      <Button variant="ghost" size="icon-sm" aria-label={`Remove ${role} step ${i + 1}`} disabled={chain.length === 1} onClick={() => setChain(role, chain.filter((_, j) => j !== i))}>
                        <XIcon aria-hidden="true" />
                      </Button>
                    </span>
                  </li>
                ))}
              </ol>
              <Button
                variant="outline"
                size="sm"
                className="self-start"
                disabled={chain.length >= 6}
                onClick={() => setChain(role, [...chain, { connectorId: options[0]?.value ?? "ai-gateway", model: chain.at(-1)?.model ?? "" }])}
              >
                <PlusIcon data-icon="inline-start" />
                Add fallback
              </Button>
            </FieldSet>
          )
        })}
      </CardContent>
      <CardFooter className="justify-end border-t">
        <Button
          disabled={busy}
          onClick={() => {
            const bad = (["default", "fast", "vision"] as Role[]).find((r) => roles[r].some((x) => !x.model.trim()))
            if (bad) return toast.error(`Fill in every model id in the ${bad} chain.`)
            void save({ modelRoles: roles })
          }}
        >
          {busy && <Spinner data-icon="inline-start" />}
          Save
        </Button>
      </CardFooter>
    </Card>
  )
}

function swap<T>(list: T[], a: number, b: number): T[] {
  const next = [...list]
  ;[next[a], next[b]] = [next[b]!, next[a]!]
  return next
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
        <CardTitle>Budgets</CardTitle>
        <CardDescription>Hitting a cap pauses turns and tells you on the channel. Plain reminders and commands keep working.</CardDescription>
      </CardHeader>
      <CardContent>
        <FieldGroup className="grid gap-4 @2xl/main:grid-cols-2">
          {field("maxSteps", "Steps per turn", "Tool calls and replies in one turn. 1 to 50.", 1, 50)}
          {field("turnTimeoutS", "Turn time limit (seconds)", "10 to 600.", 10, 600)}
          {field("maxTools", "Tools per turn", "The most relevant connectors go first. 1 to 128.", 1, 128)}
          {field("dailyTokenCap", "Daily token cap", "Across every chat and job.", 10_000, 100_000_000)}
          {field("maxRunsPerHour", "Runs per hour", "1 to 1,000.", 1, 1000)}
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
          Save
        </Button>
      </CardFooter>
    </Card>
  )
}

function PasskeysCard({ passkeys }: { passkeys: Data["passkeys"] }) {
  const router = useRouter()
  const [adding, setAdding] = React.useState(false)
  const add = async () => {
    setAdding(true)
    try {
      const res = await authClient.passkey.addPasskey({ name: `Added ${new Date().toISOString().slice(0, 10)}` })
      if (res?.error) {
        if (!passkeyCancelled(res.error)) toast.error(res.error.message ?? "That passkey couldn't be saved.")
        return
      }
      toast.success("Passkey added")
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
      toast.success("Passkey removed")
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Passkeys</CardTitle>
        <CardDescription>The only way into this dashboard. Lost them all? Run palui owner invite on the server.</CardDescription>
        <CardAction>
          <Button variant="outline" size="sm" disabled={adding} onClick={() => void add()}>
            {adding ? <Spinner data-icon="inline-start" /> : <PlusIcon data-icon="inline-start" />}
            Add passkey
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
              <EmptyTitle>No passkey yet</EmptyTitle>
              <EmptyDescription>Add one now, or you'll need a new owner link to get back in.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup className="gap-0">
            {passkeys.map((p) => (
              <Item key={p.id} size="sm" className="px-0">
                <ItemMedia variant="icon">
                  <FingerprintIcon aria-hidden="true" />
                </ItemMedia>
                <ItemContent className="min-w-0">
                  <ItemTitle className="w-full min-w-0">
                    <span className="truncate">{p.name || "Passkey"}</span>
                  </ItemTitle>
                  <ItemDescription>
                    {p.deviceType === "multiDevice" ? "Synced" : "This device only"}
                    {p.createdAt ? ` · added ${ago(p.createdAt)}` : ""}
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <AlertDialog>
                    <AlertDialogTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Remove ${p.name || "passkey"}`} disabled={passkeys.length <= 1} />}>
                      <TrashIcon aria-hidden="true" />
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>Remove this passkey?</AlertDialogTitle>
                        <AlertDialogDescription>That device can't sign in to Palui anymore.</AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>Keep it</AlertDialogCancel>
                        <AlertDialogAction variant="danger" onClick={() => void remove(p.id)}>
                          Remove
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
        {passkeys.length === 1 && <p className="mt-3 text-caption text-muted-foreground">Add a second passkey before you can remove this one.</p>}
      </CardContent>
    </Card>
  )
}

function BackupCard({ backup }: { backup: Data["backup"] }) {
  const stale = backup ? Date.now() - backup.at > 7 * 86_400_000 : true
  return (
    <Card>
      <CardHeader>
        <CardTitle>Backups</CardTitle>
        <CardDescription>deploy.sh snapshots the database before each deploy and keeps 3.</CardDescription>
      </CardHeader>
      <CardContent>
        {backup ? (
          <Item size="sm" className="px-0">
            <ItemMedia variant="icon">
              <DatabaseBackupIcon aria-hidden="true" />
            </ItemMedia>
            <ItemContent className="min-w-0">
              <ItemTitle className="w-full min-w-0">
                <span className="truncate font-mono text-caption">{backup.file}</span>
              </ItemTitle>
              <ItemDescription>
                {fullDate(backup.at)} · {(backup.bytes / 1_048_576).toFixed(1)} MB
              </ItemDescription>
            </ItemContent>
            <ItemActions>{stale ? <Badge variant="warning">over a week old</Badge> : <Badge variant="success">recent</Badge>}</ItemActions>
          </Item>
        ) : (
          <Alert>
            <AlertTitle>No backup found</AlertTitle>
            <AlertDescription>
              Nothing in <code>/data/backups</code> yet. Make one with <code>docker compose exec app palui backup /data/backups/palui.db.gz</code>.
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
  const chatTitle = (id: string) => data.chats.find((c) => c.id === id)?.title ?? id

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
        <CardTitle>Inbound hooks</CardTitle>
        <CardDescription>Other services POST to a hook; Palui forwards it to a chat. Hook text is untrusted: summaries run with zero tools.</CardDescription>
        <CardAction>
          <Button variant="outline" size="sm" onClick={() => (setCreated(null), setName(""), setOpen(true))} disabled={!data.chats.length}>
            <PlusIcon data-icon="inline-start" />
            New hook
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
              <EmptyTitle>No hooks yet</EmptyTitle>
              <EmptyDescription>{data.chats.length ? "Make one for deploy notices, form submissions or alerts." : "Open a chat first: a hook needs somewhere to post."}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup className="gap-0">
            {data.hooks.map((h) => (
              <Item key={h.id} size="sm" className="px-0">
                <ItemContent className="min-w-0">
                  <ItemTitle className="w-full min-w-0">
                    <span className="truncate">{h.name}</span>
                    <Badge variant="outline">{h.mode}</Badge>
                  </ItemTitle>
                  <ItemDescription className="font-mono break-all">{`${data.appUrl}/api/hooks/${h.slug}`}</ItemDescription>
                  <ItemDescription>
                    To {chatTitle(h.chatId)} · {h.lastAt ? `last ${ago(h.lastAt)}` : "never called"}
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <CopyButton text={`${data.appUrl}/api/hooks/${h.slug}`} label="Copy URL" size="sm" variant="ghost" />
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{created ? "Hook created" : "New hook"}</DialogTitle>
            <DialogDescription>{created ? "Copy the secret now. You won't see it again." : "Requests carry x-palui-timestamp and an HMAC-SHA256 signature over the timestamp and raw body, made with the hook's secret."}</DialogDescription>
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
                  <FieldLabel htmlFor="hook-name">Name</FieldLabel>
                  <Input id="hook-name" autoFocus maxLength={60} value={name} onChange={(e) => setName(e.target.value)} placeholder="GitHub deploys" />
                </Field>
                <Field>
                  <FieldLabel htmlFor="hook-chat">Post to</FieldLabel>
                  <SimpleSelect id="hook-chat" className="w-full" value={chatId} options={data.chats.map((c) => ({ value: c.id, label: `${c.title} (${c.channel})` }))} onChange={setChatId} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="hook-mode">Mode</FieldLabel>
                  <SimpleSelect
                    id="hook-mode"
                    className="w-full"
                    value={mode}
                    options={[
                      { value: "forward", label: "Forward the text as is (no model)" },
                      { value: "summarize", label: "Summarize it (a turn with zero tools)" },
                    ]}
                    onChange={(v) => setMode(v as typeof mode)}
                  />
                </Field>
              </FieldGroup>
            </form>
          )}
          <DialogFooter>
            {created ? (
              <Button onClick={() => setOpen(false)}>Done</Button>
            ) : (
              <>
                <Button variant="outline" onClick={() => setOpen(false)}>
                  Cancel
                </Button>
                <Button type="submit" form="new-hook" disabled={busy || !name.trim() || !chatId}>
                  {busy && <Spinner data-icon="inline-start" />}
                  Create hook
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}

function SettingsSkeleton() {
  return (
    <>
      <div className="flex flex-col gap-2">
        <Skeleton className="h-8 w-28" />
        <Skeleton className="h-4 w-80 max-w-full" />
      </div>
      <Skeleton className="h-8 w-80 max-w-full" />
      <Skeleton className="h-96 rounded-xl" />
    </>
  )
}

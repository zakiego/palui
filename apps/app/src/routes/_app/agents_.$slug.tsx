// One agent's page (PLAN 19.7): profile, shared-chat rules, limits, its
// standing instructions and the chats it answers in. The settings save
// together from the bar at the bottom; instructions and the per-chat switches
// save on their own, the way they do on Notes and Chats.
import * as React from "react"
import { createFileRoute, Link, useBlocker, useNavigate, useRouter } from "@tanstack/react-router"
import { ArrowLeftIcon, CircleDotIcon, MessageCircleIcon, MessagesSquareIcon, MonitorIcon, PlusIcon, SearchXIcon, SendIcon } from "lucide-react"
import { toast } from "sonner"

import { AgentAvatar } from "@/components/agent-avatar"
import { AvatarPicker } from "@/components/agents/avatar-picker"
import { AppError } from "@/components/app-error"
import { InstructionList, InstructionSheet, useInstructionSheet } from "@/components/instructions"
import { ListCapNote } from "@/components/list-cap-note"
import { PagePending } from "@/components/page-pending"
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
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { deleteAgentFn, getAgentPage, saveAgentFn, setChatAllowedFn } from "@/lib/fns/agents"
import { ago, errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/agents_/$slug")({
  loader: ({ params }) => getAgentPage({ data: { slug: params.slug } }),
  head: ({ loaderData }) => ({ meta: [{ title: `${loaderData?.agent?.name ?? "Agent"} · Palui` }] }),
  component: AgentPage,
  pendingComponent: AgentPageSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getAgentPage>>
type Loaded = Exclude<Data, { agent: null }>
type Agent = Loaded["agent"]
type AgentChat = Loaded["chats"][number]
type SaveData = Parameters<typeof saveAgentFn>[0]["data"]

const NAME_MAX = 40
const PERSONA_MAX = 8000
const CONTAINER_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/
const CHANNEL = {
  telegram: { Icon: SendIcon, label: "Telegram" },
  whatsapp: { Icon: MessageCircleIcon, label: "WhatsApp" },
  web: { Icon: MonitorIcon, label: "Web" },
} as const

type Form = {
  name: string
  persona: string
  ownerPowers: "off" | "approve"
  guestTools: "none" | "safe"
  cap: string
  container: string
  bot: string
  avatarImage: string | null
  avatarIcon: string | null
  avatarColor: string | null
}
type FormErrors = Partial<Record<"name" | "persona" | "cap" | "container", string>>

function formFor(a: Agent): Form {
  return {
    name: a.name,
    persona: a.persona,
    ownerPowers: a.ownerPowers,
    guestTools: a.guestTools,
    cap: String(a.dailyTokenCap),
    container: a.recallContainer ?? "",
    bot: a.telegramConnectorId ?? "",
    avatarImage: a.avatarImage,
    avatarIcon: a.avatarIcon,
    avatarColor: a.avatarColor,
  }
}

const sameForm = (a: Form, b: Form) => (Object.keys(a) as (keyof Form)[]).every((k) => a[k] === b[k])

function BackButton() {
  const t = useT()
  return (
    <Button variant="ghost" size="sm" className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/agents" />}>
      <ArrowLeftIcon data-icon="inline-start" />
      {t("nav.agents")}
    </Button>
  )
}

function AgentPage() {
  const data = Route.useLoaderData()
  const t = useT()
  if (data.agent === null) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SearchXIcon />
          </EmptyMedia>
          <EmptyTitle>{t("agents.notFound")}</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <BackButton />
        </EmptyContent>
      </Empty>
    )
  }
  return <AgentSettings key={data.agent.id} data={data} />
}

function AgentSettings({ data }: { data: Loaded }) {
  const t = useT()
  const router = useRouter()
  const navigate = useNavigate()
  const agent = data.agent
  const saved = formFor(agent)
  const [form, setForm] = React.useState(saved)
  const [errors, setErrors] = React.useState<FormErrors>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete">(null)
  const dirty = !sameForm(form, saved)
  const isDefault = agent.isDefault
  const instructionSheet = useInstructionSheet()

  // Chat can add or change an instruction while the page is open.
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "notes")

  const set = <K extends keyof Form>(k: K, v: Form[K]) => {
    setForm((prev) => ({ ...prev, [k]: v }))
    if (k in errors) setErrors((prev) => ({ ...prev, [k]: undefined }))
  }

  // Leaving with unsaved settings asks first. A delete navigates away on purpose.
  const leaving = React.useRef(false)
  const blocker = useBlocker({
    shouldBlockFn: () => dirty && !leaving.current,
    enableBeforeUnload: () => dirty && !leaving.current,
    withResolver: true,
  })

  const save = async () => {
    const next: FormErrors = {}
    if (!form.name.trim()) next.name = t("agents.err.name")
    if (!form.persona.trim()) next.persona = t("agents.err.persona")
    const cap = Number(form.cap.replace(/[.,\s]/g, ""))
    if (!form.cap.trim() || !Number.isInteger(cap) || cap < 0) next.cap = t("agents.err.cap")
    if (form.container.trim() && !CONTAINER_RE.test(form.container.trim())) next.container = t("agents.err.container")
    setErrors(next)
    const first = (["name", "persona", "cap", "container"] as const).find((k) => next[k])
    if (first) {
      document.getElementById(`agent-${first}`)?.focus()
      return
    }
    setBusy("save")
    try {
      const clean: Form = { ...form, name: form.name.trim(), persona: form.persona.trim(), cap: String(cap), container: form.container.trim() }
      await saveAgentFn({
        data: {
          id: agent.id,
          name: clean.name,
          persona: clean.persona,
          ownerPowers: clean.ownerPowers,
          guestTools: clean.guestTools,
          dailyTokenCap: cap,
          recallContainer: clean.container || null,
          telegramConnectorId: clean.bot || null,
          avatarImage: clean.avatarImage,
          avatarIcon: (clean.avatarIcon || null) as SaveData["avatarIcon"],
          avatarColor: (clean.avatarColor || null) as SaveData["avatarColor"],
        },
      })
      setForm(clean)
      await router.invalidate()
      toast.success(t("agents.saved", { name: clean.name }))
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    setBusy("delete")
    try {
      await deleteAgentFn({ data: { id: agent.id } })
      leaving.current = true
      toast.success(t("agents.deleted", { name: agent.name }))
      void navigate({ to: "/agents" })
    } catch (err) {
      toast.error(errorMessage(err))
      setBusy(null)
    }
  }

  const name = form.name.trim() || agent.name
  const over = agent.dailyTokenCap > 0 && agent.tokensToday >= agent.dailyTokenCap
  const container = form.container.trim() || (isDefault ? null : `agent-${agent.slug}`)
  const botOptions = [{ value: "", label: t("agents.botNone") }, ...data.bots.map((b) => ({ value: b.id, label: b.username ? `@${b.username}` : b.label }))]

  return (
    <>
      <div className="flex flex-col gap-3">
        <BackButton />
        <div className="flex min-w-0 items-center gap-3">
          <AgentAvatar agent={{ isDefault, avatarImage: form.avatarImage, avatarIcon: form.avatarIcon, avatarColor: form.avatarColor }} />
          <div className="flex min-w-0 flex-col gap-0.5">
            <h1 dir="auto" className="min-w-0 text-left font-heading text-title [overflow-wrap:anywhere]">
              {name}
            </h1>
            <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <span className="font-mono text-caption break-all text-muted-foreground">{agent.slug}</span>
              {isDefault && <StatusText tone="secondary">{t("agents.default")}</StatusText>}
              {over && <StatusText tone="warning">{t("agents.capHit")}</StatusText>}
            </span>
          </div>
        </div>
      </div>

      <div className="flex w-full max-w-2xl flex-col gap-6">
        <Section id="profile" title={t("agents.section.profile")}>
          <FieldGroup>
            <AvatarPicker
              agent={{ isDefault, avatarImage: form.avatarImage, avatarIcon: form.avatarIcon, avatarColor: form.avatarColor }}
              onImage={(v) => set("avatarImage", v)}
              onIcon={(v) => set("avatarIcon", v)}
              onColor={(v) => set("avatarColor", v)}
            />
            <div className="grid grid-cols-1 gap-4 @xl/main:grid-cols-2">
              <Field data-invalid={!!errors.name}>
                <FieldLabel htmlFor="agent-name">{t("agents.field.name")}</FieldLabel>
                <Input id="agent-name" value={form.name} maxLength={NAME_MAX} aria-invalid={!!errors.name} onChange={(e) => set("name", e.target.value)} />
                {errors.name && <FieldError>{errors.name}</FieldError>}
              </Field>
              <Field>
                <FieldLabel htmlFor="agent-slug">{t("agents.field.slug")}</FieldLabel>
                <Input id="agent-slug" className="font-mono" readOnly value={agent.slug} aria-describedby="agent-slug-hint" />
                <FieldDescription id="agent-slug-hint" className="[overflow-wrap:anywhere]">
                  {t("agents.slugHint", { slug: agent.slug })}
                </FieldDescription>
              </Field>
            </div>
            <Field data-invalid={!!errors.persona}>
              <FieldLabel htmlFor="agent-persona">{t("agents.field.persona")}</FieldLabel>
              {/* It grows with its text up to half the screen, then scrolls. */}
              <Textarea id="agent-persona" rows={8} maxLength={PERSONA_MAX} className="max-h-[50svh]" value={form.persona} aria-invalid={!!errors.persona} onChange={(e) => set("persona", e.target.value)} />
              <FieldDescription className="tabular-nums">{t("notes.chars", { n: form.persona.length.toLocaleString(), max: PERSONA_MAX.toLocaleString() })}</FieldDescription>
              {errors.persona && <FieldError>{errors.persona}</FieldError>}
            </Field>
            {isDefault && <p className="text-sm text-pretty text-muted-foreground">{t("agents.defaultNote")}</p>}
          </FieldGroup>
        </Section>

        {!isDefault && (
          <Section id="shared" title={t("agents.shared")} description={t("agents.sharedBody")}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="agent-guests">{t("agents.field.guestTools")}</FieldLabel>
                <SimpleSelect
                  id="agent-guests"
                  value={form.guestTools}
                  onChange={(v) => set("guestTools", v as Form["guestTools"])}
                  options={[
                    { value: "safe", label: t("agents.guestTools.safe") },
                    { value: "none", label: t("agents.guestTools.none") },
                  ]}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="agent-owner">{t("agents.field.ownerPowers")}</FieldLabel>
                <SimpleSelect
                  id="agent-owner"
                  value={form.ownerPowers}
                  onChange={(v) => set("ownerPowers", v as Form["ownerPowers"])}
                  options={[
                    { value: "off", label: t("agents.ownerPowers.off") },
                    { value: "approve", label: t("agents.ownerPowers.approve") },
                  ]}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="agent-bot">{t("agents.field.bot")}</FieldLabel>
                <SimpleSelect id="agent-bot" value={form.bot} onChange={(v) => set("bot", v)} options={botOptions} />
                <FieldDescription>
                  {t("agents.botHint")}{" "}
                  <Link to="/connectors/new" className="underline underline-offset-4">
                    {t("agents.botAdd")}
                  </Link>
                </FieldDescription>
              </Field>
            </FieldGroup>
          </Section>
        )}

        <Section id="limits" title={t("agents.section.limits")} description={t("agents.limitsBody")}>
          <FieldGroup>
            <TokensToday agent={agent} over={over} />
            <div className="grid grid-cols-1 gap-4 @xl/main:grid-cols-2">
              <Field data-invalid={!!errors.cap}>
                <FieldLabel htmlFor="agent-cap">{t("agents.field.cap")}</FieldLabel>
                <Input id="agent-cap" inputMode="numeric" className="tabular-nums" value={form.cap} aria-invalid={!!errors.cap} onChange={(e) => set("cap", e.target.value)} />
                {errors.cap ? <FieldError>{errors.cap}</FieldError> : <FieldDescription>{t("agents.capHint")}</FieldDescription>}
              </Field>
              <Field data-invalid={!!errors.container}>
                <FieldLabel htmlFor="agent-container">{t("agents.field.container")}</FieldLabel>
                <Input
                  id="agent-container"
                  className="font-mono"
                  spellCheck={false}
                  autoCapitalize="none"
                  autoComplete="off"
                  maxLength={64}
                  placeholder={isDefault ? t("agents.containerConnector") : `agent-${agent.slug}`}
                  value={form.container}
                  aria-invalid={!!errors.container}
                  onChange={(e) => set("container", e.target.value)}
                />
                {errors.container ? (
                  <FieldError>{errors.container}</FieldError>
                ) : (
                  <FieldDescription className="[overflow-wrap:anywhere]">
                    {t("agents.containerHint")} {container ? t("agents.containerIs", { name: container }) : t("agents.containerIsConnector")}
                  </FieldDescription>
                )}
              </Field>
            </div>
          </FieldGroup>
        </Section>

        <Section
          id="instructions"
          title={t("agents.section.instructions")}
          description={isDefault ? t("agents.instructionsBodyDefault") : t("agents.instructionsBody")}
          action={
            <Button variant="outline" size="sm" onClick={() => instructionSheet.edit("new")}>
              <PlusIcon data-icon="inline-start" />
              {t("notes.newInstruction")}
            </Button>
          }
        >
          <InstructionList items={data.instructions} onEdit={instructionSheet.edit} />
        </Section>

        <Section id="chats" title={t("agents.section.chats")} description={isDefault ? t("agents.chatsBodyDefault") : t("agents.chatsBody", { slug: agent.slug })}>
          {data.chats.length === 0 ? (
            <Empty className="border border-dashed p-6">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <MessagesSquareIcon />
                </EmptyMedia>
                <EmptyTitle>{t("agents.chatsEmpty")}</EmptyTitle>
                <EmptyDescription>{t("agents.chatsEmptyBody", { slug: agent.slug })}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div>
              <ul className="flex flex-col divide-y">
                {data.chats.map((c) => (
                  <ChatRow key={c.id} chat={c} />
                ))}
              </ul>
              <ListCapNote shown={data.chats.length} total={data.chatsTotal} />
            </div>
          )}
        </Section>

        {!isDefault && (
          <Section id="delete" title={t("agents.delete")} description={t("agents.deleteBody")}>
            <AlertDialog>
              <AlertDialogTrigger render={<Button variant="destructive" className="max-w-full self-start" disabled={busy !== null} />}>
                {busy === "delete" && <Spinner data-icon="inline-start" />}
                <span className="truncate">{t("agents.deleteNamed", { name: agent.name })}</span>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle className="[overflow-wrap:anywhere]">{t("agents.deleteTitle", { name: agent.name })}</AlertDialogTitle>
                  <AlertDialogDescription>{t("agents.deleteBody")}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
                  <AlertDialogAction variant="danger" onClick={() => void remove()}>
                    {t("common.delete")}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </Section>
        )}
      </div>

      <div className="sticky bottom-0 z-10 -mx-4 -mb-10 border-t bg-background/95 px-4 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur md:-mx-6 md:px-6 lg:-mx-8 lg:px-8">
        <div className="flex w-full max-w-2xl items-center gap-3">
          {dirty && (
            <StatusText tone="secondary" icon={<CircleDotIcon aria-hidden="true" />} className="min-w-0 truncate" role="status">
              {t("agents.unsaved")}
            </StatusText>
          )}
          <Button className="ms-auto" disabled={busy !== null || !dirty} onClick={() => void save()}>
            {busy === "save" && <Spinner data-icon="inline-start" />}
            {t("common.save")}
          </Button>
        </div>
      </div>

      <InstructionSheet key={instructionSheet.key} open={instructionSheet.open} target={instructionSheet.target} agentId={agent.id} onClose={instructionSheet.close} />

      <AlertDialog open={blocker.status === "blocked"} onOpenChange={(o) => !o && blocker.reset?.()}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("agents.leaveTitle")}</AlertDialogTitle>
            <AlertDialogDescription className="[overflow-wrap:anywhere]">{t("agents.leaveBody", { name: agent.name })}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => blocker.reset?.()}>{t("agents.stay")}</AlertDialogCancel>
            <AlertDialogAction variant="danger" onClick={() => blocker.proceed?.()}>
              {t("agents.leave")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function Section({ id, title, description, action, children }: { id: string; title: string; description?: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <>
      {id !== "profile" && <Separator />}
      <section aria-labelledby={`agent-section-${id}`} className="flex flex-col gap-4">
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
          <div className="flex min-w-0 flex-col gap-1">
            <h2 id={`agent-section-${id}`} className="text-heading">
              {title}
            </h2>
            {description && <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">{description}</p>}
          </div>
          {action}
        </div>
        {children}
      </section>
    </>
  )
}

function TokensToday({ agent, over }: { agent: Agent; over: boolean }) {
  const t = useT()
  const tokens = agent.tokensToday.toLocaleString()
  const capped = agent.dailyTokenCap > 0
  return (
    <div className="flex flex-col gap-2">
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <span className="text-sm font-medium">{t("agents.usedToday")}</span>
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground tabular-nums">
          {over && <StatusText tone="warning">{t("agents.capHit")}</StatusText>}
          {capped ? t("agents.tokensOfCap", { n: tokens, cap: agent.dailyTokenCap.toLocaleString() }) : t("agents.tokens", { n: tokens })}
        </span>
      </div>
      {capped ? (
        <Progress value={Math.min(100, (agent.tokensToday / agent.dailyTokenCap) * 100)} aria-label={t("agents.usedToday")} className={over ? "[&_[data-slot=progress-indicator]]:bg-warning" : undefined} />
      ) : (
        <p className="text-caption text-muted-foreground">{t("agents.noCap")}</p>
      )}
    </div>
  )
}

// A chat this agent answers in, with the same Answer here switch as the Chats page.
function ChatRow({ chat }: { chat: AgentChat }) {
  const t = useT()
  const router = useRouter()
  const [allowed, setAllowed] = React.useState(chat.allowed)
  React.useEffect(() => setAllowed(chat.allowed), [chat.allowed])
  const { Icon, label } = CHANNEL[chat.channel]

  const toggle = async (next: boolean) => {
    setAllowed(next)
    try {
      await setChatAllowedFn({ data: { chatId: chat.id, allowed: next } })
      void router.invalidate()
    } catch (err) {
      setAllowed(!next)
      toast.error(errorMessage(err))
    }
  }

  return (
    <li className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
      <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <Link
          to="/chats"
          search={{ chat: chat.id }}
          dir="auto"
          aria-label={t("agents.openChat", { name: chat.title })}
          className="line-clamp-2 min-w-0 text-left text-sm font-medium underline-offset-4 [overflow-wrap:anywhere] hover:underline"
        >
          {chat.title}
        </Link>
        <span className="flex min-w-0 flex-wrap gap-x-3 gap-y-0.5 text-caption text-muted-foreground tabular-nums">
          <span>{label}</span>
          {chat.channel !== "web" && <span>{chat.kind === "group" ? t("agents.chat.group") : t("agents.chat.dm")}</span>}
          <span>{ago(chat.lastAt)}</span>
        </span>
      </div>
      {chat.channel !== "web" && (
        <label className="flex shrink-0 items-center gap-2 text-sm">
          <span className="text-muted-foreground">{t("chats.allowed")}</span>
          <Switch checked={allowed} onCheckedChange={(v) => void toggle(v)} aria-label={t("chats.allowedFor", { name: chat.title })} />
        </label>
      )}
    </li>
  )
}

function AgentPageSkeleton() {
  return (
    <PagePending page="nav.agents">
      <Skeleton className="h-7 w-24" />
      <div className="flex items-center gap-3">
        <Skeleton className="size-10 rounded-lg" />
        <div className="flex flex-col gap-2">
          <Skeleton className="h-7 w-48" />
          <Skeleton className="h-4 w-24" />
        </div>
      </div>
      <div className="flex w-full max-w-2xl flex-col gap-4">
        <Skeleton className="h-40" />
        <Skeleton className="h-24" />
        <Skeleton className="h-24" />
      </div>
    </PagePending>
  )
}

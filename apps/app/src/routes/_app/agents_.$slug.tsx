// One agent's page (PLAN 19.7), in tabs: its settings, the chats it lives in,
// its standing instructions, where its memory is, and the tools it may use.
// The settings (and the memory container) save together from the bar at the
// bottom; instructions and the per-chat controls save on their own, the way
// they do on Notes and Chats. Memory and Tools ask the runtime when opened.
import * as React from "react"
import { createFileRoute, Link, useBlocker, useNavigate, useRouter } from "@tanstack/react-router"
import { ArrowLeftIcon, AtSignIcon, BrainIcon, CircleAlertIcon, ContactIcon, NotebookPenIcon, SlidersHorizontalIcon, CircleDotIcon, MessagesSquareIcon, MonitorIcon, PlusIcon, SearchIcon, SearchXIcon, UserIcon, UsersIcon, WrenchIcon } from "lucide-react"
import { TelegramIcon, WhatsAppIcon } from "@/components/brand-icons"
import { z } from "zod"
import { toast } from "sonner"

import { AgentAvatar } from "@/components/agent-avatar"
import { AgentMembers } from "@/components/agent-members"
import { AvatarPicker } from "@/components/agents/avatar-picker"
import { AppError } from "@/components/app-error"
import { ConnectorStatusBadge } from "@/components/connector-status"
import { ToolsByScope } from "@/components/agent-tools/by-scope"
import { groupTools, inMemberChats, inSharedChats, type Permission } from "@/components/agent-tools/shared"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { InstructionList, InstructionSheet, useInstructionSheet } from "@/components/instructions"
import { ListCapNote } from "@/components/list-cap-note"
import { PagePending } from "@/components/page-pending"
import { SimpleSelect } from "@/components/simple-select"
import { AckControl } from "@/components/ack-control"
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
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { setConnectorOwnerFn } from "@/lib/api/connectors"
import { deleteAgentFn, getAgentMemoryFn, getAgentPage, getAgentToolsFn, saveAgentFn, setAgentToolPermissionsFn, setChatAllowedFn, setChatDeployNoticeFn, setChatReplyModeFn } from "@/lib/api/agents"
import { ago, errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

const TABS = ["settings", "chats", "instructions", "memory", "tools"] as const
type Tab = (typeof TABS)[number]

export const Route = createFileRoute("/_app/agents_/$slug")({
  validateSearch: z.object({ tab: z.enum(TABS).optional() }),
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
const MAX_READS = 5
const splitReads = (v: string) => [...new Set(v.split(/[\s,]+/).map((c) => c.trim().toLowerCase()).filter(Boolean))]
const CHANNEL = {
  telegram: { Icon: TelegramIcon, label: "Telegram" },
  whatsapp: { Icon: WhatsAppIcon, label: "WhatsApp" },
  web: { Icon: MonitorIcon, label: "Web" },
} as const

type Form = {
  name: string
  persona: string
  ownerPowers: "off" | "approve"
  guestTools: "none" | "safe"
  cap: string
  container: string
  // Comma or space separated in the input, a list on save.
  reads: string
  perChat: boolean
  bot: string
  avatarImage: string | null
  avatarIcon: string | null
  avatarColor: string | null
}
type FormErrors = Partial<Record<"name" | "persona" | "cap" | "container" | "reads", string>>

function formFor(a: Agent): Form {
  return {
    name: a.name,
    persona: a.persona,
    ownerPowers: a.ownerPowers,
    guestTools: a.guestTools,
    cap: String(a.dailyTokenCap),
    container: a.recallContainer ?? "",
    reads: a.recallReadContainers.join(", "),
    perChat: a.recallPerChat,
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
  const navigate = useNavigate({ from: Route.fullPath })
  const agent = data.agent
  const saved = formFor(agent)
  const [form, setForm] = React.useState(saved)
  const [errors, setErrors] = React.useState<FormErrors>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete">(null)
  const dirty = !sameForm(form, saved)
  const isDefault = agent.isDefault
  const instructionSheet = useInstructionSheet()
  const tab: Tab = Route.useSearch().tab ?? "settings"

  // Chat can add or change an instruction while the page is open.
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "notes")

  const set = <K extends keyof Form>(k: K, v: Form[K]) => {
    setForm((prev) => ({ ...prev, [k]: v }))
    if (k in errors) setErrors((prev) => ({ ...prev, [k]: undefined }))
  }

  // Leaving with unsaved settings asks first. A delete navigates away on purpose.
  const leaving = React.useRef(false)
  const blocker = useBlocker({
    // A tab switch only changes the search, so it never asks.
    shouldBlockFn: ({ current, next }) => dirty && !leaving.current && current.pathname !== next.pathname,
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
    const reads = splitReads(form.reads)
    if (reads.length > MAX_READS) next.reads = t("agents.err.readsMax", { n: MAX_READS })
    else if (reads.some((c) => !CONTAINER_RE.test(c))) next.reads = t("agents.err.container")
    setErrors(next)
    const first = (["name", "persona", "cap", "container", "reads"] as const).find((k) => next[k])
    if (first) {
      const target: Tab = first === "container" || first === "reads" ? "memory" : "settings"
      if (target !== tab) await navigate({ search: { tab: target }, replace: true })
      requestAnimationFrame(() => document.getElementById(`agent-${first}`)?.focus())
      return
    }
    setBusy("save")
    try {
      const clean: Form = { ...form, name: form.name.trim(), persona: form.persona.trim(), cap: String(cap), container: form.container.trim(), reads: reads.join(", ") }
      await saveAgentFn({
        data: {
          id: agent.id,
          name: clean.name,
          persona: clean.persona,
          ownerPowers: clean.ownerPowers,
          guestTools: clean.guestTools,
          dailyTokenCap: cap,
          recallContainer: clean.container || null,
          recallReadContainers: reads,
          recallPerChat: clean.perChat,
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
  const botOptions = [{ value: "", label: t("agents.botNone") }, ...data.bots.map((b) => ({ value: b.id, label: b.username ? `@${b.username}` : b.label }))]

  return (
    <>
      {/* One width for the header, the tabs and every panel. */}
      <div className="flex w-full max-w-3xl flex-col gap-6">
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
                {isDefault && <StatusText tone="secondary" icon={null}>{t("agents.default")}</StatusText>}
                {over && <StatusText tone="warning">{t("agents.capHit")}</StatusText>}
              </span>
            </div>
          </div>
        </div>

        <Tabs value={tab} onValueChange={(v) => void navigate({ search: { tab: v as Tab }, replace: true, resetScroll: false })} className="gap-6">
          <SectionTabs counts={{ chats: data.chatsTotal, instructions: data.instructions.length }} />

          {/* Settings and Memory stay mounted (hidden) so unsaved edits survive a switch. */}
          <TabsContent value="settings" keepMounted className="flex flex-col gap-6 text-base">
            <Section id="profile" first title={t("agents.section.profile")}>
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
                      aria-describedby={form.guestTools === "safe" ? "agent-guests-hint" : undefined}
                    />
                    {form.guestTools === "safe" && (
                      <FieldDescription id="agent-guests-hint" className="text-pretty">
                        {t("agents.guestToolsHint")}
                      </FieldDescription>
                    )}
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

            {!isDefault && (
              <Section id="members" title={t("agents.members")} description={t("agents.membersBody")}>
                <AgentMembers agent={agent} />
              </Section>
            )}

            <Section id="limits" title={t("agents.section.limits")} description={t("agents.limitsBody")}>
              <FieldGroup>
                <TokensToday agent={agent} over={over} />
                <Field data-invalid={!!errors.cap} className="@xl/main:max-w-[calc(50%-0.5rem)]">
                  <FieldLabel htmlFor="agent-cap">{t("agents.field.cap")}</FieldLabel>
                  <Input id="agent-cap" inputMode="numeric" className="tabular-nums" value={form.cap} aria-invalid={!!errors.cap} onChange={(e) => set("cap", e.target.value)} />
                  {errors.cap ? <FieldError>{errors.cap}</FieldError> : <FieldDescription>{t("agents.capHint")}</FieldDescription>}
                </Field>
              </FieldGroup>
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
          </TabsContent>

          <TabsContent value="chats" className="flex flex-col gap-4 text-base">
            <p className="text-sm text-pretty text-muted-foreground">
              {isDefault ? t("agents.chatsBodyDefault") : t("agents.chatsBody", { slug: agent.slug })} {t("agents.deployNoticeHint")}
            </p>
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
          </TabsContent>

          <TabsContent value="instructions" className="flex flex-col gap-4 text-base">
            <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
              <p className="max-w-[65ch] min-w-0 flex-1 text-sm text-pretty text-muted-foreground">{isDefault ? t("agents.instructionsBodyDefault") : t("agents.instructionsBody")}</p>
              <Button variant="outline" size="sm" onClick={() => instructionSheet.edit("new")}>
                <PlusIcon data-icon="inline-start" />
                {t("notes.newInstruction")}
              </Button>
            </div>
            <InstructionList items={data.instructions} onEdit={instructionSheet.edit} />
          </TabsContent>

          <TabsContent value="memory" keepMounted className="flex flex-col gap-6 text-base">
            {tab === "memory" && <MemoryStats agent={agent} />}
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
                <FieldDescription>{t("agents.containerHint")}</FieldDescription>
              )}
            </Field>
            <Field data-invalid={!!errors.reads}>
              <FieldLabel htmlFor="agent-reads">{t("agents.field.reads")}</FieldLabel>
              <Input
                id="agent-reads"
                className="font-mono"
                spellCheck={false}
                autoCapitalize="none"
                autoComplete="off"
                maxLength={MAX_READS * 66}
                placeholder="kb-shop, kb-faq"
                value={form.reads}
                aria-invalid={!!errors.reads}
                onChange={(e) => set("reads", e.target.value)}
              />
              {errors.reads ? <FieldError>{errors.reads}</FieldError> : <FieldDescription>{isDefault ? t("agents.readsHintDefault") : t("agents.readsHint")}</FieldDescription>}
            </Field>
            {!isDefault && (
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="agent-per-chat">{t("agents.field.perChat")}</FieldLabel>
                  <FieldDescription>{t("agents.perChatHint")}</FieldDescription>
                </FieldContent>
                <Switch id="agent-per-chat" checked={form.perChat} onCheckedChange={(v) => set("perChat", v)} />
              </Field>
            )}
          </TabsContent>

          <TabsContent value="tools" className="flex flex-col gap-6 text-base">
            <ToolsTab agent={agent} />
          </TabsContent>
        </Tabs>
      </div>

      {(tab === "settings" || tab === "memory" || dirty) && (
      <div className="sticky bottom-0 z-10 -mx-4 -mb-10 border-t bg-background/95 px-4 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur md:-mx-6 md:px-6 lg:-mx-8 lg:px-8">
        <div className="flex w-full max-w-3xl items-center gap-3">
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
      )}

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

// The page's sections as Geist-style line tabs (icon, name, count), kept in ?tab=.
const SECTIONS = [
  { value: "settings", Icon: SlidersHorizontalIcon, title: "agents.tab.settings" },
  { value: "chats", Icon: MessagesSquareIcon, title: "agents.tab.chats" },
  { value: "instructions", Icon: NotebookPenIcon, title: "agents.tab.instructions" },
  { value: "memory", Icon: BrainIcon, title: "agents.tab.memory" },
  { value: "tools", Icon: WrenchIcon, title: "agents.tab.tools" },
] as const

function SectionTabs({ counts }: { counts: Partial<Record<Tab, number>> }) {
  const t = useT()
  return (
    <TabsList variant="line" aria-label={t("agents.sections")}>
      {SECTIONS.map(({ value: v, Icon, title }) => (
        <TabsTrigger key={v} value={v}>
          <Icon aria-hidden="true" className="hidden @xl/main:block" />
          {t(title)}
          {counts[v] !== undefined && <span className="font-normal text-muted-foreground tabular-nums">{counts[v]!.toLocaleString()}</span>}
        </TabsTrigger>
      ))}
    </TabsList>
  )
}

function Section({ id, first, title, description, action, children }: { id: string; first?: boolean; title: string; description?: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <>
      {!first && <Separator />}
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
  const [replyMode, setReplyMode] = React.useState(chat.replyMode)
  React.useEffect(() => setAllowed(chat.allowed), [chat.allowed])
  React.useEffect(() => setReplyMode(chat.replyMode), [chat.replyMode])
  const [notice, setNotice] = React.useState(chat.deployNotice)
  React.useEffect(() => setNotice(chat.deployNotice), [chat.deployNotice])
  const { Icon, label } = CHANNEL[chat.channel]

  const changeMode = async (next: string) => {
    if (next !== "mention" && next !== "all") return
    const before = replyMode
    setReplyMode(next)
    try {
      await setChatReplyModeFn({ data: { chatId: chat.id, mode: next } })
      void router.invalidate()
    } catch (err) {
      setReplyMode(before)
      toast.error(errorMessage(err))
    }
  }

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

  const toggleNotice = async (next: boolean) => {
    setNotice(next)
    try {
      await setChatDeployNoticeFn({ data: { chatId: chat.id, on: next } })
      void router.invalidate()
    } catch (err) {
      setNotice(!next)
      toast.error(errorMessage(err))
    }
  }

  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-3 py-4 first:pt-0 last:pb-0">
      <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <div className="flex min-w-48 flex-1 flex-col gap-0.5">
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
        <div className="ms-7 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 sm:ms-0">
          {chat.kind === "group" && (
            <SimpleSelect
              size="sm"
              className="w-fit min-w-0"
              aria-label={t("chats.replyModeFor", { name: chat.title })}
              value={replyMode}
              options={[
                { value: "mention", label: t("chats.replyMention"), icon: <AtSignIcon aria-hidden="true" /> },
                { value: "all", label: t("chats.replyAll"), icon: <MessagesSquareIcon aria-hidden="true" /> },
              ]}
              onChange={(v) => void changeMode(v)}
            />
          )}
          {chat.channel === "whatsapp" && <AckControl chat={chat} />}
          <label className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{t("chats.allowed")}</span>
            <Switch checked={allowed} onCheckedChange={(v) => void toggle(v)} aria-label={t("chats.allowedFor", { name: chat.title })} />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{t("agents.deployNotice")}</span>
            <Switch checked={notice} onCheckedChange={(v) => void toggleNotice(v)} aria-label={t("agents.deployNoticeFor", { name: chat.title })} />
          </label>
        </div>
      )}
    </li>
  )
}

// Loads a tab's data when the tab opens. Memory and Tools ask the runtime, so
// they never hold up the page.
function useTabData<T>(load: () => Promise<T>, key: string) {
  const [state, setState] = React.useState<{ data?: T; error?: string }>({})
  React.useEffect(() => {
    let live = true
    setState({})
    load().then(
      (data) => live && setState({ data }),
      (err: unknown) => live && setState({ error: errorMessage(err) }),
    )
    return () => {
      live = false
    }
    // `load` is a new closure every render; `key` says when it changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  return state
}

function LoadError({ message }: { message: string }) {
  const t = useT()
  return (
    <Alert variant="destructive">
      <CircleAlertIcon aria-hidden="true" />
      <AlertTitle>{t("agents.loadError")}</AlertTitle>
      <AlertDescription className="[overflow-wrap:anywhere]">{message}</AlertDescription>
    </Alert>
  )
}

// One label and value row, for the memory facts.
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-3 first:pt-0 last:pb-0">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-right text-sm [overflow-wrap:anywhere]">{children}</dd>
    </div>
  )
}

function MemoryStats({ agent }: { agent: Agent }) {
  const t = useT()
  const { data, error } = useTabData(() => getAgentMemoryFn({ data: { id: agent.id } }), `${agent.id}:${agent.recallContainer ?? ""}:${agent.recallPerChat}:${agent.recallReadContainers.join(",")}`)
  if (error) return <LoadError message={error} />
  if (!data) {
    return (
      <div className="flex flex-col gap-3" aria-busy="true">
        <Skeleton className="h-5 w-full" />
        <Skeleton className="h-5 w-full" />
        <Skeleton className="h-5 w-full" />
      </div>
    )
  }
  if (!data.connector) {
    return (
      <Empty className="border border-dashed p-6">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <BrainIcon />
          </EmptyMedia>
          <EmptyTitle>{t("agents.memory.none")}</EmptyTitle>
          <EmptyDescription>{t("agents.memory.noneBody")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" size="sm" render={<Link to="/connectors/new" />}>
            {t("agents.memory.add")}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }
  const s = data.stats
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-pretty text-muted-foreground">{agent.isDefault ? t("agents.memory.ownBody") : t("agents.memory.agentBody")}</p>
      {data.error && <LoadError message={data.error} />}
      <dl className="flex flex-col divide-y">
        <Fact label={t("agents.memory.where")}>
          <span className="font-mono">{data.perChat ? `${data.container}-c…` : data.container}</span>
        </Fact>
        {data.perChat && (
          <Fact label={t("agents.memory.chatContainers")}>
            <span className="tabular-nums">{s ? data.chatContainers.toLocaleString() : "–"}</span>
          </Fact>
        )}
        {data.reads.length > 0 && (
          <Fact label={t("agents.memory.reads")}>
            <span className="flex flex-col items-end gap-1 font-mono">
              {data.reads.map((c) => (
                <span key={c}>{c}</span>
              ))}
            </span>
          </Fact>
        )}
        <Fact label={t("agents.memory.memories")}>
          <span className="tabular-nums">{s ? s.memories.toLocaleString() : "–"}</span>
        </Fact>
        <Fact label={t("agents.memory.lastSaved")}>{!s ? "–" : s.lastActivity ? ago(s.lastActivity) : t("agents.memory.never")}</Fact>
        <Fact label={t("agents.memory.connector")}>
          <span className="inline-flex min-w-0 flex-wrap items-center justify-end gap-x-2 gap-y-1">
            <Link to="/connectors/$id" params={{ id: data.connector.id }} className="underline-offset-4 hover:underline">
              {data.connector.label}
            </Link>
            <ConnectorStatusBadge status={data.connector.enabled ? data.connector.status : "paused"} />
          </span>
        </Fact>
      </dl>
    </div>
  )
}

function ToolsTab({ agent }: { agent: Agent }) {
  const t = useT()
  const [query, setQuery] = React.useState("")
  const { data, error } = useTabData(() => getAgentToolsFn({ data: { id: agent.id } }), agent.id)
  // Owners changed here, on top of what the tab loaded. A changed server
  // moves to the other part of the list.
  const [owners, setOwners] = React.useState<Record<string, string | null>>({})
  const ownerOf = (id: string) => (id in owners ? owners[id]! : (data?.scopes[id] ?? null))

  // Global (every agent) or owned by this agent. Saves at once.
  const setScope = async (connectorId: string, scope: string) => {
    if (scope !== "all" && scope !== "only") return
    const before = ownerOf(connectorId)
    const next = scope === "all" ? null : agent.id
    setOwners((o) => ({ ...o, [connectorId]: next }))
    try {
      await setConnectorOwnerFn({ data: { id: connectorId, agentId: next } })
    } catch (err) {
      setOwners((o) => ({ ...o, [connectorId]: before }))
      toast.error(errorMessage(err))
    }
  }
  // Tool permissions save at once, like the scope. Rolled back when the save fails.
  const router = useRouter()
  const [permissions, setPermissions] = React.useState(agent.toolPermissions)
  const savePermissions = async (patch: { allowAll?: boolean; tools?: Record<string, Permission | null> }, next: typeof permissions) => {
    const before = permissions
    setPermissions(next)
    try {
      const r = await setAgentToolPermissionsFn({ data: { id: agent.id, ...patch } })
      setPermissions(r.toolPermissions)
      void router.invalidate()
    } catch (err) {
      setPermissions(before)
      toast.error(errorMessage(err))
    }
  }
  const setAllowAll = (allowAll: boolean) => void savePermissions({ allowAll }, { ...permissions, allowAll })
  const setPermission = (name: string, p: Permission | null) => {
    const tools = { ...permissions.tools }
    if (p) tools[name] = p
    else delete tools[name]
    void savePermissions({ tools: { [name]: p } }, { ...permissions, tools })
  }

  const tools = data?.tools ?? null
  const groups = React.useMemo(() => groupTools(tools ?? [], query), [tools, query])

  const youRule = permissions.allowAll ? t("agents.tools.youAllowAll") : t("agents.tools.youRule")
  // A group or someone else's DM, Palui's groups included: one tool set for
  // everyone there, never asking first.
  const sharedCount = tools?.filter((x) => inSharedChats(x, agent.guestTools, permissions)).length ?? null
  const sharedRule = agent.guestTools === "none" ? t("agents.tools.none") : sharedCount === null ? t("agents.tools.sharedRuleLoading") : t("agents.tools.sharedRule", { n: sharedCount.toLocaleString() })
  // A DM with a member, or a WhatsApp group of only you and members.
  const memberCount = tools?.filter((x) => inMemberChats(x, permissions)).length ?? null
  const membersRule = memberCount === null ? t("agents.tools.membersRuleLoading") : t("agents.tools.membersRule", { n: memberCount.toLocaleString() })

  return (
    <>
      <Field orientation="horizontal" className="max-w-2xl">
        <FieldContent>
          <FieldLabel htmlFor="agent-allow-all">{t("agents.perm.allowAll")}</FieldLabel>
          <FieldDescription>{t("agents.perm.allowAllHint")}</FieldDescription>
        </FieldContent>
        <Switch id="agent-allow-all" checked={permissions.allowAll} onCheckedChange={(v) => setAllowAll(v)} />
      </Field>

      <ItemGroup className="gap-1">
        <Rule icon={<UserIcon />} label={t("agents.tools.you")} value={youRule} />
        {!agent.isDefault && <Rule icon={<ContactIcon />} label={t("agents.tools.members")} value={membersRule} />}
        <Rule icon={<UsersIcon />} label={t("agents.tools.shared")} value={sharedRule} />
      </ItemGroup>

      {tools && tools.length > 12 && (
        <InputGroup className="max-w-sm min-w-0">
          <InputGroupAddon>
            <SearchIcon aria-hidden="true" />
          </InputGroupAddon>
          <InputGroupInput type="search" aria-label={t("agents.tools.search")} placeholder={t("agents.tools.search")} value={query} onChange={(e) => setQuery(e.target.value)} />
        </InputGroup>
      )}

      {error && <LoadError message={error} />}
      {data?.error && <LoadError message={data.error} />}
      {!data && !error && (
        <div className="flex flex-col gap-3" aria-busy="true">
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      )}
      {tools && tools.length === 0 && (
        <Empty className="border border-dashed p-6">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <WrenchIcon />
            </EmptyMedia>
            <EmptyTitle>{t("agents.tools.empty")}</EmptyTitle>
            <EmptyDescription>{t("agents.tools.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row flex-wrap justify-center">
            <Button variant="outline" size="sm" render={<Link to="/connectors/new" search={{ agent: agent.id }} />}>
              <PlusIcon data-icon="inline-start" />
              {t("agents.tools.addMcp")}
            </Button>
            <Button variant="ghost" size="sm" render={<Link to="/connectors" />}>
              {t("nav.connectors")}
            </Button>
          </EmptyContent>
        </Empty>
      )}
      {tools && tools.length > 0 && groups.length === 0 && <p className="py-6 text-center text-sm text-muted-foreground">{t("agents.tools.searchEmpty")}</p>}
      {data && tools && groups.length > 0 && (
        <ToolsByScope agent={agent} data={data} tools={tools} groups={groups} query={query} ownerOf={ownerOf} setScope={(id, scope) => void setScope(id, scope)} permissions={permissions} setPermission={setPermission} />
      )}
    </>
  )
}

function Rule({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <Item size="sm" className="px-0">
      <ItemMedia variant="icon">{icon}</ItemMedia>
      <ItemContent>
        <ItemTitle>{label}</ItemTitle>
        <ItemDescription className="line-clamp-none">{value}</ItemDescription>
      </ItemContent>
    </Item>
  )
}

function AgentPageSkeleton() {
  return (
    <PagePending page="nav.agents">
      <div className="flex w-full max-w-3xl flex-col gap-6">
        <Skeleton className="h-7 w-24" />
        <div className="flex items-center gap-3">
          <Skeleton className="size-10 rounded-lg" />
          <div className="flex flex-col gap-2">
            <Skeleton className="h-7 w-48" />
            <Skeleton className="h-4 w-24" />
          </div>
        </div>
        <Skeleton className="h-10" />
        <div className="flex flex-col gap-4">
          <Skeleton className="h-40" />
          <Skeleton className="h-24" />
          <Skeleton className="h-24" />
        </div>
      </div>
    </PagePending>
  )
}

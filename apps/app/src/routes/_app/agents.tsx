// Agents (PLAN 19.7): Palui plus any number of named personas. Each one has
// its own persona, instructions, memory and daily cap, and the non-default
// ones can answer other people in the chats they're given.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { CheckIcon, ImageUpIcon, PencilIcon, PlusIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { AgentAvatar, AVATAR_COLORS, AVATAR_ICONS, resizeAvatar } from "@/components/agent-avatar"
import { AppError } from "@/components/app-error"
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
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Textarea } from "@/components/ui/textarea"
import { deleteAgentFn, getAgentsPage, saveAgentFn } from "@/lib/fns/agents"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/agents")({
  loader: () => getAgentsPage(),
  head: () => ({ meta: [{ title: "Agents · Palui" }] }),
  component: AgentsPage,
  pendingComponent: AgentsSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getAgentsPage>>
type Agent = Data["agents"][number]
type SaveData = Parameters<typeof saveAgentFn>[0]["data"]

const NAME_MAX = 40
const PERSONA_MAX = 8000
const SLUG_RE = /^[a-z][a-z0-9-]{1,23}$/
const CONTAINER_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/

function AgentsPage() {
  const data = Route.useLoaderData()
  const t = useT()
  // `editing` stays set while the sheet closes, so it keeps its content through the exit animation.
  const [editing, setEditingTarget] = React.useState<Agent | "new" | null>(null)
  const [sheetOpen, setSheetOpen] = React.useState(false)
  const setEditing = (next: Agent | "new") => {
    setEditingTarget(next)
    setSheetOpen(true)
  }

  return (
    <>
      <PageHeader
        title={t("agents.title")}
        description={t("agents.description")}
        actions={
          <Button onClick={() => setEditing("new")}>
            <PlusIcon data-icon="inline-start" />
            {t("agents.new")}
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>{t("agents.list")}</CardTitle>
          <CardDescription>{t("agents.listBody")}</CardDescription>
        </CardHeader>
        <CardContent>
          <ul className="flex flex-col divide-y">
            {data.agents.map((a) => (
              <AgentRow key={a.id} agent={a} bot={data.bots.find((b) => b.id === a.telegramConnectorId)} onEdit={() => setEditing(a)} />
            ))}
          </ul>
        </CardContent>
      </Card>
      <AgentSheet key={editing === "new" ? "new" : (editing?.id ?? "none")} open={sheetOpen} target={editing} bots={data.bots} agents={data.agents} onClose={() => setSheetOpen(false)} />
    </>
  )
}

function AgentRow({ agent, bot, onEdit }: { agent: Agent; bot?: Data["bots"][number]; onEdit: () => void }) {
  const t = useT()
  const tokens = agent.tokensToday.toLocaleString()
  const usage = agent.dailyTokenCap > 0 ? t("agents.tokensOfCap", { n: tokens, cap: agent.dailyTokenCap.toLocaleString() }) : t("agents.tokens", { n: tokens })
  const over = agent.dailyTokenCap > 0 && agent.tokensToday >= agent.dailyTokenCap
  return (
    <li className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
      <AgentAvatar agent={agent} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span dir="auto" className="min-w-0 text-left font-medium [overflow-wrap:anywhere]">
            {agent.name}
          </span>
          <span className="font-mono text-caption text-muted-foreground">{agent.slug}</span>
          {agent.isDefault && <StatusText tone="secondary">{t("agents.default")}</StatusText>}
          {over && <StatusText tone="warning">{t("agents.capHit")}</StatusText>}
        </span>
        <p dir="auto" className="line-clamp-2 text-left text-sm text-muted-foreground [overflow-wrap:anywhere]">
          {agent.persona}
        </p>
        <span className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-caption text-muted-foreground tabular-nums">
          <span>{agent.isDefault ? t("agents.privateOnly") : t("agents.chats", { n: agent.chats.toLocaleString() })}</span>
          <span>{usage}</span>
          {bot && <span className="[overflow-wrap:anywhere]">{bot.username ? `@${bot.username}` : bot.label}</span>}
        </span>
      </div>
      <Button variant="ghost" size="icon-sm" className="shrink-0" onClick={onEdit} aria-label={t("agents.edit", { name: agent.name })}>
        <PencilIcon aria-hidden="true" />
      </Button>
    </li>
  )
}

function AgentSheet({ open, target, bots, agents, onClose }: { open: boolean; target: Agent | "new" | null; bots: Data["bots"]; agents: Agent[]; onClose: () => void }) {
  const t = useT()
  const router = useRouter()
  const item = target && target !== "new" ? target : null
  const [name, setName] = React.useState(item?.name ?? "")
  const [slug, setSlug] = React.useState(item?.slug ?? "")
  const [persona, setPersona] = React.useState(item?.persona ?? "")
  const [ownerPowers, setOwnerPowers] = React.useState<"off" | "approve">(item?.ownerPowers ?? "off")
  const [guestTools, setGuestTools] = React.useState<"none" | "safe">(item?.guestTools ?? "safe")
  const [cap, setCap] = React.useState(String(item?.dailyTokenCap ?? 0))
  const [container, setContainer] = React.useState(item?.recallContainer ?? "")
  const [bot, setBot] = React.useState(item?.telegramConnectorId ?? "")
  const [avatarImage, setAvatarImage] = React.useState<string | null>(item?.avatarImage ?? null)
  const [avatarIcon, setAvatarIcon] = React.useState<string | null>(item?.avatarIcon ?? null)
  const [avatarColor, setAvatarColor] = React.useState<string | null>(item?.avatarColor ?? null)
  const [errors, setErrors] = React.useState<Partial<Record<"name" | "slug" | "persona" | "cap" | "container", string>>>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete">(null)
  const isDefault = !!item?.isDefault
  const defaultContainer = isDefault ? t("agents.containerConnector") : `agent-${slug || "slug"}`
  // A bot that belongs to another agent isn't offered.
  const taken = new Set(agents.filter((a) => a.id !== item?.id && a.telegramConnectorId).map((a) => a.telegramConnectorId))
  const botOptions = [{ value: "", label: t("agents.botNone") }, ...bots.filter((b) => !taken.has(b.id)).map((b) => ({ value: b.id, label: b.username ? `@${b.username}` : b.label }))]

  const submit = async () => {
    const next: typeof errors = {}
    if (!name.trim()) next.name = t("agents.err.name")
    if (!item && !SLUG_RE.test(slug.trim())) next.slug = t("agents.err.slug")
    if (!persona.trim()) next.persona = t("agents.err.persona")
    const capN = Number(cap.replace(/[.,\s]/g, ""))
    if (!Number.isInteger(capN) || capN < 0) next.cap = t("agents.err.cap")
    if (container.trim() && !CONTAINER_RE.test(container.trim())) next.container = t("agents.err.container")
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy("save")
    try {
      await saveAgentFn({
        data: {
          id: item?.id,
          slug: item ? undefined : slug.trim(),
          name: name.trim(),
          persona: persona.trim(),
          ownerPowers,
          guestTools,
          dailyTokenCap: capN,
          recallContainer: container.trim() || null,
          telegramConnectorId: bot || null,
          avatarImage,
          avatarIcon: (avatarIcon || null) as SaveData["avatarIcon"],
          avatarColor: (avatarColor || null) as SaveData["avatarColor"],
        },
      })
      toast.success(t("agents.saved", { name: name.trim() }))
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
      await deleteAgentFn({ data: { id: item.id } })
      toast.success(t("agents.deleted", { name: item.name }))
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
      title={item ? item.name : t("agents.new")}
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
        <AvatarPicker
          agent={{ isDefault, avatarImage, avatarIcon, avatarColor }}
          onImage={setAvatarImage}
          onIcon={setAvatarIcon}
          onColor={setAvatarColor}
        />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field data-invalid={!!errors.name}>
            <FieldLabel htmlFor="agent-name">{t("agents.field.name")}</FieldLabel>
            <Input id="agent-name" value={name} maxLength={NAME_MAX} aria-invalid={!!errors.name} onChange={(e) => setName(e.target.value)} />
            {errors.name && <FieldError>{errors.name}</FieldError>}
          </Field>
          <Field data-invalid={!!errors.slug}>
            <FieldLabel htmlFor="agent-slug">{t("agents.field.slug")}</FieldLabel>
            <Input
              id="agent-slug"
              className="font-mono"
              spellCheck={false}
              autoCapitalize="none"
              autoComplete="off"
              placeholder="chef"
              maxLength={24}
              disabled={!!item}
              value={slug}
              aria-invalid={!!errors.slug}
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
            />
            {errors.slug ? <FieldError>{errors.slug}</FieldError> : <FieldDescription>{t("agents.slugHint", { slug: slug || "chef" })}</FieldDescription>}
          </Field>
        </div>
        <Field data-invalid={!!errors.persona}>
          <FieldLabel htmlFor="agent-persona">{t("agents.field.persona")}</FieldLabel>
          {/* It grows with its text up to half the screen, then scrolls, so Save stays in reach. */}
          <Textarea id="agent-persona" rows={8} maxLength={PERSONA_MAX} className="max-h-[50svh]" value={persona} aria-invalid={!!errors.persona} onChange={(e) => setPersona(e.target.value)} />
          <FieldDescription className="tabular-nums">{t("notes.chars", { n: persona.length.toLocaleString(), max: PERSONA_MAX.toLocaleString() })}</FieldDescription>
          {errors.persona && <FieldError>{errors.persona}</FieldError>}
        </Field>
        {isDefault ? (
          <p className="text-sm text-muted-foreground">{t("agents.defaultNote")}</p>
        ) : (
          <FieldSet>
            <FieldLegend variant="label">{t("agents.shared")}</FieldLegend>
            <FieldDescription>{t("agents.sharedBody")}</FieldDescription>
            <Field>
              <FieldLabel htmlFor="agent-guests">{t("agents.field.guestTools")}</FieldLabel>
              <SimpleSelect
                id="agent-guests"
                value={guestTools}
                onChange={(v) => setGuestTools(v as "none" | "safe")}
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
                value={ownerPowers}
                onChange={(v) => setOwnerPowers(v as "off" | "approve")}
                options={[
                  { value: "off", label: t("agents.ownerPowers.off") },
                  { value: "approve", label: t("agents.ownerPowers.approve") },
                ]}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="agent-bot">{t("agents.field.bot")}</FieldLabel>
              <SimpleSelect id="agent-bot" value={bot} onChange={setBot} options={botOptions} />
              <FieldDescription>
                {t("agents.botHint")}{" "}
                <Link to="/connectors/new" className="underline underline-offset-4">
                  {t("agents.botAdd")}
                </Link>
              </FieldDescription>
            </Field>
          </FieldSet>
        )}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field data-invalid={!!errors.cap}>
            <FieldLabel htmlFor="agent-cap">{t("agents.field.cap")}</FieldLabel>
            <Input id="agent-cap" inputMode="numeric" className="tabular-nums" value={cap} aria-invalid={!!errors.cap} onChange={(e) => setCap(e.target.value)} />
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
              placeholder={defaultContainer}
              value={container}
              aria-invalid={!!errors.container}
              onChange={(e) => setContainer(e.target.value)}
            />
            {errors.container ? <FieldError>{errors.container}</FieldError> : <FieldDescription>{t("agents.containerHint")}</FieldDescription>}
          </Field>
        </div>
        {item && (
          <p className="text-sm text-muted-foreground">
            {t("agents.instructions", { n: item.instructions.toLocaleString() })}{" "}
            <Link to="/notes" search={{ agent: item.id }} className="underline underline-offset-4">
              {t("agents.instructionsLink")}
            </Link>
          </p>
        )}
      </FieldGroup>
      {item && !isDefault && (
        <AlertDialog>
          <AlertDialogTrigger render={<Button variant="destructive" className="self-start" disabled={busy !== null} />}>
            {busy === "delete" && <Spinner data-icon="inline-start" />}
            {t("agents.delete")}
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle className="[overflow-wrap:anywhere]">{t("agents.deleteTitle", { name: item.name })}</AlertDialogTitle>
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
      )}
    </ResponsiveSheet>
  )
}

// A photo, or an icon on a color (PLAN 19.7). A photo wins while it's set.
function AvatarPicker({
  agent,
  onImage,
  onIcon,
  onColor,
}: {
  agent: { isDefault: boolean; avatarImage: string | null; avatarIcon: string | null; avatarColor: string | null }
  onImage: (v: string | null) => void
  onIcon: (v: string | null) => void
  onColor: (v: string | null) => void
}) {
  const t = useT()
  const fileRef = React.useRef<HTMLInputElement>(null)
  const [busy, setBusy] = React.useState(false)
  const pickFile = async (file: File | undefined) => {
    if (!file) return
    if (!/^image\//.test(file.type)) {
      toast.error(t("agents.avatar.notImage"))
      return
    }
    setBusy(true)
    try {
      onImage(await resizeAvatar(file))
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
      if (fileRef.current) fileRef.current.value = ""
    }
  }
  const color = agent.avatarColor ?? "zinc"
  return (
    <FieldSet>
      <FieldLegend variant="label">{t("agents.avatar")}</FieldLegend>
      <div className="flex items-center gap-3">
        <AgentAvatar agent={agent} size="lg" />
        <div className="flex flex-wrap gap-2">
          <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp,image/heic" className="sr-only" tabIndex={-1} onChange={(e) => void pickFile(e.target.files?.[0])} />
          <Button variant="outline" size="sm" disabled={busy} onClick={() => fileRef.current?.click()}>
            {busy ? <Spinner data-icon="inline-start" /> : <ImageUpIcon data-icon="inline-start" />}
            {agent.avatarImage ? t("agents.avatar.change") : t("agents.avatar.upload")}
          </Button>
          {agent.avatarImage && (
            <Button variant="ghost" size="sm" onClick={() => onImage(null)}>
              <XIcon data-icon="inline-start" />
              {t("agents.avatar.remove")}
            </Button>
          )}
        </div>
      </div>
      {agent.avatarImage && <FieldDescription>{t("agents.avatar.photoWins")}</FieldDescription>}
      <div role="radiogroup" aria-label={t("agents.avatar.icon")} className="grid grid-cols-8 gap-1.5">
        {Object.entries(AVATAR_ICONS).map(([name, Icon]) => {
          const on = (agent.avatarIcon ?? (agent.isDefault ? null : "bot")) === name
          return (
            <button
              key={name}
              type="button"
              role="radio"
              aria-checked={on}
              aria-label={name.replace(/-/g, " ")}
              data-avatar-tone={on ? color : "zinc"}
              onClick={() => onIcon(on && agent.isDefault ? null : name)}
              className="flex aspect-square items-center justify-center rounded-md bg-(--avatar-bg) text-(--avatar-fg) outline-offset-2 ring-foreground transition-[box-shadow] focus-visible:outline-2 focus-visible:outline-focus-ring aria-checked:ring-2 [&_svg]:size-4"
            >
              <Icon aria-hidden="true" />
            </button>
          )
        })}
      </div>
      <div role="radiogroup" aria-label={t("agents.avatar.color")} className="flex flex-wrap gap-1.5">
        {AVATAR_COLORS.map((c) => {
          const on = color === c
          return (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={on}
              aria-label={c}
              data-avatar-tone={c}
              onClick={() => onColor(c === "zinc" ? null : c)}
              className="flex size-8 items-center justify-center rounded-full border border-border bg-(--avatar-bg) text-(--avatar-fg) outline-offset-2 ring-foreground focus-visible:outline-2 focus-visible:outline-focus-ring aria-checked:ring-2 [&_svg]:size-4"
            >
              {on && <CheckIcon aria-hidden="true" />}
            </button>
          )
        })}
      </div>
    </FieldSet>
  )
}

function AgentsSkeleton() {
  return (
    <PagePending page="nav.agents">
      <PageHeaderSkeleton title="w-24" description="w-96" action="w-32" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </PagePending>
  )
}


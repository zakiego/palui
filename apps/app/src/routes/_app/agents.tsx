// Agents (PLAN 19.7): Palui plus any number of named personas. Each one has
// its own persona, instructions, memory and daily cap, and the non-default
// ones can answer other people in the chats they're given. A row opens the
// agent's page (/agents/<slug>); New agent asks for the basics, then opens it.
import * as React from "react"
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import { ChevronRightIcon, PlusIcon } from "lucide-react"
import { toast } from "sonner"

import { AgentAvatar } from "@/components/agent-avatar"
import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending } from "@/components/page-pending"
import { ResponsiveSheet } from "@/components/responsive-sheet"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Textarea } from "@/components/ui/textarea"
import { getAgentsPage, saveAgentFn } from "@/lib/fns/agents"
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

const NAME_MAX = 40
const PERSONA_MAX = 8000
const SLUG_RE = /^[a-z][a-z0-9-]{1,23}$/

function AgentsPage() {
  const data = Route.useLoaderData()
  const t = useT()
  const [creating, setCreating] = React.useState(false)
  // Remounted on every open, so a cancelled draft doesn't come back.
  const [draftKey, setDraftKey] = React.useState(0)

  return (
    <>
      <PageHeader
        title={t("agents.title")}
        description={t("agents.description")}
        actions={
          <Button
            onClick={() => {
              setDraftKey((k) => k + 1)
              setCreating(true)
            }}
          >
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
          <ul className="-mx-2 flex flex-col">
            {data.agents.map((a) => (
              <AgentRow key={a.id} agent={a} bot={data.bots.find((b) => b.id === a.telegramConnectorId)} />
            ))}
          </ul>
        </CardContent>
      </Card>
      <NewAgentSheet key={draftKey} open={creating} onClose={() => setCreating(false)} />
    </>
  )
}

function AgentRow({ agent, bot }: { agent: Agent; bot?: Data["bots"][number] }) {
  const t = useT()
  const tokens = agent.tokensToday.toLocaleString()
  const usage = agent.dailyTokenCap > 0 ? t("agents.tokensOfCap", { n: tokens, cap: agent.dailyTokenCap.toLocaleString() }) : t("agents.tokens", { n: tokens })
  const over = agent.dailyTokenCap > 0 && agent.tokensToday >= agent.dailyTokenCap
  return (
    <li className="border-b last:border-b-0">
      <Link
        to="/agents/$slug"
        params={{ slug: agent.slug }}
        className="group flex items-start gap-3 rounded-lg px-2 py-3 outline-offset-2 transition-colors hover:bg-muted/50 focus-visible:outline-2 focus-visible:outline-focus-ring"
      >
        <AgentAvatar agent={agent} />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span dir="auto" className="min-w-0 text-left font-medium [overflow-wrap:anywhere]">
              {agent.name}
            </span>
            <span className="font-mono text-caption break-all text-muted-foreground">{agent.slug}</span>
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
        <ChevronRightIcon aria-hidden="true" className="mt-2.5 size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 rtl:rotate-180" />
      </Link>
    </li>
  )
}

// Name, slug and persona. Everything else has a default and lives on the agent's page.
function NewAgentSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useT()
  const router = useRouter()
  const navigate = useNavigate()
  const [name, setName] = React.useState("")
  const [slug, setSlug] = React.useState("")
  const [persona, setPersona] = React.useState("")
  const [errors, setErrors] = React.useState<Partial<Record<"name" | "slug" | "persona", string>>>({})
  const [busy, setBusy] = React.useState(false)

  const submit = async () => {
    const next: typeof errors = {}
    if (!name.trim()) next.name = t("agents.err.name")
    if (!SLUG_RE.test(slug.trim())) next.slug = t("agents.err.slug")
    if (!persona.trim()) next.persona = t("agents.err.persona")
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy(true)
    try {
      const row = await saveAgentFn({
        data: {
          slug: slug.trim(),
          name: name.trim(),
          persona: persona.trim(),
          ownerPowers: "off",
          guestTools: "safe",
          dailyTokenCap: 0,
          recallContainer: null,
          recallReadContainers: [],
          // New agents keep each chat's memory apart; the Memory tab can share it.
          recallPerChat: true,
          telegramConnectorId: null,
          avatarImage: null,
          avatarIcon: null,
          avatarColor: null,
        },
      })
      toast.success(t("agents.created", { name: name.trim() }))
      onClose()
      void router.invalidate()
      void navigate({ to: "/agents/$slug", params: { slug: row.slug } })
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <ResponsiveSheet
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title={t("agents.new")}
      description={t("agents.newBody")}
      footer={
        <div className="flex w-full gap-2">
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button className="ms-auto" disabled={busy} onClick={() => void submit()}>
            {busy && <Spinner data-icon="inline-start" />}
            {t("agents.create")}
          </Button>
        </div>
      }
    >
      <FieldGroup>
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
              value={slug}
              aria-invalid={!!errors.slug}
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
            />
            {errors.slug ? <FieldError>{errors.slug}</FieldError> : <FieldDescription>{t("agents.slugHint", { slug: slug || "chef" })}</FieldDescription>}
          </Field>
        </div>
        <Field data-invalid={!!errors.persona}>
          <FieldLabel htmlFor="agent-persona">{t("agents.field.persona")}</FieldLabel>
          {/* It grows with its text up to half the screen, then scrolls, so Create stays in reach. */}
          <Textarea id="agent-persona" rows={8} maxLength={PERSONA_MAX} className="max-h-[50svh]" value={persona} aria-invalid={!!errors.persona} onChange={(e) => setPersona(e.target.value)} />
          <FieldDescription className="tabular-nums">{t("notes.chars", { n: persona.length.toLocaleString(), max: PERSONA_MAX.toLocaleString() })}</FieldDescription>
          {errors.persona && <FieldError>{errors.persona}</FieldError>}
        </Field>
      </FieldGroup>
    </ResponsiveSheet>
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

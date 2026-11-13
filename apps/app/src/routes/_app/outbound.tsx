// Outbound (docs/design/outbound-messages.md): messages Palui sends to other
// people for the owner. The kill switch and limits up top, then Contacts (with
// each one's approval setting), Scheduled, and the Log of every send.
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { BotIcon, EllipsisIcon, RepeatIcon, SendIcon, SlidersHorizontalIcon, UserPlusIcon, UsersIcon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { ListCapNote } from "@/components/list-cap-note"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, TableSkeleton, TabsSkeleton } from "@/components/page-pending"
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
} from "@/components/ui/alert-dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { StatusText, type StatusTone } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { addContactFn, cancelOutboundFn, getOutbound, LOG_LIMIT, removeContactFn, saveOutboundLimits, setContactPolicy, setOutboundSwitch } from "@/lib/fns/outbound"
import { clip, errorMessage, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/outbound")({
  validateSearch: z.object({ tab: z.enum(["contacts", "scheduled", "log"]).optional(), add: z.boolean().optional(), agent: z.string().max(100).optional() }),
  loader: () => getOutbound(),
  head: () => ({ meta: [{ title: "Outbound · Palui" }] }),
  component: OutboundPage,
  pendingComponent: OutboundSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getOutbound>>

// Each agent has its own contacts (PLAN 19). With more than one agent the page
// filters by agent and names the agent on every row.
function scoped(data: Data, agent: string | undefined): Data {
  if (!agent) return data
  return {
    ...data,
    contacts: data.contacts.filter((c) => c.agentId === agent),
    scheduled: data.scheduled.filter((r) => r.agentId === agent),
    log: data.log.filter((r) => r.agentId === agent),
  }
}

function useAgentName(data: Data, agent: string | undefined) {
  const names = new Map(data.agents.map((a) => [a.id, a.name]))
  const show = !agent && data.agents.length > 1
  return (id: string | null) => (show && id ? (names.get(id) ?? null) : null)
}

function AgentTag({ name }: { name: string | null }) {
  if (!name) return null
  return (
    <Badge variant="soft" className="max-w-40">
      <BotIcon aria-hidden="true" className="shrink-0" />
      <span className="truncate">{name}</span>
    </Badge>
  )
}

function OutboundPage() {
  const all = Route.useLoaderData()
  const search = Route.useSearch()
  const agent = search.agent && all.agents.some((a) => a.id === search.agent) ? search.agent : undefined
  const data = scoped(all, agent)
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  const tab = search.tab ?? "contacts"
  const [limitsOpen, setLimitsOpen] = React.useState(false)
  const setAdd = (open: boolean) => void navigate({ search: (p) => ({ ...p, add: open || undefined }), replace: true })

  return (
    <>
      <PageHeader
        title={t("outbound.title")}
        description={t("outbound.description")}
        actions={
          <>
            <Button variant="outline" onClick={() => setLimitsOpen(true)}>
              <SlidersHorizontalIcon data-icon="inline-start" />
              {t("outbound.limits")}
            </Button>
            <Button onClick={() => setAdd(true)}>
              <UserPlusIcon data-icon="inline-start" />
              {t("outbound.addContact")}
            </Button>
          </>
        }
      />
      <div className="flex flex-col gap-8">
        <KillSwitch data={data} />
        <Tabs value={tab} onValueChange={(v) => void navigate({ search: (p) => ({ ...p, tab: v as typeof tab }), replace: true })} className="gap-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="contacts">
              {t("outbound.contacts")} <span className="font-mono text-muted-foreground tabular-nums">{data.contacts.length.toLocaleString("en")}</span>
            </TabsTrigger>
            <TabsTrigger value="scheduled">
              {t("outbound.scheduled")} <span className="font-mono text-muted-foreground tabular-nums">{data.scheduled.length.toLocaleString("en")}</span>
            </TabsTrigger>
            <TabsTrigger value="log">{t("outbound.log")}</TabsTrigger>
          </TabsList>
          {all.agents.length > 1 && (
            <SimpleSelect
              size="sm"
              className="w-48"
              aria-label={t("outbound.agent")}
              value={agent ?? "all"}
              options={[{ value: "all", label: t("outbound.allAgents") }, ...all.agents.map((a) => ({ value: a.id, label: a.name }))]}
              onChange={(v) => void navigate({ search: (p) => ({ ...p, agent: v === "all" ? undefined : v }), replace: true })}
            />
          )}
          </div>
          <TabsContent value="contacts">
            <ContactsTable data={data} agent={agent} />
          </TabsContent>
          <TabsContent value="scheduled">
            <ScheduledTable data={data} agent={agent} />
          </TabsContent>
          <TabsContent value="log">
            <LogTable data={data} agent={agent} />
          </TabsContent>
        </Tabs>
      </div>
      <AddContactDialog open={!!search.add} onOpenChange={setAdd} data={all} agent={agent} />
      <LimitsDialog open={limitsOpen} onOpenChange={setLimitsOpen} data={data} />
    </>
  )
}

function KillSwitch({ data }: { data: Data }) {
  const t = useT()
  const router = useRouter()
  const [enabled, setEnabled] = React.useState(data.settings.enabled)
  React.useEffect(() => setEnabled(data.settings.enabled), [data.settings.enabled])
  const toggle = async (v: boolean) => {
    setEnabled(v)
    try {
      const r = await setOutboundSwitch({ data: { enabled: v } })
      toast.success(v ? t("outbound.turnedOn") : t("outbound.turnedOff", { n: String(r.cancelled) }))
      void router.invalidate()
    } catch (err) {
      setEnabled(!v)
      toast.error(errorMessage(err))
    }
  }
  return (
    <Card className="flex-row flex-wrap items-center justify-between gap-x-6 gap-y-4 px-5 py-4">
      <div className="flex min-w-0 flex-col gap-1">
        <p className="text-sm font-medium">{t("outbound.switch")}</p>
        <p className="text-caption text-pretty text-muted-foreground">
          {enabled ? t("outbound.switchOn") : t("outbound.switchOff")}
        </p>
        <p className="font-mono text-caption text-muted-foreground tabular-nums">
          {t("outbound.usage", { n: data.usage.toLocaleString("en"), max: data.settings.perDay.toLocaleString("en"), per: data.settings.perContactPerDay.toLocaleString("en") })}
        </p>
      </div>
      <div className="flex items-center gap-3">
        <StatusText tone={enabled ? "success" : "destructive"}>{enabled ? t("outbound.on") : t("outbound.off")}</StatusText>
        <Switch checked={enabled} onCheckedChange={(v) => void toggle(v)} aria-label={t("outbound.switch")} />
      </div>
    </Card>
  )
}

function ContactsTable({ data, agent }: { data: Data; agent: string | undefined }) {
  const t = useT()
  const agentName = useAgentName(data, agent)
  if (!data.contacts.length) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <UsersIcon />
          </EmptyMedia>
          <EmptyTitle>{t("outbound.noContacts")}</EmptyTitle>
          <EmptyDescription>{t("outbound.noContactsHint")}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("outbound.contact")}</TableHead>
            <TableHead className="w-48 max-sm:hidden">{t("outbound.approval")}</TableHead>
            <TableHead className="w-28 max-sm:hidden">{t("outbound.today")}</TableHead>
            <TableHead className="w-12">
              <span className="sr-only">{t("table.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.contacts.map((c) => (
            <ContactRow key={c.id} contact={c} max={data.settings.perContactPerDay} agentName={agentName(c.agentId)} />
          ))}
        </TableBody>
      </Table>
    </Card>
  )
}

function ContactRow({ contact: c, max, agentName }: { contact: Data["contacts"][number]; max: number; agentName: string | null }) {
  const t = useT()
  const router = useRouter()
  const [policy, setPolicy] = React.useState(c.policy)
  const [confirm, setConfirm] = React.useState<"remove" | "allow" | null>(null)
  React.useEffect(() => setPolicy(c.policy), [c.policy])
  const save = async (next: "ask" | "allow") => {
    const prev = policy
    setPolicy(next)
    try {
      await setContactPolicy({ data: { id: c.id, policy: next } })
      toast.success(next === "allow" ? t("outbound.nowAllow", { name: c.name }) : t("outbound.nowAsk", { name: c.name }))
      void router.invalidate()
    } catch (err) {
      setPolicy(prev)
      toast.error(errorMessage(err))
    }
  }
  const remove = async () => {
    try {
      const r = await removeContactFn({ data: { id: c.id } })
      toast.success(t("outbound.removed", { name: c.name, n: String(r.cancelled) }))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  const policySelect = (
    <SimpleSelect
      size="sm"
      className="w-full"
      aria-label={t("outbound.approvalFor", { name: c.name })}
      value={policy}
      options={[
        { value: "ask", label: t("outbound.ask") },
        { value: "allow", label: t("outbound.allow") },
      ]}
      onChange={(v) => (v === "allow" ? setConfirm("allow") : void save("ask"))}
    />
  )
  return (
    <TableRow>
      <TableCell className="max-w-0">
        <span dir="auto" className="block truncate font-medium" title={c.name}>
          {c.name}
        </span>
        <span className="mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-caption text-muted-foreground">
          <AgentTag name={agentName} />
          <Badge variant="outline">{c.channel === "whatsapp" ? "WhatsApp" : "Telegram"}</Badge>
          <span className="truncate font-mono tabular-nums">{c.address}</span>
          <span className="tabular-nums sm:hidden">
            {t("outbound.today")}: <span className="font-mono">{c.sentToday}/{max}</span>
          </span>
        </span>
        {c.note && <span className="mt-1 block truncate text-caption text-muted-foreground">{c.note}</span>}
        {/* The approval column is gone below sm; the select sits under the name instead. */}
        <div className="mt-2 sm:hidden">{policySelect}</div>
      </TableCell>
      <TableCell className="max-sm:hidden">{policySelect}</TableCell>
      <TableCell className="font-mono text-caption tabular-nums max-sm:hidden">
        {c.sentToday}/{max}
      </TableCell>
      <TableCell>
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("outbound.actionsFor", { name: c.name })} />}>
            <EllipsisIcon aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-40">
            <DropdownMenuItem variant="destructive" onClick={() => setConfirm("remove")}>
              {t("outbound.remove")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <AlertDialog open={confirm !== null} onOpenChange={(o) => !o && setConfirm(null)}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{confirm === "allow" ? t("outbound.allowTitle", { name: c.name }) : t("outbound.removeTitle", { name: c.name })}</AlertDialogTitle>
              <AlertDialogDescription>{confirm === "allow" ? t("outbound.allowBody") : t("outbound.removeBody")}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
              {confirm === "allow" ? (
                <AlertDialogAction onClick={() => void save("allow")}>{t("outbound.allow")}</AlertDialogAction>
              ) : (
                <AlertDialogAction variant="danger" onClick={() => void remove()}>
                  {t("outbound.remove")}
                </AlertDialogAction>
              )}
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </TableCell>
    </TableRow>
  )
}

function ScheduledTable({ data, agent }: { data: Data; agent: string | undefined }) {
  const t = useT()
  const agentName = useAgentName(data, agent)
  const router = useRouter()
  if (!data.scheduled.length) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SendIcon />
          </EmptyMedia>
          <EmptyTitle>{t("outbound.noScheduled")}</EmptyTitle>
          <EmptyDescription>{t("outbound.noScheduledHint")}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  const cancel = async (id: string) => {
    try {
      await cancelOutboundFn({ data: { id } })
      toast.success(t("outbound.cancelled"))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("outbound.message")}</TableHead>
            <TableHead className="w-48 max-md:hidden">{t("outbound.next")}</TableHead>
            <TableHead className="w-20">
              <span className="sr-only">{t("table.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.scheduled.map((r) => (
            <TableRow key={r.id}>
              <TableCell className="max-w-0 whitespace-normal">
                <span dir="auto" className="block truncate text-sm font-medium">
                  {r.contact}
                </span>
                <ClampText text={r.text} lines={3} className="text-muted-foreground" />
                <span className="mt-1 flex flex-wrap items-center gap-1.5 text-caption text-muted-foreground">
                  <AgentTag name={agentName(r.agentId)} />
                  <span className="font-mono tabular-nums md:hidden">{inZone(r.fireAt, data.timezone)}</span>
                  {r.rrule && (
                    <Badge variant="outline" title={r.rrule}>
                      <RepeatIcon aria-hidden="true" />
                      <span className="max-w-48 truncate font-mono">{r.rrule}</span>
                    </Badge>
                  )}
                </span>
              </TableCell>
              <TableCell className="font-mono text-caption tabular-nums max-md:hidden">{inZone(r.fireAt, data.timezone)}</TableCell>
              <TableCell>
                <Button variant="ghost" size="sm" onClick={() => void cancel(r.id)} aria-label={t("outbound.cancelFor", { text: clip(r.text, 40) })}>
                  {t("common.cancel")}
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  )
}

const STATUS_TONE: Record<string, StatusTone> = { sent: "success", failed: "destructive", cancelled: "secondary", pending: "outline", sending: "outline" }

function LogTable({ data, agent }: { data: Data; agent: string | undefined }) {
  const t = useT()
  const agentName = useAgentName(data, agent)
  const now = Date.now()
  if (!data.log.length) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SendIcon />
          </EmptyMedia>
          <EmptyTitle>{t("outbound.noLog")}</EmptyTitle>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("outbound.message")}</TableHead>
            <TableHead className="w-36">{t("table.status")}</TableHead>
            <TableHead className="w-52 max-md:hidden">{t("outbound.when")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.log.map((r) => {
            const held = r.status === "pending" && r.sendAt > now
            return (
              <TableRow key={r.id}>
                <TableCell className="max-w-0 whitespace-normal">
                  <span dir="auto" className="block truncate text-sm font-medium">
                    {r.contact}
                  </span>
                  <ClampText text={r.text} lines={2} className="text-muted-foreground" />
                  <span className="mt-1 flex flex-wrap items-center gap-1.5 text-caption text-muted-foreground">
                    <AgentTag name={agentName(r.agentId)} />
                    {r.approvedBy && <span>{t(`outbound.by.${r.approvedBy}` as "outbound.by.owner")}</span>}
                    <span className="font-mono tabular-nums md:hidden">{inZone(r.sentAt ?? r.sendAt, data.timezone)}</span>
                  </span>
                  {r.error && r.status !== "sent" && <span className="mt-1 block text-caption break-words text-destructive-fg">{clip(r.error, 200)}</span>}
                </TableCell>
                <TableCell>
                  <StatusText tone={held ? "warning" : (STATUS_TONE[r.status] ?? "outline")}>{held ? t("outbound.held") : t(`outbound.status.${r.status}` as "outbound.status.sent")}</StatusText>
                </TableCell>
                <TableCell className="font-mono text-caption tabular-nums max-md:hidden">{inZone(r.sentAt ?? r.sendAt, data.timezone)}</TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      <ListCapNote shown={data.log.length} total={data.log.length >= LOG_LIMIT ? LOG_LIMIT + 1 : data.log.length} />
    </Card>
  )
}

function AddContactDialog({ open, onOpenChange, data, agent }: { open: boolean; onOpenChange: (o: boolean) => void; data: Data; agent: string | undefined }) {
  const t = useT()
  const router = useRouter()
  const [agentId, setAgentId] = React.useState(agent ?? data.defaultAgentId)
  const [name, setName] = React.useState("")
  const [source, setSource] = React.useState<"phone" | "telegram">("phone")
  const [phone, setPhone] = React.useState("")
  const [chatId, setChatId] = React.useState("")
  const [note, setNote] = React.useState("")
  const [saving, setSaving] = React.useState(false)
  const [error, setError] = React.useState<string>()

  React.useEffect(() => {
    if (!open) {
      setName("")
      setPhone("")
      setChatId("")
      setNote("")
      setError(undefined)
      setSource("phone")
    } else setAgentId(agent ?? data.defaultAgentId)
  }, [open, agent, data.defaultAgentId])

  const ok = name.trim() && (source === "phone" ? phone.trim() : chatId)
  const save = async () => {
    setSaving(true)
    setError(undefined)
    try {
      await addContactFn({ data: { agentId, name, ...(source === "phone" ? { phone } : { chatId }), note: note || undefined, policy: "ask" } })
      toast.success(t("outbound.added", { name: name.trim() }))
      onOpenChange(false)
      void router.invalidate()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("outbound.addContact")}</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void save()
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="contact-name">{t("outbound.name")}</FieldLabel>
              <Input id="contact-name" autoFocus maxLength={60} value={name} onChange={(e) => setName(e.target.value)} className="text-base md:text-sm" />
            </Field>
            {data.agents.length > 1 && (
              <Field>
                <FieldLabel htmlFor="contact-agent">{t("outbound.agent")}</FieldLabel>
                <SimpleSelect id="contact-agent" className="w-full" value={agentId} options={data.agents.map((a) => ({ value: a.id, label: a.name }))} onChange={setAgentId} />
                <FieldDescription>{t("outbound.agentHint")}</FieldDescription>
              </Field>
            )}
            {data.telegramChats.length > 0 && (
              <Field>
                <FieldLabel htmlFor="contact-source">{t("outbound.via")}</FieldLabel>
                <SimpleSelect
                  id="contact-source"
                  className="w-full"
                  value={source}
                  options={[
                    { value: "phone", label: t("outbound.viaPhone") },
                    { value: "telegram", label: t("outbound.viaTelegram") },
                  ]}
                  onChange={(v) => setSource(v as "phone" | "telegram")}
                />
              </Field>
            )}
            {source === "phone" ? (
              <Field>
                <FieldLabel htmlFor="contact-phone">{t("outbound.phone")}</FieldLabel>
                <Input id="contact-phone" inputMode="tel" maxLength={30} placeholder="0822 0000 1001" value={phone} onChange={(e) => setPhone(e.target.value)} className="font-mono text-base md:text-sm" />
                <FieldDescription>{t("outbound.phoneHint")}</FieldDescription>
              </Field>
            ) : (
              <Field>
                <FieldLabel htmlFor="contact-chat">{t("outbound.chat")}</FieldLabel>
                <SimpleSelect
                  id="contact-chat"
                  className="w-full"
                  value={chatId}
                  options={data.telegramChats.map((c) => ({ value: c.id, label: c.title || c.externalId }))}
                  onChange={setChatId}
                />
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor="contact-note">{t("outbound.note")}</FieldLabel>
              <Input id="contact-note" maxLength={200} value={note} onChange={(e) => setNote(e.target.value)} className="text-base md:text-sm" />
              <FieldDescription>{t("outbound.askByDefault")}</FieldDescription>
            </Field>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
          </FieldGroup>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!ok || saving}>
              {saving && <Spinner data-icon="inline-start" />}
              {t("outbound.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function LimitsDialog({ open, onOpenChange, data }: { open: boolean; onOpenChange: (o: boolean) => void; data: Data }) {
  const t = useT()
  const router = useRouter()
  const s = data.settings
  const [perContact, setPerContact] = React.useState(String(s.perContactPerDay))
  const [perDay, setPerDay] = React.useState(String(s.perDay))
  const [hold, setHold] = React.useState(String(s.holdSeconds))
  const [signature, setSignature] = React.useState(s.signature)
  const [saving, setSaving] = React.useState(false)
  const [error, setError] = React.useState<string>()
  React.useEffect(() => {
    if (open) {
      setPerContact(String(s.perContactPerDay))
      setPerDay(String(s.perDay))
      setHold(String(s.holdSeconds))
      setSignature(s.signature)
      setError(undefined)
    }
  }, [open, s])
  const save = async () => {
    setSaving(true)
    setError(undefined)
    try {
      await saveOutboundLimits({ data: { perContactPerDay: Number(perContact), perDay: Number(perDay), holdSeconds: Number(hold), signature } })
      toast.success(t("outbound.limitsSaved"))
      onOpenChange(false)
      void router.invalidate()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("outbound.limits")}</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void save()
          }}
        >
          <FieldGroup>
            <div className="grid grid-cols-2 gap-4">
              <Field>
                <FieldLabel htmlFor="lim-contact">{t("outbound.perContact")}</FieldLabel>
                <Input id="lim-contact" type="number" min={1} max={50} value={perContact} onChange={(e) => setPerContact(e.target.value)} className="font-mono text-base md:text-sm" />
              </Field>
              <Field>
                <FieldLabel htmlFor="lim-day">{t("outbound.perDay")}</FieldLabel>
                <Input id="lim-day" type="number" min={1} max={200} value={perDay} onChange={(e) => setPerDay(e.target.value)} className="font-mono text-base md:text-sm" />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="lim-hold">{t("outbound.hold")}</FieldLabel>
              <Input id="lim-hold" type="number" min={0} max={120} value={hold} onChange={(e) => setHold(e.target.value)} className="font-mono text-base md:text-sm" />
              <FieldDescription>{t("outbound.holdHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="lim-sig">{t("outbound.signature")}</FieldLabel>
              <Input id="lim-sig" maxLength={120} placeholder="(dikirim asisten Zaki)" value={signature} onChange={(e) => setSignature(e.target.value)} className="text-base md:text-sm" />
              <FieldDescription>{t("outbound.signatureHint")}</FieldDescription>
            </Field>
            {error && (
              <Alert variant="destructive">
                <AlertTitle>{t("outbound.limitsError")}</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
          </FieldGroup>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={saving}>
              {saving && <Spinner data-icon="inline-start" />}
              {t("outbound.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function OutboundSkeleton() {
  return (
    <PagePending page="nav.outbound">
      <PageHeaderSkeleton title="w-36" action="w-36" />
      <div className="flex flex-col gap-4">
        <TabsSkeleton className="w-64" />
        <TableSkeleton
          rows={6}
          columns={[
            { head: "w-16", sub: true },
            { className: "w-48", head: "w-16", short: "w-28" },
            { className: "w-28 max-sm:hidden", head: "w-10", short: "w-10" },
            { className: "w-12", controls: ["size-4 rounded-sm"] },
          ]}
        />
      </div>
    </PagePending>
  )
}

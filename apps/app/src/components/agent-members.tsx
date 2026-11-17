// The Members section of an agent's page (PLAN 19.4, 19.7). It saves at once
// and loads on its own, so it never touches the settings form's dirty state.
import * as React from "react"
import { CircleAlertIcon, PencilIcon, PlusIcon, UserMinusIcon, UserPlusIcon, UsersIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { TelegramIcon, WhatsAppIcon } from "@/components/brand-icons"
import { SimpleSelect } from "@/components/simple-select"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Item, ItemActions, ItemContent, ItemGroup, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { getAgentMembersFn, saveMemberFn, setAgentMemberFn, type AgentMembers as Members, type MemberChannel, type MemberView, type Person } from "@/lib/api/agents"
import { RpcError } from "@/lib/api/client"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

const MAX_ACCOUNTS = 5
const NAME_MAX = 60
const ADDRESS_MAX = 64

const CHANNEL: Record<MemberChannel, { Icon: React.ComponentType<React.SVGProps<SVGSVGElement>>; label: string; placeholder: string }> = {
  whatsapp: { Icon: WhatsAppIcon, label: "WhatsApp", placeholder: "0812…" },
  telegram: { Icon: TelegramIcon, label: "Telegram", placeholder: "123456789…" },
}

type AgentRef = { id: string; name: string; isDefault: boolean }
type AccountRow = { key: number; channel: MemberChannel; address: string }
type Target = { kind: "new" } | { kind: "edit"; member: MemberView }

export function AgentMembers({ agent }: { agent: AgentRef }) {
  const t = useT()
  const [state, setState] = React.useState<{ data?: Members; error?: string }>({})
  const [version, setVersion] = React.useState(0)
  const [target, setTarget] = React.useState<Target | null>(null)
  const [confirm, setConfirm] = React.useState<{ member: MemberView; open: boolean } | null>(null)
  const reload = () => setVersion((v) => v + 1)

  React.useEffect(() => {
    if (agent.isDefault) return
    let live = true
    getAgentMembersFn({ data: { id: agent.id } }).then(
      (data) => live && setState({ data }),
      (err: unknown) => live && setState({ error: errorMessage(err) }),
    )
    return () => {
      live = false
    }
  }, [agent.id, agent.isDefault, version])

  if (agent.isDefault) return null

  // Taking a member out of their last agent deletes them and their accounts.
  const isLast = (m: MemberView) => m.agents.every((a) => a.id === agent.id)

  const remove = async (m: MemberView) => {
    const before = state.data
    if (before) setState({ data: { ...before, members: before.members.filter((x) => x.id !== m.id) } })
    try {
      await setAgentMemberFn({ data: { agentId: agent.id, memberId: m.id, on: false } })
      toast.success(isLast(m) ? t("agents.members.deleted", { name: m.name }) : t("agents.members.removed", { name: m.name, agent: agent.name }))
      reload()
    } catch (err) {
      setState({ data: before })
      toast.error(errorMessage(err))
    }
  }

  const { data, error } = state
  if (error) {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>{t("agents.loadError")}</AlertTitle>
        <AlertDescription className="[overflow-wrap:anywhere]">{error}</AlertDescription>
      </Alert>
    )
  }
  if (!data) {
    return (
      <div className="flex flex-col gap-3" aria-busy="true">
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-16 w-full" />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {data.members.length === 0 ? (
        <Empty className="border border-dashed p-6">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <UsersIcon />
            </EmptyMedia>
            <EmptyTitle>{t("agents.members.empty")}</EmptyTitle>
            <EmptyDescription>{t("agents.members.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button variant="outline" size="sm" onClick={() => setTarget({ kind: "new" })}>
              <UserPlusIcon data-icon="inline-start" />
              {t("agents.members.add")}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <>
          <ItemGroup className="gap-3">
            {data.members.map((m) => (
              <MemberRow key={m.id} member={m} agentId={agent.id} onEdit={() => setTarget({ kind: "edit", member: m })} onRemove={() => (isLast(m) ? setConfirm({ member: m, open: true }) : void remove(m))} />
            ))}
          </ItemGroup>
          <Button variant="outline" size="sm" className="self-start" onClick={() => setTarget({ kind: "new" })}>
            <UserPlusIcon data-icon="inline-start" />
            {t("agents.members.add")}
          </Button>
        </>
      )}
      <p className="text-caption text-pretty text-muted-foreground">{t("agents.members.telegramGroups")}</p>
      <AlertDialog open={confirm?.open ?? false} onOpenChange={(o) => !o && setConfirm((c) => c && { ...c, open: false })}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="[overflow-wrap:anywhere]">{t("agents.members.deleteTitle", { name: confirm?.member.name ?? "" })}</AlertDialogTitle>
            <AlertDialogDescription className="[overflow-wrap:anywhere]">
              {t("agents.members.deleteBody", { name: confirm?.member.name ?? "", agent: agent.name })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              onClick={() => {
                if (confirm) void remove(confirm.member)
              }}
            >
              {t("agents.members.deleteAction")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {target && (
        <MemberDialog
          key={target.kind === "edit" ? target.member.id : "new"}
          agent={agent}
          target={target}
          people={data.people}
          others={data.others}
          onClose={() => setTarget(null)}
          onSaved={() => {
            setTarget(null)
            reload()
          }}
        />
      )}
    </div>
  )
}

function MemberRow({ member, agentId, onEdit, onRemove }: { member: MemberView; agentId: string; onEdit: () => void; onRemove: () => void }) {
  const t = useT()
  const elsewhere = member.agents.filter((a) => a.id !== agentId)
  return (
    <Item variant="outline" role="listitem" className="items-start gap-x-4 gap-y-3 px-4 py-4">
      <ItemContent className="min-w-48 gap-2">
        <ItemTitle dir="auto" className="line-clamp-none w-full [overflow-wrap:anywhere]">
          {member.name}
        </ItemTitle>
        <ul className="flex flex-col gap-1.5">
          {member.accounts.map((a) => {
            const { Icon, label } = CHANNEL[a.channel]
            return (
              <li key={a.id} className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
                <Icon aria-hidden="true" className="size-4 shrink-0" />
                <span className="sr-only">{label}</span>
                <span className="min-w-0 tabular-nums [overflow-wrap:anywhere]">{a.label}</span>
              </li>
            )
          })}
        </ul>
        {elsewhere.length > 0 && (
          <StatusText tone="secondary" icon={<UsersIcon aria-hidden="true" />} className="whitespace-normal">
            <span className="[overflow-wrap:anywhere]">{t("agents.members.alsoIn", { names: elsewhere.map((a) => a.name).join(", ") })}</span>
          </StatusText>
        )}
      </ItemContent>
      <ItemActions className="-me-2 ms-auto">
        <Button variant="ghost" size="icon-sm" aria-label={t("agents.members.edit", { name: member.name })} onClick={onEdit}>
          <PencilIcon aria-hidden="true" />
        </Button>
        <Button variant="destructive-ghost" size="icon-sm" aria-label={t("agents.members.remove", { name: member.name })} onClick={onRemove}>
          <UserMinusIcon aria-hidden="true" />
        </Button>
      </ItemActions>
    </Item>
  )
}

const isNameRefusal = (message: string) => message === "Give the member a name." || message.startsWith("A member's name is at most")

let nextKey = 0
const row = (channel: MemberChannel = "whatsapp", address = ""): AccountRow => ({ key: ++nextKey, channel, address })

function MemberDialog({
  agent,
  target,
  people,
  others,
  onClose,
  onSaved,
}: {
  agent: AgentRef
  target: Target
  people: Person[]
  others: MemberView[]
  onClose: () => void
  onSaved: () => void
}) {
  const t = useT()
  const editing = target.kind === "edit" ? target.member : null
  const [name, setName] = React.useState(editing?.name ?? "")
  const [accounts, setAccounts] = React.useState<AccountRow[]>(() => (editing ? editing.accounts.map((a) => row(a.channel, a.address)) : [row()]))
  const [errors, setErrors] = React.useState<{ name?: string; accounts?: string }>({})
  const [busy, setBusy] = React.useState(false)

  const setAccount = (key: number, patch: Partial<AccountRow>) => {
    setAccounts((list) => list.map((a) => (a.key === key ? { ...a, ...patch } : a)))
    setErrors((e) => ({ ...e, accounts: undefined }))
  }
  const taken = new Set(accounts.map((a) => `${a.channel}:${a.address.trim()}`))
  const picks = people.filter((p) => !taken.has(`${p.channel}:${p.address}`))

  const pick = (p: Person) => {
    setAccounts((list) => {
      const empty = list.find((a) => !a.address.trim())
      if (empty) return list.map((a) => (a.key === empty.key ? { ...a, channel: p.channel, address: p.address } : a))
      return list.length < MAX_ACCOUNTS ? [...list, row(p.channel, p.address)] : list
    })
    if (!name.trim() && p.name) setName(p.name)
    setErrors({})
  }

  const save = async () => {
    const clean = accounts.map((a) => ({ channel: a.channel, address: a.address.trim() })).filter((a) => a.address)
    const next: typeof errors = {}
    if (!name.trim()) next.name = t("agents.members.err.name")
    if (clean.length === 0) next.accounts = t("agents.members.err.account")
    setErrors(next)
    if (next.name || next.accounts) return
    setBusy(true)
    try {
      await saveMemberFn({ data: { agentId: agent.id, ...(editing ? { id: editing.id } : {}), name: name.trim(), accounts: clean } })
      toast.success(t("agents.members.saved", { name: name.trim() }))
      onSaved()
    } catch (err) {
      // Match only the server's two name refusals: other refusals can quote a
      // member's name, which may contain the word "name".
      if (err instanceof RpcError && err.code === "BAD_REQUEST") {
        setErrors(isNameRefusal(err.message) ? { name: err.message } : { accounts: err.message })
      } else {
        toast.error(errorMessage(err))
      }
      setBusy(false)
    }
  }

  const link = async (m: MemberView) => {
    setBusy(true)
    try {
      await setAgentMemberFn({ data: { agentId: agent.id, memberId: m.id, on: true } })
      toast.success(t("agents.members.saved", { name: m.name }))
      onSaved()
    } catch (err) {
      toast.error(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="pe-8 [overflow-wrap:anywhere]">{editing ? t("agents.members.edit", { name: editing.name }) : t("agents.members.add")}</DialogTitle>
        </DialogHeader>
        <form
          id="member-form"
          className="flex flex-col gap-6"
          onSubmit={(e) => {
            e.preventDefault()
            void save()
          }}
        >
          <Field data-invalid={!!errors.name}>
            <FieldLabel htmlFor="member-name">{t("agents.members.field.name")}</FieldLabel>
            <Input
              id="member-name"
              dir="auto"
              value={name}
              maxLength={NAME_MAX}
              aria-invalid={!!errors.name}
              onChange={(e) => {
                setName(e.target.value)
                setErrors((x) => ({ ...x, name: undefined }))
              }}
            />
            {errors.name && <FieldError>{errors.name}</FieldError>}
          </Field>

          <Field data-invalid={!!errors.accounts}>
            <FieldLabel htmlFor={`member-address-${accounts[0]?.key}`}>{t("agents.members.field.accounts")}</FieldLabel>
            <ul className="flex flex-col gap-3">
              {accounts.map((a) => (
                <li key={a.key} className="flex min-w-0 items-center gap-2">
                  <SimpleSelect
                    className="w-fit shrink-0"
                    aria-label={t("agents.members.channel")}
                    value={a.channel}
                    options={(Object.keys(CHANNEL) as MemberChannel[]).map((c) => {
                      const { Icon, label } = CHANNEL[c]
                      return { value: c, label, icon: <Icon aria-hidden="true" /> }
                    })}
                    onChange={(v) => setAccount(a.key, { channel: v as MemberChannel })}
                  />
                  <Input
                    id={`member-address-${a.key}`}
                    className="min-w-0 flex-1 tabular-nums"
                    inputMode={a.channel === "telegram" ? "numeric" : "tel"}
                    autoComplete="off"
                    maxLength={ADDRESS_MAX}
                    placeholder={CHANNEL[a.channel].placeholder}
                    aria-label={t(`agents.members.address.${a.channel}`)}
                    aria-invalid={!!errors.accounts}
                    value={a.address}
                    onChange={(e) => setAccount(a.key, { address: e.target.value })}
                  />
                  {accounts.length > 1 && (
                    <Button type="button" variant="ghost" size="icon-sm" aria-label={t("agents.members.removeAccount")} onClick={() => setAccounts((list) => list.filter((x) => x.key !== a.key))}>
                      <XIcon aria-hidden="true" />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
            {errors.accounts ? <FieldError className="[overflow-wrap:anywhere]">{errors.accounts}</FieldError> : <FieldDescription>{t("agents.members.accountsHint")}</FieldDescription>}
            {accounts.length < MAX_ACCOUNTS && (
              <Button type="button" variant="ghost" size="sm" className="-ms-2.5 self-start" onClick={() => setAccounts((list) => [...list, row()])}>
                <PlusIcon data-icon="inline-start" />
                {t("agents.members.addAccount")}
              </Button>
            )}
          </Field>

          {picks.length > 0 && (
            <section aria-labelledby="member-people" className="flex flex-col gap-2">
              <h3 id="member-people" className="text-sm font-medium">
                {t("agents.members.people")}
              </h3>
              <p className="text-caption text-muted-foreground">{t("agents.members.peopleBody")}</p>
              <ul className="flex max-h-60 flex-col gap-1 overflow-y-auto">
                {picks.map((p) => (
                  <li key={`${p.channel}:${p.address}`}>
                    <PersonButton person={p} onPick={() => pick(p)} />
                  </li>
                ))}
              </ul>
            </section>
          )}

          {!editing && others.length > 0 && (
            <section aria-labelledby="member-others" className="flex flex-col gap-2">
              <h3 id="member-others" className="text-sm font-medium">
                {t("agents.members.others")}
              </h3>
              <ul className="flex flex-col gap-1">
                {others.map((m) => (
                  <li key={m.id}>
                    <button
                      type="button"
                      disabled={busy}
                      aria-label={t("agents.members.link", { name: m.name })}
                      className="flex w-full min-w-0 items-center gap-3 rounded-lg px-2 py-2 text-start text-sm outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50"
                      onClick={() => void link(m)}
                    >
                      <UserPlusIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                      <span dir="auto" className="min-w-0 flex-1 [overflow-wrap:anywhere]">
                        {m.name}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
        </form>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="member-form" disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function PersonButton({ person, onPick }: { person: Person; onPick: () => void }) {
  const { Icon, label } = CHANNEL[person.channel]
  return (
    <button
      type="button"
      className="flex w-full min-w-0 items-center gap-3 rounded-lg px-2 py-2 text-start text-sm outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50"
      onClick={onPick}
    >
      <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <span className="sr-only">{label}</span>
      <span className="flex min-w-0 flex-1 flex-col">
        {person.name && (
          <span dir="auto" className="[overflow-wrap:anywhere]">
            {person.name}
          </span>
        )}
        <span className="text-caption text-muted-foreground tabular-nums [overflow-wrap:anywhere]">{person.label}</span>
      </span>
    </button>
  )
}

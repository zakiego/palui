// The Chat rules section of an agent's page: rules the owner or allowed
// members set from shared chats, and every add and remove with undo. It
// saves at once and loads on its own, like Members.
import * as React from "react"
import { CircleAlertIcon, ScrollTextIcon, Trash2Icon, Undo2Icon } from "lucide-react"
import { toast } from "sonner"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Item, ItemActions, ItemContent, ItemGroup, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusText } from "@/components/ui/status-text"
import { deleteChatRuleFn, getChatRulesFn, undoChatRuleChangeFn, type ChatRuleChange, type ChatRules } from "@/lib/api/agents"
import { ago, errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

type AgentRef = { id: string; isDefault: boolean }

export function AgentChatRules({ agent }: { agent: AgentRef }) {
  const t = useT()
  const [state, setState] = React.useState<{ data?: ChatRules; error?: string }>({})
  const [version, setVersion] = React.useState(0)
  const [busy, setBusy] = React.useState<string | null>(null)
  const reload = () => setVersion((v) => v + 1)

  React.useEffect(() => {
    if (agent.isDefault) return
    let live = true
    getChatRulesFn({ data: { id: agent.id } }).then(
      (data) => live && setState({ data }),
      (err: unknown) => live && setState({ error: errorMessage(err) }),
    )
    return () => {
      live = false
    }
  }, [agent.id, agent.isDefault, version])

  if (agent.isDefault) return null

  const run = async (key: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(key)
    try {
      await fn()
      toast.success(done)
      reload()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
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
      </div>
    )
  }

  const where = (title: string) => title || t("agents.rules.untitledChat")

  return (
    <div className="flex flex-col gap-8">
      {data.rules.length === 0 ? (
        <Empty className="border border-dashed p-6">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ScrollTextIcon />
            </EmptyMedia>
            <EmptyTitle>{t("agents.rules.empty")}</EmptyTitle>
            <EmptyDescription>{t("agents.rules.emptyBody")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ItemGroup className="gap-3">
          {data.rules.map((r) => (
            <Item key={r.id} variant="outline" role="listitem" className="items-start gap-x-4 gap-y-3 px-4 py-4">
              <ItemContent className="min-w-48 gap-1.5">
                <ItemTitle dir="auto" className="line-clamp-none w-full [overflow-wrap:anywhere]">
                  {r.title}
                </ItemTitle>
                <p dir="auto" className="text-sm whitespace-pre-line text-pretty [overflow-wrap:anywhere]">
                  {r.body}
                </p>
                <p className="text-caption text-muted-foreground [overflow-wrap:anywhere]">
                  {t("agents.rules.meta", { chat: where(r.chatTitle), by: r.fromOwner ? t("agents.rules.you") : r.by, time: ago(r.createdAt) })}
                </p>
              </ItemContent>
              <ItemActions className="-me-2 ms-auto">
                <Button
                  variant="destructive-ghost"
                  size="icon-sm"
                  disabled={busy !== null}
                  aria-label={t("agents.rules.remove", { title: r.title })}
                  onClick={() => void run(r.id, () => deleteChatRuleFn({ data: { agentId: agent.id, id: r.id } }), t("agents.rules.removed", { title: r.title }))}
                >
                  <Trash2Icon aria-hidden="true" />
                </Button>
              </ItemActions>
            </Item>
          ))}
        </ItemGroup>
      )}

      {data.changes.length > 0 && (
        <section aria-labelledby="chat-rule-changes" className="flex flex-col gap-3">
          <h3 id="chat-rule-changes" className="text-sm font-medium">
            {t("agents.rules.changes")}
          </h3>
          <ul className="flex flex-col gap-3">
            {data.changes.map((c) => (
              <ChangeRow
                key={c.id}
                change={c}
                chat={where(c.chatTitle)}
                disabled={busy !== null}
                onUndo={() => void run(c.id, () => undoChatRuleChangeFn({ data: { agentId: agent.id, id: c.id } }), t("agents.rules.undoneToast"))}
              />
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function ChangeRow({ change, chat, disabled, onUndo }: { change: ChatRuleChange; chat: string; disabled: boolean; onUndo: () => void }) {
  const t = useT()
  const by = change.fromOwner ? t("agents.rules.you") : change.by
  const what = change.action === "add" ? t("agents.rules.added", { by, chat }) : t("agents.rules.removedBy", { by, chat })
  return (
    <li className="flex flex-wrap items-start gap-x-4 gap-y-2 rounded-lg border px-4 py-3">
      <div className="flex min-w-48 flex-1 flex-col gap-1">
        <p className="text-sm [overflow-wrap:anywhere]">
          <span>{what}</span>{" "}
          <span dir="auto" className="font-medium">
            {change.title}
          </span>
        </p>
        <p dir="auto" className="line-clamp-2 text-caption text-muted-foreground [overflow-wrap:anywhere]">
          {change.body}
        </p>
        <p className="text-caption text-muted-foreground">{ago(change.createdAt)}</p>
      </div>
      {change.undoneAt ? (
        <StatusText tone="secondary">{t("agents.rules.undone")}</StatusText>
      ) : (
        <Button variant="outline" size="sm" disabled={disabled} onClick={onUndo} aria-label={t("agents.rules.undoLabel", { title: change.title })}>
          <Undo2Icon data-icon="inline-start" />
          {t("agents.rules.undo")}
        </Button>
      )}
    </li>
  )
}

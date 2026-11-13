// Chats (PLAN 11.4): every chat across channels on the left (a Drawer on a
// phone), the transcript on the right. The "Palui (web)" chat has an input that
// sends through the runtime and streams the reply over /api/chat/stream. Tool
// calls sit inline and collapsible; pending approvals show Approve/Deny.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { ArrowUpIcon, AtSignIcon, CircleAlertIcon, CopyIcon, HashIcon, ListIcon, MessagesSquareIcon, MonitorIcon, SearchIcon, ShieldAlertIcon, SquareIcon, WebhookIcon } from "lucide-react"
import { TelegramIcon, WhatsAppIcon } from "@/components/brand-icons"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { Markdown } from "@/components/markdown"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, SkeletonText } from "@/components/page-pending"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { StatusText } from "@/components/ui/status-text"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Drawer, DrawerContent, DrawerHeader, DrawerTitle } from "@/components/ui/drawer"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput, InputGroupTextarea } from "@/components/ui/input-group"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Message, MessageContent, MessageFooter } from "@/components/ui/message"
import { MessageScroller, MessageScrollerButton, MessageScrollerContent, MessageScrollerItem, MessageScrollerViewport } from "@/components/ui/message-scroller"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { RuntimeAlert } from "@/components/runtime-alert"
import { CLASS_TONE, ToolTrace } from "@/components/tool-trace"
import { AgentAvatar } from "@/components/agent-avatar"
import { SimpleSelect } from "@/components/simple-select"
import { Switch } from "@/components/ui/switch"
import { setChatAgentFn, setChatAllowedFn, setChatReplyModeFn } from "@/lib/fns/agents"
import { decideApproval, getChats, getTranscript, sendWebMessage, stopTurn } from "@/lib/fns/chats"
import { ago, errorMessage } from "@/lib/format"
import { markdownToPlain } from "@/lib/markdown"
import { transcriptToMarkdown } from "@/lib/transcript-text"
import { useLabel, useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/chats")({
  validateSearch: z.object({ chat: z.string().max(100).optional() }),
  loaderDeps: ({ search }) => ({ chat: search.chat }),
  loader: async ({ deps }) => {
    const read = (chatId: string) => getTranscript({ data: { chatId } }).catch((err: unknown) => ({ error: errorMessage(err) }))
    // With a chat in the URL the transcript doesn't depend on the list, so both go out together.
    const asked = deps.chat ? read(deps.chat) : undefined
    const list = await getChats()
    const chatId = deps.chat ?? list.webChatId
    const transcript = await (asked ?? read(chatId))
    return { ...list, chatId, transcript }
  },
  head: () => ({ meta: [{ title: "Chats · Palui" }] }),
  component: ChatsPage,
  pendingComponent: ChatsSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getChats>> & { chatId: string; transcript: Awaited<ReturnType<typeof getTranscript>> | { error: string } }
type ChatRow = Data["chats"][number]
type Transcript = Awaited<ReturnType<typeof getTranscript>>

const CHANNEL_ICON = { telegram: TelegramIcon, whatsapp: WhatsAppIcon, web: MonitorIcon } as const

// Both panes are a fixed height at every width, so the transcript scrolls
// inside its card and opens at the newest message. Without a height on a phone
// the card grew with the conversation and the composer ended up screens away.
const PANE_HEIGHT = "h-[calc(100svh-16rem)] min-h-96 md:h-[calc(100svh-15rem)]"

// Rows the chat list renders at once. Past this, search narrows it down.
const LIST_LIMIT = 100

function ChatsPage() {
  const data = Route.useLoaderData() as Data
  const t = useT()
  const [listOpen, setListOpen] = React.useState(false)
  const current = data.chats.find((c) => c.id === data.chatId)

  const list = <ChatList chats={data.chats} activeId={data.chatId} onPick={() => setListOpen(false)} />

  return (
    <>
      <PageHeader
        title={t("chats.title")}
        actions={
          // The list sits beside the transcript only when the page itself is
          // wide enough (a container query, so an open sidebar counts). Below
          // that it's a drawer behind this button.
          <Button variant="outline" className="@3xl/main:hidden" onClick={() => setListOpen(true)}>
            <ListIcon data-icon="inline-start" />
            {t("chats.openList")}
          </Button>
        }
      />
      <div className="grid min-h-0 flex-1 gap-4 @3xl/main:grid-cols-[minmax(14rem,18rem)_minmax(0,1fr)]">
        <Card className={cn("hidden min-h-0 gap-0 py-0 @3xl/main:flex", PANE_HEIGHT)}>
          <h2 className="border-b px-4 py-3 text-heading">{t("chats.list")}</h2>
          <ScrollArea className="min-h-0 flex-1">{list}</ScrollArea>
        </Card>
        {"error" in data.transcript ? (
          <Alert variant="destructive" className="self-start">
            <CircleAlertIcon aria-hidden="true" />
            <AlertTitle>{t("chats.loadError")}</AlertTitle>
            <AlertDescription className="break-words">
              <span className="block">{data.transcript.error}</span>
              <span className="block">{t("chats.loadErrorFix")}</span>
            </AlertDescription>
            <AlertAction>
              <Button size="sm" variant="outline" render={<Link to="/chats" search={{}} />}>
                {t("chats.openWeb")}
              </Button>
            </AlertAction>
          </Alert>
        ) : (
          <TranscriptPane key={data.chatId} transcript={data.transcript} isWeb={current?.channel === "web" || data.chatId === data.webChatId} />
        )}
      </div>
      <Drawer open={listOpen} onOpenChange={setListOpen} showSwipeHandle>
        <DrawerContent>
          <DrawerHeader className="group-data-[side=bottom]/drawer-popup:text-start">
            <DrawerTitle>{t("chats.list")}</DrawerTitle>
          </DrawerHeader>
          <div className="min-h-0 flex-1 overflow-y-auto pb-[max(1rem,env(safe-area-inset-bottom))]">{list}</div>
        </DrawerContent>
      </Drawer>
    </>
  )
}

function ChatList({ chats, activeId, onPick }: { chats: ChatRow[]; activeId: string; onPick: () => void }) {
  const t = useT()
  const [query, setQuery] = React.useState("")
  const matches = React.useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? chats.filter((c) => c.title.toLowerCase().includes(q) || c.preview.toLowerCase().includes(q)) : chats
  }, [chats, query])
  // The open chat stays in the list even when it's past the cap.
  const rows = React.useMemo(() => {
    const head = matches.slice(0, LIST_LIMIT)
    const active = matches.find((c) => c.id === activeId)
    return active && !head.includes(active) ? [...head, active] : head
  }, [matches, activeId])
  if (chats.length === 0) {
    return (
      <Empty className="p-4">
        <EmptyHeader>
          <EmptyTitle>{t("chats.empty")}</EmptyTitle>
          <EmptyDescription>{t("chats.emptyBody")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button size="sm" variant="outline" render={<Link to="/connectors" />}>
            {t("nav.connectors")}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }
  return (
    <ItemGroup className="gap-0 p-2">
      {/* Only once the list is long enough to need it. */}
      {chats.length > 8 && (
        <InputGroup className="mb-1">
          <InputGroupAddon>
            <SearchIcon aria-hidden="true" />
          </InputGroupAddon>
          <InputGroupInput type="search" aria-label={t("chats.search")} placeholder={t("chats.search")} value={query} onChange={(e) => setQuery(e.target.value)} />
        </InputGroup>
      )}
      {matches.length === 0 && <p className="px-2 py-6 text-center text-sm text-muted-foreground">{t("chats.searchEmpty")}</p>}
      {rows.map((c) => {
        const Icon = CHANNEL_ICON[c.channel]
        const active = c.id === activeId
        return (
          <Item
            key={c.id}
            size="sm"
            className={cn("rounded-lg", active && "bg-accent")}
            render={<Link to="/chats" search={{ chat: c.id }} aria-current={active ? "page" : undefined} onClick={onPick} />}
          >
            <ItemMedia variant="icon">
              <Icon aria-hidden="true" />
            </ItemMedia>
            <ItemContent className="min-w-0">
              <ItemTitle className="w-full min-w-0">
                <span dir="auto" className="line-clamp-2 min-w-0 flex-1 text-left break-words" title={c.title}>
                  {c.title}
                </span>
                <span className="shrink-0 text-caption font-normal text-muted-foreground tabular-nums">{ago(c.lastAt)}</span>
              </ItemTitle>
              <ItemDescription className="line-clamp-1 break-all">
                <span className="sr-only">{c.channel}. </span>
                {!c.allowed && c.channel !== "web" && (
                  <StatusText tone="secondary" className="mr-1.5 align-[-2px]">
                    {t("chats.off")}
                  </StatusText>
                )}
                {c.preview ? (c.previewRole === "assistant" ? markdownToPlain(c.preview) : c.preview) : t("chats.noMessages")}
              </ItemDescription>
            </ItemContent>
          </Item>
        )
      })}
      {matches.length > rows.length && (
        <p className="px-2 py-3 text-center text-caption text-muted-foreground tabular-nums">
          {t("chats.listCapped", { shown: rows.length.toLocaleString("en"), total: matches.length.toLocaleString("en") })}
        </p>
      )}
    </ItemGroup>
  )
}

// --- live turn state from /api/chat/stream ------------------------------------------

type LiveTool = { id: string; tool: string; class: string; status: "running" | "ok" | "error" | "denied"; ms?: number; preview?: string }
type LiveTurn = { runId: string; text: string; tools: LiveTool[]; approvals: { approvalId: string; tool: string; class: string; argsPreview: string }[] }

type TurnEvent =
  | { type: "turn-start"; chatId: string; runId: string }
  | { type: "text-delta"; chatId: string; runId: string; delta: string }
  | { type: "tool-call"; chatId: string; runId: string; toolCallId: string; tool: string; class: string }
  | { type: "tool-result"; chatId: string; runId: string; toolCallId: string; tool: string; preview: string; ms: number; status: "ok" | "error" | "denied" }
  | { type: "approval"; chatId: string; runId: string; approvalId: string; tool: string; class: string; argsPreview: string }
  | { type: "message"; chatId: string }
  | { type: "turn-end"; chatId: string; runId: string; status: string; error?: string }

function useTurnStream(chatId: string, onSettled: () => Promise<unknown>) {
  const [turn, setTurn] = React.useState<LiveTurn | null>(null)
  const [down, setDown] = React.useState(false)
  const settled = React.useRef(onSettled)
  settled.current = onSettled
  React.useEffect(() => {
    if (typeof EventSource === "undefined") return
    const es = new EventSource("/api/chat/stream")
    es.onopen = () => setDown(false)
    es.onerror = () => setDown(true)
    es.onmessage = (e) => {
      let ev: TurnEvent
      try {
        ev = JSON.parse(e.data) as TurnEvent
      } catch {
        return
      }
      if (ev.chatId !== chatId) return
      switch (ev.type) {
        case "turn-start":
          setTurn({ runId: ev.runId, text: "", tools: [], approvals: [] })
          break
        case "text-delta":
          setTurn((p) => ({ ...(p ?? { runId: ev.runId, tools: [], approvals: [] }), text: (p?.text ?? "") + ev.delta }))
          break
        case "tool-call":
          setTurn((p) => (p ? { ...p, tools: [...p.tools, { id: ev.toolCallId, tool: ev.tool, class: ev.class, status: "running" }] } : p))
          break
        case "tool-result":
          setTurn((p) => (p ? { ...p, tools: p.tools.map((x) => (x.id === ev.toolCallId ? { ...x, status: ev.status, ms: ev.ms, preview: ev.preview } : x)) } : p))
          break
        case "approval":
          setTurn((p) => (p ? { ...p, approvals: [...p.approvals, { approvalId: ev.approvalId, tool: ev.tool, class: ev.class, argsPreview: ev.argsPreview }] } : p))
          void settled.current()
          break
        case "message":
          void settled.current()
          break
        case "turn-end":
          if (ev.status === "error" && ev.error) toast.error(ev.error)
          // The live reply stays until the saved message has loaded, so it
          // never blinks out between the stream ending and the refresh.
          void settled.current().finally(() => setTurn((p) => (p?.runId === ev.runId ? null : p)))
          break
      }
    }
    return () => es.close()
  }, [chatId])
  return { turn, down }
}

function TranscriptPane({ transcript, isWeb }: { transcript: Transcript; isWeb: boolean }) {
  const t = useT()
  const router = useRouter()
  const [optimistic, setOptimistic] = React.useState<string[]>([])
  const refresh = React.useCallback(() => router.invalidate().then(() => setOptimistic([])), [router])
  const { turn, down } = useTurnStream(transcript.chat.id, refresh)
  const empty = transcript.messages.length === 0 && optimistic.length === 0 && !turn

  return (
    <Card className={cn("min-w-0 gap-0 py-0", PANE_HEIGHT)}>
      <header className="flex min-w-0 items-center gap-2 border-b px-4 py-3">
        <h2 dir="auto" className="line-clamp-2 min-w-0 text-heading break-words" title={transcript.chat.title}>
          {transcript.chat.title}
        </h2>
        <Badge variant="outline">{transcript.chat.channel}</Badge>
        <CopyMenu transcript={transcript} />
      </header>
      {transcript.chat.channel !== "web" && <AgentBar transcript={transcript} />}
      {isWeb && down && <RuntimeAlert className="m-3 w-auto" />}
      <MessageScroller className="min-h-0 flex-1">
        <MessageScrollerViewport>
          <MessageScrollerContent className="gap-4 p-4">
            {empty && (
              <Empty className="my-auto">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <MessagesSquareIcon />
                  </EmptyMedia>
                  <EmptyDescription>{isWeb ? t("chats.webEmpty") : t("chats.transcriptEmpty")}</EmptyDescription>
                </EmptyHeader>
              </Empty>
            )}
            {transcript.messages.map((m) => (
              <MessageScrollerItem key={m.id}>
                {m.role === "assistant" && m.tools.length > 0 && <ToolTrace className="mb-2" tools={m.tools.map((x) => ({ id: x.id, tool: x.tool, class: x.class, status: x.status as LiveTool["status"], ms: x.ms ?? undefined, preview: x.result ?? undefined, args: x.args }))} />}
                <ChatMessage role={m.role} source={m.source} sender={m.sender} text={m.text} at={m.createdAt} />
              </MessageScrollerItem>
            ))}
            {optimistic.map((text, i) => (
              <MessageScrollerItem key={`opt-${i}`}>
                <ChatMessage role="user" text={text} pending />
              </MessageScrollerItem>
            ))}
            {turn && (
              <MessageScrollerItem>
                {turn.tools.length > 0 && <ToolTrace className="mb-2" tools={turn.tools} />}
                {turn.text ? (
                  <ChatMessage role="assistant" text={turn.text} streaming />
                ) : (
                  <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
                    <Spinner aria-hidden="true" />
                    {t("chats.thinking")}
                  </p>
                )}
              </MessageScrollerItem>
            )}
            {transcript.approvals.map((a) => (
              // content-visibility clips paint at the item box; the padding keeps the card's ring.
              <MessageScrollerItem key={a.id} className="p-px">
                <ApprovalCard approval={{ id: a.id, tool: a.tool, class: a.class, argsPreview: a.argsPreview ?? "" }} onDone={refresh} />
              </MessageScrollerItem>
            ))}
          </MessageScrollerContent>
        </MessageScrollerViewport>
        <MessageScrollerButton />
      </MessageScroller>
      {isWeb && (
        <Composer
          busy={!!turn}
          onSend={async (text) => {
            setOptimistic((p) => [...p, text])
            try {
              await sendWebMessage({ data: { text } })
              // The runtime wrote the message; once it's loaded, drop the local copy.
              await router.invalidate()
              setOptimistic((p) => p.filter((x) => x !== text))
            } catch (err) {
              setOptimistic((p) => p.filter((x) => x !== text))
              toast.error(errorMessage(err))
              throw err
            }
          }}
          onStop={async () => {
            try {
              await stopTurn({ data: { chatId: transcript.chat.id } })
            } catch (err) {
              toast.error(errorMessage(err))
            }
          }}
        />
      )}
    </Card>
  )
}

// Who answers in this chat and whether it's answered at all (PLAN 19.2).
function AgentBar({ transcript }: { transcript: Transcript }) {
  const t = useT()
  const router = useRouter()
  const chat = transcript.chat
  const [agentId, setAgentId] = React.useState(transcript.agent.id)
  const [allowed, setAllowed] = React.useState(chat.allowed)
  const [replyMode, setReplyMode] = React.useState(chat.replyMode)
  const [busy, setBusy] = React.useState(false)
  React.useEffect(() => setAgentId(transcript.agent.id), [transcript.agent.id])
  React.useEffect(() => setAllowed(chat.allowed), [chat.allowed])
  React.useEffect(() => setReplyMode(chat.replyMode), [chat.replyMode])

  const pick = async (id: string) => {
    const next = transcript.agents.find((a) => a.id === id)
    if (!next || id === agentId) return
    const before = { agentId, allowed }
    // Picking an agent turns the chat on.
    setAgentId(id)
    setAllowed(true)
    setBusy(true)
    try {
      const r = await setChatAgentFn({ data: { chatId: chat.id, agentId: next.isDefault ? null : id } })
      toast.success(t(r.saved ? "chats.agentSaved" : "chats.agentSavedNoMemory", { name: next.name }))
      void router.invalidate()
    } catch (err) {
      setAgentId(before.agentId)
      setAllowed(before.allowed)
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
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

  return (
    <div className="flex flex-col gap-2 border-b px-4 py-2">
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
        {chat.connectorId ? (
          <span className="text-sm text-muted-foreground [overflow-wrap:anywhere]">{t("chats.botChat", { name: transcript.agent.name })}</span>
        ) : (
          <SimpleSelect
            size="sm"
            className="w-full max-w-56 min-w-0"
            aria-label={t("chats.agentFor", { name: chat.title })}
            disabled={busy}
            value={agentId}
            options={transcript.agents.map((a) => ({ value: a.id, label: a.name, icon: <AgentAvatar agent={a} size="xs" /> }))}
            onChange={(id) => void pick(id)}
          />
        )}
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
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={allowed} disabled={busy} onCheckedChange={(v) => void toggle(v)} aria-label={t("chats.allowedFor", { name: chat.title })} />
          {t("chats.allowed")}
        </label>
      </div>
      {transcript.shared && transcript.agent.isDefault && <p className="text-caption text-muted-foreground">{t("chats.guestsWaiting")}</p>}
      {chat.channel === "telegram" && chat.kind === "group" && replyMode === "all" && <p className="text-caption text-muted-foreground">{t("chats.privacyMode")}</p>}
    </div>
  )
}

// Copies what's loaded: the transcript query keeps the newest 150 messages.
function CopyMenu({ transcript }: { transcript: Transcript }) {
  const t = useT()
  const copy = async (text: string, done: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(done)
    } catch {
      toast.error(t("chats.copyFailed"))
    }
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" className="ms-auto shrink-0" aria-label={t("chats.copyMenu")} />}>
        <CopyIcon aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        <DropdownMenuItem disabled={transcript.messages.length === 0} onSelect={() => void copy(transcriptToMarkdown(transcript), t("chats.copiedConversation"))}>
          <MessagesSquareIcon aria-hidden="true" />
          {t("chats.copyConversation")}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void copy(transcript.chat.id, t("chats.copiedId"))}>
          <HashIcon aria-hidden="true" />
          {t("chats.copyId")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function ChatMessage({
  role,
  source,
  sender,
  text,
  at,
  pending,
  streaming,
}: {
  role: "user" | "assistant"
  source?: "hook" | null
  // Set for someone other than the owner in a shared chat.
  sender?: string | null
  text: string
  at?: number
  pending?: boolean
  streaming?: boolean
}) {
  // A webhook event arrives as a user row but isn't something I typed: show it
  // on the agent's side as an outlined event, as plain text (it's untrusted).
  const t = useT()
  if (source === "hook") {
    return (
      <Message align="start">
        <MessageContent>
          <Bubble variant="outline" align="start">
            <BubbleContent className="whitespace-pre-wrap break-words">
              <span className="mb-1 flex items-center gap-1.5 text-caption">
                <WebhookIcon aria-hidden className="size-3.5" />
                {t("chats.webhookEvent")}
              </span>
              {text}
            </BubbleContent>
          </Bubble>
          {at && (
            <MessageFooter>
              <time className="tabular-nums" dateTime={new Date(at).toISOString()}>
                {ago(at)}
              </time>
            </MessageFooter>
          )}
        </MessageContent>
      </Message>
    )
  }
  if (sender) {
    return (
      <Message align="start">
        <MessageContent>
          <Bubble variant="outline" align="start">
            <BubbleContent className="whitespace-pre-wrap break-words">
              <span dir="auto" className="mb-1 block text-caption font-medium [overflow-wrap:anywhere]">
                {sender}
              </span>
              {text}
            </BubbleContent>
          </Bubble>
          {at && (
            <MessageFooter>
              <time className="tabular-nums" dateTime={new Date(at).toISOString()}>
                {ago(at)}
              </time>
            </MessageFooter>
          )}
        </MessageContent>
      </Message>
    )
  }
  const mine = role === "user"
  return (
    <Message align={mine ? "end" : "start"}>
      <MessageContent>
        {/* Mine never animates; the agent's reply fades in once as it starts. */}
        <Bubble variant={mine ? "default" : "muted"} align={mine ? "end" : "start"} className={cn(!mine && streaming && "agent-in", pending && "opacity-70")}>
          {/* Mine stays plain text; the agent's reply renders the safe Markdown subset. */}
          <BubbleContent className={mine ? "whitespace-pre-wrap break-words" : undefined}>{mine ? text : <Markdown text={text} />}</BubbleContent>
        </Bubble>
        {at && (
          <MessageFooter>
            <time className="tabular-nums" dateTime={new Date(at).toISOString()}>
              {ago(at)}
            </time>
          </MessageFooter>
        )}
      </MessageContent>
    </Message>
  )
}

function ApprovalCard({ approval, onDone }: { approval: { id: string; tool: string; class: string; argsPreview: string }; onDone: () => void }) {
  const t = useT()
  const label = useLabel()
  const [busy, setBusy] = React.useState<null | boolean>(null)
  const decide = async (approved: boolean) => {
    setBusy(approved)
    try {
      await decideApproval({ data: { id: approval.id, approved } })
      toast.success(approved ? t("chats.approved") : t("chats.denied"))
      onDone()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }
  return (
    <Card size="sm">
      {/* minmax(0, 1fr) lets the title column shrink, and the tool name may break anywhere: it has to be readable in full before you approve it. */}
      <CardHeader className="grid-cols-[auto_minmax(0,1fr)] gap-x-2">
        <ShieldAlertIcon className="mt-0.5 shrink-0 text-warning-fg" aria-hidden="true" />
        <CardTitle className="min-w-0 text-sm font-medium [overflow-wrap:anywhere]">{t("chats.approvalTitle", { tool: approval.tool })}</CardTitle>
        <CardDescription className="col-start-2">
          <StatusText tone={CLASS_TONE[approval.class] ?? "destructive"} icon={null}>{label("tool.class", approval.class)}</StatusText>
        </CardDescription>
      </CardHeader>
      {approval.argsPreview && (
        <CardContent>
          <pre className="max-h-40 overflow-auto rounded-md bg-code p-2 font-mono text-xs break-all whitespace-pre-wrap">{approval.argsPreview}</pre>
        </CardContent>
      )}
      <CardFooter className="flex-wrap gap-2">
        {/* A destructive tool gets the red confirm, so approving a delete never looks routine. */}
        <Button size="sm" variant={approval.class === "destructive" ? "danger" : "default"} disabled={busy !== null} onClick={() => void decide(true)}>
          {busy === true && <Spinner data-icon="inline-start" />}
          {t("chats.approve")}
        </Button>
        <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => void decide(false)}>
          {busy === false && <Spinner data-icon="inline-start" />}
          {t("chats.deny")}
        </Button>
      </CardFooter>
    </Card>
  )
}

function Composer({ busy, onSend, onStop }: { busy: boolean; onSend: (text: string) => Promise<void>; onStop: () => Promise<void> }) {
  const t = useT()
  const [text, setText] = React.useState("")
  const [sending, setSending] = React.useState(false)
  const submit = async () => {
    const value = text.trim()
    if (!value || sending) return
    setSending(true)
    setText("")
    try {
      await onSend(value)
    } catch {
      setText(value)
    } finally {
      setSending(false)
    }
  }
  return (
    <form
      className="border-t p-3"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <InputGroup data-focus-within-ring="">
        <InputGroupTextarea
          data-focus-within=""
          aria-label={t("chats.inputPlaceholder")}
          placeholder={t("chats.inputPlaceholder")}
          value={text}
          rows={1}
          maxLength={8000}
          className="max-h-40 min-h-10 text-base md:text-sm"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              void submit()
            }
          }}
        />
        <InputGroupAddon align="block-end" className="justify-end">
          {busy ? (
            <InputGroupButton variant="outline" size="icon-sm" aria-label={t("chats.stop")} onClick={() => void onStop()}>
              <SquareIcon aria-hidden="true" />
            </InputGroupButton>
          ) : (
            <InputGroupButton type="submit" variant="default" size="icon-sm" aria-label={t("chats.send")} disabled={!text.trim() || sending}>
              {sending ? <Spinner aria-hidden="true" /> : <ArrowUpIcon aria-hidden="true" />}
            </InputGroupButton>
          )}
        </InputGroupAddon>
      </InputGroup>
    </form>
  )
}

// The two cards the page renders: the chat list (wide layouts only, a Drawer
// otherwise) and the open transcript with its composer.
const CHAT_ROWS = [
  ["w-3/4", "w-5/6"],
  ["w-1/2", "w-2/3"],
  ["w-2/3", "w-3/4"],
  ["w-3/5", "w-1/2"],
  ["w-4/5", "w-2/3"],
  ["w-1/2", "w-3/5"],
]

function ChatsSkeleton() {
  return (
    <PagePending page="nav.chats">
      <PageHeaderSkeleton title="w-20" wraps={false} action="w-28 @3xl/main:hidden" />
      <div className="grid min-h-0 flex-1 gap-4 @3xl/main:grid-cols-[minmax(14rem,18rem)_minmax(0,1fr)]">
        <Card className={cn("hidden min-h-0 gap-0 py-0 @3xl/main:flex", PANE_HEIGHT)}>
          <div className="border-b px-4 py-3">
            <SkeletonText className="text-heading" width="w-28" />
          </div>
          <ItemGroup className="gap-0 p-2">
            {CHAT_ROWS.map(([title, preview], i) => (
              <Item key={i} size="sm" className="rounded-lg">
                <ItemMedia variant="icon">
                  <Skeleton className="size-4 rounded-sm" />
                </ItemMedia>
                <ItemContent className="min-w-0">
                  <span className="flex items-center gap-2">
                    <SkeletonText className="min-w-0 flex-1 text-sm" width={title} />
                    <SkeletonText className="text-caption" width="w-12" />
                  </span>
                  <SkeletonText className="text-sm" width={preview} />
                </ItemContent>
              </Item>
            ))}
          </ItemGroup>
        </Card>
        <Card className={cn("min-w-0 gap-0 py-0", PANE_HEIGHT)}>
          <div className="flex items-center gap-2 border-b px-4 py-3">
            <SkeletonText className="text-heading" width="w-24" />
            <Skeleton className="h-5 w-10 rounded-full" />
          </div>
          <div className="flex min-h-0 flex-1 flex-col justify-end gap-4 overflow-hidden p-4">
            <Skeleton className="ms-auto h-9 w-2/5 rounded-xl" />
            <div className="flex w-4/5 flex-col">
              <SkeletonText className="text-sm" width="w-full" />
              <SkeletonText className="text-sm" width="w-11/12" />
              <SkeletonText className="text-sm" width="w-1/2" />
            </div>
            <Skeleton className="ms-auto h-9 w-1/3 rounded-xl" />
            <div className="flex w-3/4 flex-col">
              <SkeletonText className="text-sm" width="w-full" />
              <SkeletonText className="text-sm" width="w-2/3" />
            </div>
          </div>
          <div className="border-t p-3">
            <Skeleton className="h-20 rounded-lg" />
          </div>
        </Card>
      </div>
    </PagePending>
  )
}


// Chats (PLAN 11.4): every chat across channels on the left (a Drawer on a
// phone), the transcript on the right. The "Palui (web)" chat has an input that
// sends through the runtime and streams the reply over /api/chat/stream. Tool
// calls sit inline and collapsible; pending approvals show Approve/Deny.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { ArrowUpIcon, CircleAlertIcon, ListIcon, MessagesSquareIcon, MonitorIcon, SendIcon, ShieldAlertIcon, SquareIcon, MessageCircleIcon, WebhookIcon, WrenchIcon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { Markdown } from "@/components/markdown"
import { PageHeader } from "@/components/page-header"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Drawer, DrawerContent, DrawerHeader, DrawerTitle } from "@/components/ui/drawer"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from "@/components/ui/input-group"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Message, MessageContent, MessageFooter } from "@/components/ui/message"
import { MessageScroller, MessageScrollerProvider, MessageScrollerButton, MessageScrollerContent, MessageScrollerItem, MessageScrollerViewport } from "@/components/ui/message-scroller"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Trace, TraceCollapsible, TraceCollapsibleContent, TraceCollapsibleTrigger, TraceDescription, TraceIcon, TraceLine, TraceTitle } from "@/components/ui/trace"
import { RuntimeAlert } from "@/components/runtime-alert"
import { useIsMobile } from "@/hooks/use-mobile"
import { decideApproval, getChats, getTranscript, sendWebMessage, stopTurn } from "@/lib/fns/chats"
import { ago, duration, errorMessage } from "@/lib/format"
import { markdownToPlain } from "@/lib/markdown"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/chats")({
  validateSearch: z.object({ chat: z.string().max(100).optional() }),
  loaderDeps: ({ search }) => ({ chat: search.chat }),
  loader: async ({ deps }) => {
    const list = await getChats()
    const chatId = deps.chat ?? list.webChatId
    const transcript = await getTranscript({ data: { chatId } }).catch((err: unknown) => ({ error: errorMessage(err) }))
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

const CHANNEL_ICON = { telegram: SendIcon, whatsapp: MessageCircleIcon, web: MonitorIcon } as const

function ChatsPage() {
  const data = Route.useLoaderData() as Data
  const t = useT()
  const mobile = useIsMobile()
  const [listOpen, setListOpen] = React.useState(false)
  const current = data.chats.find((c) => c.id === data.chatId)

  const list = <ChatList chats={data.chats} activeId={data.chatId} onPick={() => setListOpen(false)} />

  return (
    <>
      <PageHeader
        title={t("chats.title")}
        description={t("chats.description")}
        actions={
          mobile ? (
            <Button variant="outline" onClick={() => setListOpen(true)}>
              <ListIcon data-icon="inline-start" />
              {t("chats.openList")}
            </Button>
          ) : undefined
        }
      />
      <div className="grid min-h-0 flex-1 gap-4 md:grid-cols-[minmax(14rem,18rem)_1fr]">
        {!mobile && (
          <Card className="min-h-0 gap-0 py-0 md:h-[calc(100svh-15rem)]">
            <h2 className="border-b px-4 py-3 text-heading">{t("chats.list")}</h2>
            <ScrollArea className="min-h-0 flex-1">{list}</ScrollArea>
          </Card>
        )}
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
      {mobile && (
        <Drawer open={listOpen} onOpenChange={setListOpen} showSwipeHandle>
          <DrawerContent>
            <DrawerHeader className="group-data-[swipe-axis=y]/drawer-popup:text-start">
              <DrawerTitle>{t("chats.list")}</DrawerTitle>
            </DrawerHeader>
            <div className="min-h-0 flex-1 overflow-y-auto pb-[max(1rem,env(safe-area-inset-bottom))]">{list}</div>
          </DrawerContent>
        </Drawer>
      )}
    </>
  )
}

function ChatList({ chats, activeId, onPick }: { chats: ChatRow[]; activeId: string; onPick: () => void }) {
  const t = useT()
  if (chats.length === 0) {
    return (
      <Empty className="p-4">
        <EmptyHeader>
          <EmptyTitle>{t("chats.empty")}</EmptyTitle>
          <EmptyDescription>{t("chats.emptyBody")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button size="sm" variant="outline" render={<Link to="/saklar" />}>
            {t("nav.saklar")}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }
  return (
    <ItemGroup className="gap-0 p-2">
      {chats.map((c) => {
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
                <span dir="auto" className="line-clamp-2 min-w-0 flex-1 text-left break-words">{c.title}</span>
                <span className="shrink-0 text-caption font-normal text-muted-foreground tabular-nums">{ago(c.lastAt)}</span>
              </ItemTitle>
              <ItemDescription className="line-clamp-1 break-all">
                <span className="sr-only">{c.channel}. </span>
                {c.preview ? (c.previewRole === "assistant" ? markdownToPlain(c.preview) : c.preview) : t("chats.noMessages")}
              </ItemDescription>
            </ItemContent>
          </Item>
        )
      })}
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
    <Card className="min-h-[60svh] gap-0 py-0 md:h-[calc(100svh-15rem)]">
      <header className="flex min-w-0 items-center gap-2 border-b px-4 py-3">
        <h2 dir="auto" className="line-clamp-2 min-w-0 text-heading break-words">{transcript.chat.title}</h2>
        <Badge variant="outline">{transcript.chat.channel}</Badge>
      </header>
      {isWeb && down && <RuntimeAlert className="m-3 w-auto" />}
      <MessageScrollerProvider autoScroll defaultScrollPosition="end">
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
              <MessageScrollerItem key={m.id} messageId={m.id}>
                {m.role === "assistant" && m.tools.length > 0 && <ToolTrace tools={m.tools.map((x) => ({ id: x.id, tool: x.tool, class: x.class, status: x.status as LiveTool["status"], ms: x.ms ?? undefined, preview: x.result ?? undefined, args: x.args }))} />}
                <ChatMessage role={m.role} source={m.source} text={m.text} at={m.createdAt} />
              </MessageScrollerItem>
            ))}
            {optimistic.map((text, i) => (
              <MessageScrollerItem key={`opt-${i}`} messageId={`opt-${i}`}>
                <ChatMessage role="user" text={text} pending />
              </MessageScrollerItem>
            ))}
            {turn && (
              <MessageScrollerItem messageId={`live-${turn.runId}`}>
                {turn.tools.length > 0 && <ToolTrace tools={turn.tools} />}
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
              <MessageScrollerItem key={a.id} messageId={`approval-${a.id}`} className="p-px">
                <ApprovalCard approval={{ id: a.id, tool: a.tool, class: a.class, argsPreview: a.argsPreview ?? "" }} onDone={refresh} />
              </MessageScrollerItem>
            ))}
          </MessageScrollerContent>
        </MessageScrollerViewport>
        <MessageScrollerButton />
      </MessageScroller>
      </MessageScrollerProvider>
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

function ChatMessage({
  role,
  source,
  text,
  at,
  pending,
  streaming,
}: {
  role: "user" | "assistant"
  source?: "hook" | null
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

const CLASS_TONE: Record<string, "secondary" | "warning" | "destructive"> = { read: "secondary", write: "warning", destructive: "destructive" }

function ToolTrace({ tools }: { tools: (LiveTool & { args?: string })[] }) {
  const t = useT()
  return (
    <Trace className="mb-2">
      {tools.map((x) => (
        <TraceCollapsible key={x.id}>
          <TraceCollapsibleTrigger>
            <TraceLine status={x.status === "running" ? "running" : x.status === "error" ? "error" : undefined}>
              <TraceIcon>{x.status === "running" ? <Spinner /> : <WrenchIcon />}</TraceIcon>
              <TraceTitle className="font-mono text-caption">{x.tool}</TraceTitle>
              <Badge variant={CLASS_TONE[x.class] ?? "destructive"}>{x.class}</Badge>
              {x.status === "denied" && <Badge variant="outline">denied</Badge>}
              {x.ms != null && <TraceDescription>{duration(x.ms)}</TraceDescription>}
            </TraceLine>
          </TraceCollapsibleTrigger>
          <TraceCollapsibleContent className="flex flex-col gap-2">
            {x.args && <pre className="max-h-48 overflow-auto rounded-md bg-code p-2 font-mono text-xs break-all whitespace-pre-wrap">{x.args}</pre>}
            {x.preview ? <pre className="max-h-48 overflow-auto rounded-md bg-code p-2 font-mono text-xs break-all whitespace-pre-wrap">{x.preview}</pre> : <span className="text-caption">{t("chats.noResult")}</span>}
          </TraceCollapsibleContent>
        </TraceCollapsible>
      ))}
    </Trace>
  )
}

function ApprovalCard({ approval, onDone }: { approval: { id: string; tool: string; class: string; argsPreview: string }; onDone: () => void }) {
  const t = useT()
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
      <CardHeader className="grid-cols-[auto_1fr] gap-x-2">
        <ShieldAlertIcon className="mt-0.5 shrink-0 text-warning-fg" aria-hidden="true" />
        <CardTitle className="text-sm font-medium break-words">{t("chats.approvalTitle", { tool: approval.tool })}</CardTitle>
        <CardDescription className="col-start-2">
          <Badge variant={CLASS_TONE[approval.class] ?? "destructive"}>{approval.class}</Badge>
        </CardDescription>
      </CardHeader>
      {approval.argsPreview && (
        <CardContent>
          <pre className="max-h-40 overflow-auto rounded-md bg-code p-2 font-mono text-xs break-all whitespace-pre-wrap">{approval.argsPreview}</pre>
        </CardContent>
      )}
      <CardFooter className="gap-2">
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

function ChatsSkeleton() {
  return (
    <>
      <div className="flex flex-col gap-2">
        <Skeleton className="h-8 w-28" />
        <Skeleton className="h-4 w-64 max-w-full" />
      </div>
      <div className="grid gap-4 md:grid-cols-[minmax(14rem,18rem)_1fr]">
        <Skeleton className="hidden h-[calc(100svh-15rem)] rounded-xl md:block" />
        <Skeleton className="h-[60svh] rounded-xl md:h-[calc(100svh-15rem)]" />
      </div>
    </>
  )
}


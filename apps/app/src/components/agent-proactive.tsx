// The agent page's Proactive tab (docs/rfc/proactive-prompt.md 12). The prompt
// is a draft until Save; every other control saves on its own, like the Tools
// tab: a switch or a select saves as it changes.
import * as React from "react"
import { BellOffIcon, BookmarkIcon, CircleAlertIcon, MessageCircleIcon, MoonIcon, PlayIcon, RefreshCwIcon, SunIcon, Trash2Icon, WrenchIcon } from "lucide-react"
import { toast } from "sonner"
import { bindLayer, position } from "@ui-sdk/core"

import { SimpleSelect, type Option } from "@/components/simple-select"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText, type StatusTone } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  addProactiveNoteFn,
  checkProactiveNowFn,
  deleteProactiveNoteFn,
  editProactiveNoteFn,
  getProactiveFn,
  MAX_MENTIONS,
  MAX_NOTE_CHARS,
  MAX_NOTES_CHARS,
  MAX_PROMPT_CHARS,
  saveProactiveFn,
  testProactiveFn,
  unmuteProactiveFn,
  type NudgeStatus,
  type ProactiveConfig,
  type ProactiveNote,
  type ProactiveNudge,
  type ProactivePatch,
  type ProactiveTestResult,
  type ProactiveTool,
  type ProactiveView,
} from "@/lib/api/proactive"
import { clockTime, errorMessage } from "@/lib/format"
import { useCount, useT } from "@/lib/i18n"
import type { Key } from "@/lib/i18n/en"

type T = ReturnType<typeof useT>

const pad = (n: number) => String(n).padStart(2, "0")
const times = (from: number, to: number, stepMin: number) => {
  const out: string[] = []
  for (let m = from * 60; m <= to * 60; m += stepMin) out.push(`${pad(Math.floor(m / 60))}:${pad(m % 60)}`)
  return out
}
// Keeps the saved value listed even when it isn't one of the presets.
const withCurrent = (opts: Option[], value: string) => (opts.some((o) => o.value === value) ? opts : [{ value, label: value }, ...opts])

// A merge patch applied locally, so a control moves before the server answers.
function merged(cfg: ProactiveConfig, patch: ProactivePatch): ProactiveConfig {
  const out = { ...cfg } as Record<string, unknown>
  for (const [k, v] of Object.entries(patch)) {
    const cur = out[k]
    out[k] = v && typeof v === "object" && !Array.isArray(v) && cur && typeof cur === "object" ? { ...(cur as object), ...v } : v
  }
  return out as ProactiveConfig
}

// The server's mention syntax (internal/proactive/mentions.go): "@" then a
// namespaced tool name, not inside a word or an email address.
const MENTION_RE = /(^|[^A-Za-z0-9_.@-])@([A-Za-z0-9][A-Za-z0-9_-]*__[A-Za-z0-9][A-Za-z0-9_-]*)/g
// The mention being typed right before the caret.
const TYPING_RE = /(^|[^A-Za-z0-9_.@-])@([A-Za-z0-9_-]*)$/

export function mentionsIn(prompt: string): string[] {
  const out: string[] = []
  for (const m of prompt.matchAll(MENTION_RE)) {
    const name = m[2].replace(/[-_]+$/, "")
    if (name.length <= 64 && !out.includes(name)) out.push(name)
  }
  return out
}

// What's wrong with a mention, from the tools the agent may use (the server
// checks the same way at save and at every run).
function problemFor(mention: string, tools: ProactiveTool[]): Key | null {
  const tool = tools.find((x) => x.name === mention)
  if (!tool) return "proactive.reason.notFound"
  if (tool.class === "read") return null
  if (tool.suggestedClass) return "proactive.reason.unsorted"
  return "proactive.reason.notRead"
}

const sameDay = (ms: number) => new Date(ms).toDateString() === new Date().toDateString()
const shortDay = (ms: number) => new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "short" })
const shownAt = (n: ProactiveNudge) => (n.status === "scheduled" && n.wakeAt ? n.wakeAt : (n.sentAt ?? n.updatedAt))
const byTime = (list: ProactiveNudge[]) => [...list].sort((a, b) => shownAt(b) - shownAt(a))
const DAY_MS = 24 * 3600_000

const STATUS: Record<NudgeStatus, { tone: StatusTone; key: Key }> = {
  pending: { tone: "outline", key: "proactive.status.pending" },
  scheduled: { tone: "outline", key: "proactive.status.scheduled" },
  firing: { tone: "outline", key: "proactive.status.scheduled" },
  sent: { tone: "success", key: "proactive.status.sent" },
  done: { tone: "secondary", key: "proactive.status.done" },
  snoozed: { tone: "secondary", key: "proactive.status.snoozed" },
  dropped: { tone: "secondary", key: "proactive.status.dropped" },
  expired: { tone: "secondary", key: "proactive.status.expired" },
  tracking: { tone: "secondary", key: "proactive.status.dropped" },
}

export function AgentProactive({ agentId }: { agentId: string }) {
  const t = useT()
  const count = useCount()
  const [view, setView] = React.useState<ProactiveView | null>(null)
  const [error, setError] = React.useState<string | null>(null)
  const [checking, setChecking] = React.useState(false)
  const [reload, setReload] = React.useState(0)
  const latest = React.useRef(0)
  // Every request runs after the one before it, so responses arrive in order.
  // `confirmed` is the last view the server sent; a failed save goes back to it.
  const queue = React.useRef<Promise<unknown>>(Promise.resolve())
  const confirmed = React.useRef<ProactiveView | null>(null)
  const timer = React.useRef<number | undefined>(undefined)
  React.useEffect(() => () => window.clearTimeout(timer.current), [])

  const run = React.useCallback((call: () => Promise<ProactiveView>) => {
    const req = ++latest.current
    const p = queue.current.then(call).then((v) => {
      confirmed.current = v
      if (req === latest.current) setView(v)
      return v
    })
    queue.current = p.catch(() => {})
    return { p, req }
  }, [])

  React.useEffect(() => {
    setError(null)
    run(() => getProactiveFn({ data: { agentId } })).p.catch((err: unknown) => setError(errorMessage(err)))
  }, [agentId, reload, run])

  const save = async (input: { enabled?: boolean; chatId?: string; config?: ProactivePatch }) => {
    if (!view) return false
    setView({
      ...view,
      enabled: input.enabled ?? view.enabled,
      chatId: input.chatId ?? view.chatId,
      config: input.config ? merged(view.config, input.config) : view.config,
    })
    const { p, req } = run(() => saveProactiveFn({ data: { agentId, ...input } }))
    try {
      await p
      return true
    } catch (err) {
      if (req === latest.current && confirmed.current) setView(confirmed.current)
      toast.error(errorMessage(err))
      return false
    }
  }

  // The tick runs on the next scheduler pass (within a minute), so the page
  // looks again once after that.
  const checkNow = () => {
    setChecking(true)
    window.clearTimeout(timer.current)
    const { p } = run(() => checkProactiveNowFn({ data: { agentId } }))
    void p.then(
      () => {
        timer.current = window.setTimeout(() => void run(() => getProactiveFn({ data: { agentId } })).p.catch(() => {}), 70_000)
      },
      () => {},
    )
    toast.promise(p, {
      loading: t("proactive.checkingNow"),
      success: t("proactive.checking"),
      error: (err: unknown) => errorMessage(err),
    })
    void p.catch(() => {}).finally(() => setChecking(false))
  }

  const [unmuting, setUnmuting] = React.useState<string | null>(null)
  const unmute = async (subject: string) => {
    setUnmuting(subject)
    try {
      await run(() => unmuteProactiveFn({ data: { agentId, subject } })).p
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setUnmuting(null)
    }
  }

  // Memory actions return the whole view, like the rest.
  const notes = async (call: () => Promise<ProactiveView>) => {
    try {
      await run(call).p
      return true
    } catch (err) {
      toast.error(errorMessage(err))
      return false
    }
  }

  if (error) {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>{t("agents.loadError")}</AlertTitle>
        <AlertDescription className="flex flex-col items-start gap-3 [overflow-wrap:anywhere]">
          {error}
          <Button variant="outline" size="sm" onClick={() => setReload((n) => n + 1)}>
            {t("proactive.retry")}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }
  if (!view) {
    return (
      <div className="flex flex-col gap-6" aria-busy="true">
        <div className="flex items-center justify-between gap-4">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-5 w-9 rounded-full" />
        </div>
        <div className="flex flex-col gap-2">
          <Skeleton className="h-4 w-20" />
          <Skeleton className="h-8 w-full" />
        </div>
        <Separator />
        <Skeleton className="h-5 w-24" />
        <Skeleton className="h-40 w-full rounded-xl" />
      </div>
    )
  }

  const cfg = view.config
  const chatOptions: Option[] = view.chats.map((c) => ({
    value: c.id,
    label: c.channel === "web" ? t("proactive.webChat") : c.title,
  }))
  const lastRun = view.lastRun

  return (
    <div className="flex flex-col gap-6">
      <Section id="status" first label={t("proactive.title")}>
        <FieldGroup>
          <Field orientation="horizontal">
            <FieldContent>
              <FieldLabel htmlFor="proactive-on">{t("proactive.on")}</FieldLabel>
            </FieldContent>
            <Switch
              id="proactive-on"
              checked={view.enabled}
              onCheckedChange={(v) => {
                if (v && !view.chatId) {
                  toast.error(t("proactive.pickChatFirst"))
                  document.getElementById("proactive-chat")?.focus()
                  return
                }
                void save({ enabled: v })
              }}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="proactive-chat">{t("proactive.sendTo")}</FieldLabel>
            <SimpleSelect
              id="proactive-chat"
              value={view.chatId ?? ""}
              options={view.chatId ? chatOptions : [{ value: "", label: t("proactive.pickChat") }, ...chatOptions]}
              onChange={(v) => v && void save({ chatId: v })}
            />
          </Field>
          {view.enabled && (
            <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
              <span className="text-sm text-muted-foreground tabular-nums">
                {view.lastTickAt ? t("proactive.lastCheck", { at: clockTime(view.lastTickAt) }) : t("proactive.noCheckYet")}
                {lastRun && !lastRun.error && lastRun.skipped === "unchanged" ? ` (${t("proactive.lastRunSkipped")})` : ""}
                {view.nextTickAt && !view.pausedReason ? ` · ${t("proactive.nextCheck", { at: clockTime(Math.max(view.nextTickAt, Date.now())) })}` : ""}
              </span>
              <Button variant="outline" size="sm" disabled={checking || !!view.pausedReason} onClick={() => void checkNow()}>
                {checking ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
                {t("proactive.checkNow")}
              </Button>
            </div>
          )}
          {view.pausedReason && (
            <Alert variant="warning">
              <CircleAlertIcon aria-hidden="true" />
              <AlertTitle>{t("proactive.paused")}</AlertTitle>
              <AlertDescription className="[overflow-wrap:anywhere]">{view.pausedReason}</AlertDescription>
            </Alert>
          )}
          {view.enabled && !view.pausedReason && lastRun?.error && (
            <Alert variant="warning">
              <CircleAlertIcon aria-hidden="true" />
              <AlertTitle>{t("proactive.lastRunFailed")}</AlertTitle>
              <AlertDescription className="[overflow-wrap:anywhere]">{lastRun.error}</AlertDescription>
            </Alert>
          )}
        </FieldGroup>
      </Section>

      <Section id="prompt" title={t("proactive.prompt")} description={t("proactive.promptNote")}>
        <PromptEditor key={view.agentId} view={view} save={save} t={t} />
      </Section>

      <Section id="rhythm" title={t("proactive.rhythm")} description={t("proactive.quietNote")}>
        <Rhythm cfg={cfg} save={save} t={t} />
      </Section>

      <Section
        id="memory"
        title={t("proactive.memory")}
        description={t("proactive.memoryNote")}
        action={<span className="text-sm text-muted-foreground tabular-nums">{t("proactive.notesUsed", { n: view.notesChars.toLocaleString(), max: MAX_NOTES_CHARS.toLocaleString() })}</span>}
      >
        <Memory view={view} t={t} count={count} act={notes} agentId={agentId} />
      </Section>

      {view.mutes.length > 0 && (
        <Section id="mutes" title={t("proactive.mutes")}>
          <ItemGroup role="list" aria-label={t("proactive.mutes")} className="gap-1">
            {view.mutes.map((m) => (
              <Item key={m.subject} role="listitem" size="sm" className="px-0">
                <ItemMedia variant="icon">
                  <BellOffIcon aria-hidden="true" className="text-muted-foreground" />
                </ItemMedia>
                <ItemContent>
                  <ItemTitle dir="auto" className="line-clamp-none w-full [overflow-wrap:anywhere]">
                    {m.label}
                  </ItemTitle>
                </ItemContent>
                <ItemActions>
                  <Button variant="outline" size="sm" disabled={unmuting === m.subject} onClick={() => void unmute(m.subject)}>
                    {unmuting === m.subject && <Spinner data-icon="inline-start" />}
                    {t("proactive.unmute")}
                  </Button>
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        </Section>
      )}

      {view.enabled && (
        <>
          <Section
            id="activity"
            title={t("proactive.activity")}
            action={
              <span className="text-sm text-muted-foreground tabular-nums">
                {count(view.ticksToday, "proactive.checksOne", "proactive.checksMany")} · {count(view.sentToday, "proactive.sentOne", "proactive.sentMany")}
              </span>
            }
          >
            {view.activity.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("proactive.activityNone")}</p>
            ) : (
              <ul className="flex flex-col divide-y" aria-label={t("proactive.activity")}>
                {byTime(view.activity).map((n) => (
                  <ActivityRow key={n.id} n={n} t={t} />
                ))}
              </ul>
            )}
          </Section>

          <Section id="cost" title={t("proactive.cost")}>
            <FieldGroup>
              <div className="flex flex-col gap-2">
                <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <span className="text-sm font-medium">{t("agents.usedToday")}</span>
                  <span className="text-sm text-muted-foreground tabular-nums">
                    {view.tokenCap > 0
                      ? t("agents.tokensOfCap", {
                          n: view.tokensToday.toLocaleString(),
                          cap: view.tokenCap.toLocaleString(),
                        })
                      : t("agents.tokens", {
                          n: view.tokensToday.toLocaleString(),
                        })}
                  </span>
                </div>
                {view.tokenCap > 0 && <Progress value={Math.min(100, (view.tokensToday / view.tokenCap) * 100)} aria-label={t("agents.usedToday")} />}
              </div>
              <Field className="@xl/main:max-w-[calc(50%-0.5rem)]">
                <FieldLabel htmlFor="proactive-role">{t("proactive.model")}</FieldLabel>
                <SimpleSelect
                  id="proactive-role"
                  value={cfg.modelRole}
                  options={[
                    { value: "fast", label: "fast" },
                    { value: "default", label: "default" },
                  ]}
                  onChange={(v) =>
                    void save({
                      config: { modelRole: v as ProactiveConfig["modelRole"] },
                    })
                  }
                />
              </Field>
            </FieldGroup>
          </Section>
        </>
      )}
    </div>
  )
}

type Save = (input: { enabled?: boolean; chatId?: string; config?: ProactivePatch }) => Promise<boolean>

function PromptEditor({ view, save, t }: { view: ProactiveView; save: Save; t: T }) {
  const count = useCount()
  const saved = view.config.prompt
  const [draft, setDraft] = React.useState(saved)
  const [saving, setSaving] = React.useState(false)
  const [testing, setTesting] = React.useState(false)
  const [result, setResult] = React.useState<ProactiveTestResult | null>(null)
  // A saved prompt from the server replaces the draft only when there's no edit in progress.
  const lastSaved = React.useRef(saved)
  React.useEffect(() => {
    setDraft((d) => (d === lastSaved.current ? saved : d))
    lastSaved.current = saved
  }, [saved])
  const dirty = draft !== saved
  const mentions = mentionsIn(draft)
  const chars = draft.length
  const tooLong = chars > MAX_PROMPT_CHARS
  const tooMany = mentions.length > MAX_MENTIONS

  const onSave = async () => {
    setSaving(true)
    const ok = await save({ config: { prompt: draft } })
    setSaving(false)
    if (ok) toast.success(t("proactive.saved"))
  }
  const onTest = async () => {
    setTesting(true)
    setResult(null)
    try {
      setResult(await testProactiveFn({ data: { agentId: view.agentId, prompt: draft } }))
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setTesting(false)
    }
  }

  return (
    <FieldGroup>
      <Field>
        <FieldLabel htmlFor="proactive-prompt" className="sr-only">
          {t("proactive.promptLabel")}
        </FieldLabel>
        <MentionTextarea id="proactive-prompt" value={draft} onChange={setDraft} tools={view.tools} t={t} invalid={tooLong || tooMany} />
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-1 text-caption text-muted-foreground tabular-nums">
          <span className={tooMany ? "text-destructive" : undefined}>{count(mentions.length, "proactive.mentionsOne", "proactive.mentionsMany")}</span>
          <span className={tooLong ? "text-destructive" : undefined}>{t("proactive.chars", { n: chars.toLocaleString(), max: MAX_PROMPT_CHARS.toLocaleString() })}</span>
        </div>
      </Field>
      {draft.trim() === "" && (
        <div>
          <Button variant="outline" size="sm" onClick={() => setDraft(view.starter)}>
            {t("proactive.useStarter")}
          </Button>
        </div>
      )}
      {mentions.length > 0 && (
        <ul className="flex flex-col gap-1.5" aria-label={t("proactive.prompt")}>
          {mentions.map((m) => {
            const tool = view.tools.find((x) => x.name === m)
            const problem = problemFor(m, view.tools)
            return (
              <li key={m} className="flex min-w-0 items-start gap-2 text-sm">
                <WrenchIcon aria-hidden="true" className={`mt-0.5 size-4 shrink-0 ${problem ? "text-destructive" : "text-muted-foreground"}`} />
                <span className="min-w-0 [overflow-wrap:anywhere]">
                  <span className="font-mono text-caption">@{m}</span>
                  {tool && !problem && (
                    <span className="text-muted-foreground">
                      {" "}
                      · {tool.connectorLabel}
                      {tool.title ? ` · ${tool.title}` : ""}
                    </span>
                  )}
                  {problem && <span className="text-destructive"> {t(problem)}</span>}
                </span>
              </li>
            )
          })}
        </ul>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={!dirty || saving || tooLong || tooMany} onClick={() => void onSave()}>
          {saving && <Spinner data-icon="inline-start" />}
          {t("proactive.savePrompt")}
        </Button>
        {dirty && (
          <Button variant="ghost" size="sm" disabled={saving} onClick={() => setDraft(saved)}>
            {t("proactive.discard")}
          </Button>
        )}
        <Button variant="outline" size="sm" disabled={testing || mentions.length === 0 || tooLong || tooMany} onClick={() => void onTest()}>
          {testing ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
          {testing ? t("proactive.testing") : t("proactive.test")}
        </Button>
        {dirty && <span className="text-caption text-muted-foreground">{t("proactive.unsaved")}</span>}
      </div>
      <FieldDescription>{t("proactive.testNote")}</FieldDescription>
      {result && <TestResult r={result} t={t} />}
    </FieldGroup>
  )
}

function TestResult({ r, t }: { r: ProactiveTestResult; t: T }) {
  return (
    <Card size="sm" aria-live="polite">
      <CardContent className="flex flex-col gap-3">
        {r.error ? (
          <Alert variant="destructive">
            <CircleAlertIcon aria-hidden="true" />
            <AlertTitle>{t("proactive.testFailed")}</AlertTitle>
            <AlertDescription className="[overflow-wrap:anywhere]">{r.error}</AlertDescription>
          </Alert>
        ) : r.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("proactive.testNothing")}</p>
        ) : (
          <div className="flex flex-col gap-2">
            <h3 className="text-sm font-medium">{t("proactive.testWouldSend")}</h3>
            <ul className="flex flex-col gap-2">
              {r.items.map((it) => (
                <li key={it.key} className="flex min-w-0 flex-col gap-0.5">
                  <span dir="auto" className="text-sm whitespace-pre-line [overflow-wrap:anywhere]">
                    {it.text}
                  </span>
                  <span className="text-caption text-muted-foreground tabular-nums [overflow-wrap:anywhere]">
                    <span className="font-mono">{it.key}</span>
                    {it.sendAt ? ` · ${t("proactive.testAt", { at: `${clockTime(it.sendAt)}${sameDay(it.sendAt) ? "" : ` ${shortDay(it.sendAt)}`}` })}` : ""}
                    {it.urgent ? ` · ${t("proactive.testUrgent")}` : ""}
                    {it.dropped ? ` · ${t("proactive.testDropped", { why: it.dropped })}` : ""}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {r.remember.length > 0 && (
          <div className="flex flex-col gap-1">
            <h3 className="text-sm font-medium">{t("proactive.testRemember")}</h3>
            <ul className="flex list-disc flex-col gap-1 ps-5 text-sm">
              {r.remember.map((n) => (
                <li key={n} dir="auto" className="[overflow-wrap:anywhere]">
                  {n}
                </li>
              ))}
            </ul>
          </div>
        )}
        <p className="text-caption text-muted-foreground tabular-nums">{t("proactive.testCalls", { calls: r.calls.length.toLocaleString(), tokens: r.tokens.toLocaleString() })}</p>
      </CardContent>
    </Card>
  )
}

// The prompt split into plain text and mentions, for the highlight layer
// behind the textarea. A mention with a problem is flagged.
function promptParts(prompt: string, tools: ProactiveTool[]): { text: string; mention?: "ok" | "problem" }[] {
  const out: { text: string; mention?: "ok" | "problem" }[] = []
  let last = 0
  for (const m of prompt.matchAll(MENTION_RE)) {
    const at = (m.index ?? 0) + m[1].length
    if (at > last) out.push({ text: prompt.slice(last, at) })
    out.push({ text: "@" + m[2], mention: problemFor(m[2], tools) ? "problem" : "ok" })
    last = at + m[2].length + 1
  }
  out.push({ text: prompt.slice(last) })
  return out
}

// The textarea styles that decide where a character lands, copied onto the
// mirror caretOffset measures in.
const MIRROR_STYLES = [
  "boxSizing", "fontFamily", "fontSize", "fontWeight", "fontStyle", "fontFeatureSettings", "fontVariationSettings", "letterSpacing", "lineHeight", "textTransform", "textIndent", "wordSpacing", "tabSize",
  "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth",
] as const

// caretOffset is where character index sits inside the textarea's content,
// before scrolling: a hidden copy of the text up to it, wrapped the same way.
function caretOffset(el: HTMLTextAreaElement, index: number): { left: number; top: number; height: number } {
  const style = getComputedStyle(el)
  const mirror = document.createElement("div")
  for (const k of MIRROR_STYLES) mirror.style[k] = style[k]
  Object.assign(mirror.style, { position: "absolute", top: "0", left: "-9999px", visibility: "hidden", whiteSpace: "pre-wrap", overflowWrap: "break-word", width: `${el.clientWidth + parseFloat(style.borderLeftWidth) + parseFloat(style.borderRightWidth)}px` })
  mirror.textContent = el.value.slice(0, index)
  const mark = document.createElement("span")
  mark.textContent = "\u200b"
  mirror.append(mark)
  document.body.append(mirror)
  const out = { left: mark.offsetLeft, top: mark.offsetTop, height: mark.offsetHeight || parseFloat(style.lineHeight) || 20 }
  mirror.remove()
  return out
}

// MentionTextarea is a textarea that suggests tools after "@". It follows the
// combobox pattern on the textbox itself: the list is announced through
// aria-activedescendant, arrows move, Enter or Tab inserts, Escape closes.
// The list sits in the top layer (ui-sdk's bindLayer), so the card's
// overflow can't clip it, and it hangs under the "@" being typed.
function MentionTextarea({ id, value, onChange, tools, t, invalid }: { id: string; value: string; onChange: (v: string) => void; tools: ProactiveTool[]; t: T; invalid: boolean }) {
  const count = useCount()
  const ref = React.useRef<HTMLTextAreaElement>(null)
  const listRef = React.useRef<HTMLDivElement>(null)
  const backRef = React.useRef<HTMLDivElement>(null)
  const parts = React.useMemo(() => promptParts(value, tools), [value, tools])

  // The highlight layer copies the textarea's scroll, and its scrollbar's
  // width so lines wrap at the same place.
  const sync = React.useCallback(() => {
    const box = ref.current
    const back = backRef.current
    if (!box || !back) return
    const style = getComputedStyle(box)
    const bar = box.offsetWidth - box.clientWidth - parseFloat(style.borderLeftWidth) - parseFloat(style.borderRightWidth)
    back.style.paddingRight = `calc(${style.paddingRight} + ${Math.max(0, bar)}px)`
    back.scrollTop = box.scrollTop
    back.scrollLeft = box.scrollLeft
  }, [])
  React.useLayoutEffect(sync, [sync, value])
  React.useEffect(() => {
    const box = ref.current
    if (!box || typeof ResizeObserver === "undefined") return
    const ro = new ResizeObserver(sync)
    ro.observe(box)
    return () => ro.disconnect()
  }, [sync])
  const listId = React.useId()
  const [query, setQuery] = React.useState<{ start: number; text: string } | null>(null)
  const [active, setActive] = React.useState(0)

  // Read tools first (the only ones that can be picked), cut to 8, then
  // grouped by connector so each one gets a single heading.
  const groups = React.useMemo(() => {
    if (!query) return []
    const q = query.text.toLowerCase()
    const hit = (x: ProactiveTool) => !q || x.name.toLowerCase().includes(q) || x.connectorLabel.toLowerCase().includes(q) || (x.title ?? "").toLowerCase().includes(q)
    const list = tools.filter(hit)
    const picked = [...list.filter((x) => x.class === "read"), ...list.filter((x) => x.class !== "read")].slice(0, 8)
    const byConnector = new Map<string, { label: string; tools: ProactiveTool[] }>()
    for (const x of picked) {
      const g = byConnector.get(x.connectorId) ?? { label: x.connectorLabel, tools: [] }
      g.tools.push(x)
      byConnector.set(x.connectorId, g)
    }
    return [...byConnector.entries()].map(([connectorId, g]) => ({ connectorId, ...g }))
  }, [query, tools])
  const options = React.useMemo(() => groups.flatMap((g) => g.tools), [groups])
  const open = query !== null
  const firstRead = options.findIndex((o) => o.class === "read")

  const track = (el: HTMLTextAreaElement) => {
    const before = el.value.slice(0, el.selectionStart ?? 0)
    const m = TYPING_RE.exec(before)
    if (m) {
      const start = before.length - m[2].length - 1
      setQuery((cur) => (cur && cur.start === start && cur.text === m[2] ? cur : { start, text: m[2] }))
    } else {
      setQuery(null)
    }
  }
  React.useEffect(() => setActive(Math.max(0, firstRead)), [query?.text, firstRead])

  const start = query?.start
  React.useLayoutEffect(() => {
    const box = ref.current
    const list = listRef.current
    if (start === undefined || !box || !list) return
    const at = caretOffset(box, start)
    const stopLayer = bindLayer(list, { topLayer: true, contains: (target) => box.contains(target), onDismiss: () => setQuery(null) })
    // Under the "@", kept inside the textarea when that line scrolls away.
    const anchor = {
      getBoundingClientRect: () => {
        const r = box.getBoundingClientRect()
        const x = Math.min(r.left + at.left - box.scrollLeft, r.right - 16)
        const y = Math.min(Math.max(r.top + at.top - box.scrollTop, r.top), r.bottom - at.height)
        return { x, y, left: x, top: y, width: 0, height: at.height, right: x, bottom: y + at.height }
      },
    }
    const stopPosition = position(anchor, list, { side: "bottom", align: "start", sideOffset: 6, collisionPadding: 8 })
    return () => {
      stopPosition()
      stopLayer()
    }
  }, [start])

  const insert = (tool: ProactiveTool) => {
    const el = ref.current
    if (!el || !query || tool.class !== "read") return
    const end = el.selectionStart ?? value.length
    const next = value.slice(0, query.start) + "@" + tool.name + " " + value.slice(end)
    const caret = query.start + tool.name.length + 2
    onChange(next)
    setQuery(null)
    requestAnimationFrame(() => {
      el.focus()
      el.setSelectionRange(caret, caret)
    })
  }

  const move = (dir: 1 | -1) => {
    if (options.length === 0) return
    let i = active
    for (let n = 0; n < options.length; n++) {
      i = (i + dir + options.length) % options.length
      if (options[i].class === "read") break
    }
    setActive(i)
  }

  // Keep the active row in view when the arrows move past the list's edge.
  React.useEffect(() => {
    if (open) document.getElementById(`${listId}-${active}`)?.scrollIntoView?.({ block: "nearest" })
  }, [open, active, listId])

  let index = -1
  return (
    <div className="relative">
      {/* The text is drawn here, with mentions as code chips, and the
          textarea on top keeps its caret and selection but paints no text. */}
      <div
        ref={backRef}
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 overflow-hidden rounded-lg border border-transparent bg-background px-2.5 py-2 font-mono text-sm leading-relaxed [overflow-wrap:break-word] whitespace-pre-wrap text-foreground"
      >
        {parts.map((p, i) =>
          p.mention ? (
            <span key={i} data-mention={p.mention} className={p.mention === "ok" ? "rounded-sm bg-(--primary-subtle) text-brand dark:text-[color-mix(in_lch,var(--brand),white_35%)]" : "rounded-sm bg-destructive/12 text-destructive"}>
              {p.text}
            </span>
          ) : (
            <React.Fragment key={i}>{p.text}</React.Fragment>
          ),
        )}
        {/* A trailing newline needs a character after it to take up a line. */}
        {"\u200b"}
      </div>
      <Textarea
        ref={ref}
        id={id}
        value={value}
        rows={8}
        spellCheck={false}
        aria-invalid={invalid || undefined}
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && options[active] ? `${listId}-${active}` : undefined}
        placeholder={t("proactive.promptPlaceholder")}
        className="relative max-h-[60vh] min-h-40 overflow-y-auto bg-transparent font-mono text-sm leading-relaxed text-transparent"
        onScroll={sync}
        onChange={(e) => {
          onChange(e.target.value)
          track(e.target)
        }}
        onClick={(e) => track(e.currentTarget)}
        onKeyUp={(e) => ["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key) && track(e.currentTarget)}
        onBlur={() => setQuery(null)}
        onKeyDown={(e) => {
          if (!open) return
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault()
            move(e.key === "ArrowDown" ? 1 : -1)
          } else if ((e.key === "Enter" || e.key === "Tab") && options[active]?.class === "read") {
            e.preventDefault()
            insert(options[active])
          } else if (e.key === "Escape") {
            e.preventDefault()
            setQuery(null)
          }
        }}
      />
      {open && (
        // Same surface as the Select popup: no motion, since it opens on
        // every "@" while typing.
        <div
          ref={listRef}
          id={listId}
          role="listbox"
          aria-label={t("proactive.prompt")}
          className="fixed z-50 m-0 flex max-h-[min(20rem,var(--available-height))] w-[min(24rem,calc(100vw-2rem))] flex-col overflow-y-auto overscroll-contain rounded-xl border-0 bg-popover p-1 text-start text-popover-foreground shadow-(--shadow-elevated)"
          onMouseDown={(e) => e.preventDefault()}
        >
          {options.length === 0 ? (
            <div role="presentation" className="px-2 py-1.5 text-sm text-muted-foreground">
              {tools.some((x) => x.class === "read") ? t("proactive.noMatch") : t("proactive.noTools")}
            </div>
          ) : (
            groups.map((g) => (
              <div key={g.connectorId} role="group" aria-labelledby={`${listId}-g-${g.connectorId}`} className="flex flex-col not-first:mt-1">
                <div id={`${listId}-g-${g.connectorId}`} role="presentation" className="truncate px-2 pt-1.5 pb-1 text-xs font-medium text-muted-foreground">
                  {g.label}
                </div>
                {g.tools.map((o) => {
                  const i = ++index
                  const disabled = o.class !== "read"
                  const tag = disabled ? t("proactive.onlyRead") : o.untrusted ? t("proactive.outsideData") : ""
                  return (
                    <div
                      key={o.name}
                      id={`${listId}-${i}`}
                      role="option"
                      aria-selected={i === active}
                      aria-disabled={disabled || undefined}
                      data-highlighted={i === active ? "" : undefined}
                      className="flex min-w-0 cursor-pointer flex-col gap-0.5 rounded-lg px-2 py-1.5 pointer-coarse:py-2.5 select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground aria-disabled:cursor-default aria-disabled:opacity-50"
                      onClick={() => insert(o)}
                      onMouseMove={() => !disabled && i !== active && setActive(i)}
                    >
                      <span className="flex min-w-0 items-baseline justify-between gap-3">
                        <span className="truncate text-sm">{o.title ?? o.tool}</span>
                        {tag && <span className="shrink-0 text-xs text-muted-foreground">{tag}</span>}
                      </span>
                      <span className="truncate font-mono text-xs text-muted-foreground">@{o.name}</span>
                    </div>
                  )
                })}
              </div>
            ))
          )}
        </div>
      )}
      <span className="sr-only" aria-live="polite">
        {open ? count(options.filter((o) => o.class === "read").length, "proactive.suggestionsOne", "proactive.suggestionsMany") : ""}
      </span>
    </div>
  )
}

function Memory({ view, t, count, act, agentId }: { view: ProactiveView; t: T; count: ReturnType<typeof useCount>; act: (call: () => Promise<ProactiveView>) => Promise<boolean>; agentId: string }) {
  const [text, setText] = React.useState("")
  const [busy, setBusy] = React.useState<string | null>(null)
  const add = async () => {
    const v = text.trim()
    if (!v) return
    setBusy("add")
    if (await act(() => addProactiveNoteFn({ data: { agentId, text: v } }))) setText("")
    setBusy(null)
  }
  const now = Date.now()
  return (
    <FieldGroup>
      {view.notes.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("proactive.memoryNone")}</p>
      ) : (
        <ItemGroup role="list" aria-label={t("proactive.memory")} className="gap-1">
          {view.notes.map((n: ProactiveNote) => {
            const days = n.expiresAt ? Math.max(1, Math.ceil((n.expiresAt - now) / DAY_MS)) : 0
            return (
              <Item key={n.id} role="listitem" size="sm" className="px-0">
                <ItemContent>
                  <ItemTitle dir="auto" className="line-clamp-none w-full font-normal [overflow-wrap:anywhere]">
                    {n.text}
                  </ItemTitle>
                  <ItemDescription>{n.source === "owner" ? t("proactive.noteYou") : count(days, "proactive.noteDaysOne", "proactive.noteDaysMany")}</ItemDescription>
                </ItemContent>
                <ItemActions>
                  {n.source === "model" && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t("proactive.keepNote")}
                      title={t("proactive.keep")}
                      disabled={busy === n.id}
                      onClick={async () => {
                        setBusy(n.id)
                        await act(() => editProactiveNoteFn({ data: { agentId, noteId: n.id, keep: true } }))
                        setBusy(null)
                      }}
                    >
                      <BookmarkIcon />
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("proactive.deleteNote")}
                    disabled={busy === n.id}
                    onClick={async () => {
                      setBusy(n.id)
                      await act(() => deleteProactiveNoteFn({ data: { agentId, noteId: n.id } }))
                      setBusy(null)
                    }}
                  >
                    <Trash2Icon />
                  </Button>
                </ItemActions>
              </Item>
            )
          })}
        </ItemGroup>
      )}
      <form
        className="flex min-w-0 flex-col gap-2 @md/main:flex-row"
        onSubmit={(e) => {
          e.preventDefault()
          void add()
        }}
      >
        <Input aria-label={t("proactive.addNote")} placeholder={t("proactive.notePlaceholder")} maxLength={MAX_NOTE_CHARS} value={text} onChange={(e) => setText(e.target.value)} className="min-w-0 flex-1" />
        <Button type="submit" variant="outline" disabled={!text.trim() || busy === "add"}>
          {busy === "add" && <Spinner data-icon="inline-start" />}
          {t("proactive.addNote")}
        </Button>
      </form>
    </FieldGroup>
  )
}

function Rhythm({ cfg, save, t }: { cfg: ProactiveConfig; save: Save; t: T }) {
  const off = { value: "off", label: t("proactive.off") }
  const at = (list: string[]) => list.map((v) => ({ value: v, label: v }))
  const summary = (key: "morning" | "evening", list: string[]) => {
    const s = cfg[key]
    const value = s.on ? s.at : "off"
    return (
      <Field>
        <FieldLabel htmlFor={`proactive-${key}`}>{t(key === "morning" ? "proactive.morning" : "proactive.evening")}</FieldLabel>
        <SimpleSelect
          id={`proactive-${key}`}
          value={value}
          options={withCurrent([off, ...at(list)], value)}
          onChange={(v) =>
            void save({
              config: {
                [key]: v === "off" ? { on: false } : { on: true, at: v },
              },
            })
          }
        />
      </Field>
    )
  }
  const hourly = at(times(0, 23, 60))
  return (
    <FieldGroup>
      <div className="grid grid-cols-1 gap-4 @xl/main:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="proactive-every">{t("proactive.every")}</FieldLabel>
          <SimpleSelect
            id="proactive-every"
            value={String(cfg.checkEveryMin)}
            options={withCurrent(
              [15, 30, 60, 120].map((n) => ({
                value: String(n),
                label: t("proactive.minutes", { n }),
              })),
              String(cfg.checkEveryMin),
            )}
            onChange={(v) => void save({ config: { checkEveryMin: Number(v) } })}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="proactive-max">{t("proactive.maxPerHour")}</FieldLabel>
          <SimpleSelect
            id="proactive-max"
            value={String(cfg.maxPerHour)}
            options={withCurrent(
              Array.from({ length: 10 }, (_, i) => ({
                value: String(i + 1),
                label: t("proactive.perHour", { n: i + 1 }),
              })),
              String(cfg.maxPerHour),
            )}
            onChange={(v) => void save({ config: { maxPerHour: Number(v) } })}
          />
        </Field>
        {summary("morning", times(5, 11, 30))}
        {summary("evening", times(17, 23, 30))}
        <Field>
          <FieldLabel htmlFor="proactive-quiet-start">{t("proactive.quietStart")}</FieldLabel>
          <SimpleSelect id="proactive-quiet-start" value={cfg.quietHours.start} options={withCurrent(hourly, cfg.quietHours.start)} onChange={(v) => void save({ config: { quietHours: { start: v } } })} />
        </Field>
        <Field>
          <FieldLabel htmlFor="proactive-quiet-end">{t("proactive.quietEnd")}</FieldLabel>
          <SimpleSelect id="proactive-quiet-end" value={cfg.quietHours.end} options={withCurrent(hourly, cfg.quietHours.end)} onChange={(v) => void save({ config: { quietHours: { end: v } } })} />
        </Field>
      </div>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldLabel htmlFor="proactive-urgent">{t("proactive.urgentPasses")}</FieldLabel>
        </FieldContent>
        <Switch id="proactive-urgent" checked={cfg.quietHours.urgentPasses} onCheckedChange={(v) => void save({ config: { quietHours: { urgentPasses: v } } })} />
      </Field>
    </FieldGroup>
  )
}

function ActivityRow({ n, t }: { n: ProactiveNudge; t: T }) {
  const Icon = n.kind === "sum_morning" ? SunIcon : n.kind === "sum_evening" ? MoonIcon : MessageCircleIcon
  const s = STATUS[n.status as NudgeStatus] ?? STATUS.pending
  const at = shownAt(n)
  const isSummary = n.kind === "sum_morning" || n.kind === "sum_evening"
  return (
    <li className="flex min-w-0 items-start gap-3 py-2.5 first:pt-0 last:pb-0">
      <span className="flex w-[8.5ch] shrink-0 flex-col pt-0.5 font-mono text-caption text-muted-foreground tabular-nums">
        {clockTime(at)}
        {!sameDay(at) && <span>{shortDay(at)}</span>}
      </span>
      <Icon aria-hidden={true} className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span dir="auto" className="min-w-0 text-sm [overflow-wrap:anywhere]">
          {isSummary ? t(n.kind === "sum_morning" ? "proactive.morning" : "proactive.evening") : n.label}
        </span>
        <StatusText tone={s.tone} className="@xl/main:hidden">
          {t(s.key)}
        </StatusText>
        {(n.status === "dropped" || n.status === "pending") && n.reason && <span className="min-w-0 text-caption text-muted-foreground [overflow-wrap:anywhere]">{n.reason}</span>}
      </span>
      <StatusText tone={s.tone} className="hidden pt-0.5 @xl/main:inline-flex">
        {t(s.key)}
      </StatusText>
    </li>
  )
}

// A section with a visible heading, or (label) only a name for assistive tech,
// for the first one: the tab already says "Proactive".
function Section({ id, first, title, label, description, action, children }: { id: string; first?: boolean; title?: string; label?: string; description?: string; action?: React.ReactNode; children: React.ReactNode }) {
  if (!title) {
    return (
      <section aria-label={label} className="flex flex-col gap-4">
        {children}
      </section>
    )
  }
  return (
    <>
      {!first && <Separator />}
      <section aria-labelledby={`proactive-section-${id}`} className="flex flex-col gap-4">
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
          <div className="flex min-w-0 flex-col gap-1">
            <h2 id={`proactive-section-${id}`} className="text-heading">
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


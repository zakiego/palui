// The agent page's Proactive tab (docs/rfc/proactive.md 12). Every control
// saves on its own, like the Tools tab: a switch or a select saves as it
// changes, the account field when it loses focus.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { BellOffIcon, CalendarClockIcon, CalendarIcon, CircleAlertIcon, MessageCircleIcon, RefreshCwIcon, StarIcon, SunIcon, MoonIcon, VideoIcon, XIcon, CarIcon } from "lucide-react"
import { toast } from "sonner"

import { WhatsAppIcon } from "@/components/brand-icons"
import { SimpleSelect, type Option } from "@/components/simple-select"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Item, ItemActions, ItemContent, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Field, FieldContent, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { StatusText, type StatusTone } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import {
  checkProactiveNowFn,
  getProactiveFn,
  saveProactiveFn,
  unmuteProactiveFn,
  type NudgeKind,
  type NudgeStatus,
  type ProactiveConfig,
  type ProactiveNudge,
  type ProactivePatch,
  type ProactiveView,
} from "@/lib/api/proactive"
import { clockTime, errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"
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

const shownAt = (n: ProactiveNudge) => (n.status === "scheduled" && n.wakeAt ? n.wakeAt : (n.sentAt ?? n.updatedAt))
const byTime = (list: ProactiveNudge[]) => [...list].sort((a, b) => shownAt(b) - shownAt(a))

const KIND_ICON: Record<NudgeKind, React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>> = {
  cal_depart: CarIcon,
  cal_start: VideoIcon,
  cal_changed: CalendarClockIcon,
  wa_waiting: WhatsAppIcon,
  sum_morning: SunIcon,
  sum_evening: MoonIcon,
}

const STATUS: Record<NudgeStatus, { tone: StatusTone; key: Key }> = {
  pending: { tone: "outline", key: "proactive.status.pending" },
  scheduled: { tone: "outline", key: "proactive.status.scheduled" },
  firing: { tone: "outline", key: "proactive.status.scheduled" },
  sent: { tone: "success", key: "proactive.status.sent" },
  done: { tone: "secondary", key: "proactive.status.done" },
  snoozed: { tone: "secondary", key: "proactive.status.snoozed" },
  dropped: { tone: "secondary", key: "proactive.status.dropped" },
  expired: { tone: "secondary", key: "proactive.status.expired" },
}

export function AgentProactive({ agentId }: { agentId: string }) {
  const t = useT()
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
    if (!view) return
    setView({
      ...view,
      enabled: input.enabled ?? view.enabled,
      chatId: input.chatId ?? view.chatId,
      config: input.config ? merged(view.config, input.config) : view.config,
    })
    const { p, req } = run(() => saveProactiveFn({ data: { agentId, ...input } }))
    try {
      await p
    } catch (err) {
      if (req === latest.current && confirmed.current) setView(confirmed.current)
      toast.error(errorMessage(err))
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
        <Skeleton className="h-24 w-full rounded-xl" />
        <Skeleton className="h-24 w-full rounded-xl" />
      </div>
    )
  }

  const cfg = view.config
  const chatOptions: Option[] = view.chats.map((c) => ({
    value: c.id,
    label: c.channel === "web" ? t("proactive.webChat") : c.title,
  }))
  const contactSubjects = new Set(cfg.contacts.map((c) => c.subject))
  const toggleContact = (n: ProactiveNudge) => {
    const contacts = contactSubjects.has(n.subject) ? cfg.contacts.filter((c) => c.subject !== n.subject) : [...cfg.contacts, { subject: n.subject, label: n.label }]
    void save({ config: { contacts } })
  }

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
        </FieldGroup>
      </Section>

      <Section id="sources" title={t("proactive.sources")} description={t("proactive.sourcesNote")}>
        <Sources view={view} save={save} t={t} />
      </Section>

      {view.enabled && (
        <>
          <Section id="rhythm" title={t("proactive.rhythm")}>
            <Rhythm cfg={cfg} save={save} t={t} />
          </Section>

          <Section id="contacts" title={t("proactive.contacts")}>
            <FieldGroup>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="proactive-auto">{t("proactive.contactsAuto")}</FieldLabel>
                </FieldContent>
                <Switch id="proactive-auto" checked={cfg.contactsAuto} onCheckedChange={(v) => void save({ config: { contactsAuto: v } })} />
              </Field>
              {cfg.contacts.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("proactive.contactsNone")}</p>
              ) : (
                <ItemGroup role="list" aria-label={t("proactive.contacts")} className="gap-1">
                  {cfg.contacts.map((c) => (
                    <Item key={c.subject} role="listitem" size="sm" className="px-0">
                      <ItemMedia variant="icon">
                        <StarIcon aria-hidden="true" className="fill-current text-muted-foreground" />
                      </ItemMedia>
                      <ItemContent>
                        <ItemTitle dir="auto" className="line-clamp-none w-full [overflow-wrap:anywhere]">
                          {c.label}
                        </ItemTitle>
                      </ItemContent>
                      <ItemActions>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          aria-label={t("proactive.removeContact", {
                            name: c.label,
                          })}
                          onClick={() =>
                            void save({
                              config: {
                                contacts: cfg.contacts.filter((x) => x.subject !== c.subject),
                              },
                            })
                          }
                        >
                          <XIcon />
                        </Button>
                      </ItemActions>
                    </Item>
                  ))}
                </ItemGroup>
              )}
            </FieldGroup>
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

          <Section
            id="activity"
            title={t("proactive.activity")}
            action={
              <span className="text-sm text-muted-foreground tabular-nums">
                {t("proactive.activityCounts", {
                  checks: view.ticksToday.toLocaleString(),
                  sent: view.sentToday.toLocaleString(),
                })}
              </span>
            }
          >
            {view.activity.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("proactive.activityNone")}</p>
            ) : (
              <ul className="flex flex-col divide-y" aria-label={t("proactive.activity")}>
                {byTime(view.activity).map((n) => (
                  <ActivityRow key={n.id} n={n} t={t} important={contactSubjects.has(n.subject)} onImportant={() => toggleContact(n)} />
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

type Save = (input: { enabled?: boolean; chatId?: string; config?: ProactivePatch }) => Promise<void>

function Sources({ view, save, t }: { view: ProactiveView; save: Save; t: T }) {
  const cfg = view.config
  const cal = cfg.calendar
  const wa = cfg.whatsapp
  const off = { value: "", label: t("proactive.off") }
  const calOptions: Option[] = [off, ...view.connectors.calendar.map((c) => ({ value: c.id, label: c.label }))]
  const waOptions: Option[] = [off, ...view.connectors.whatsapp.map((c) => ({ value: c.id, label: c.label }))]
  const minutes = (ns: number[]) =>
    ns.map((n) => ({
      value: String(n),
      label: n === 0 ? t("proactive.atStart") : t("proactive.minutesBefore", { n }),
    }))
  const hours = (ns: number[]) => ns.map((n) => ({ value: String(n), label: t("proactive.hours", { n }) }))
  const [account, setAccount] = React.useState(wa?.account ?? "")
  React.useEffect(() => setAccount(wa?.account ?? ""), [wa?.account])
  const noConnectors = view.connectors.calendar.length === 0 && view.connectors.whatsapp.length === 0

  return (
    <FieldGroup>
      {noConnectors && (
        <p className="text-sm text-muted-foreground">
          {t("proactive.noConnectors")}{" "}
          <Link to="/connectors/new" className="underline underline-offset-4">
            {t("proactive.addConnector")}
          </Link>
        </p>
      )}
      {(["calendar", "whatsapp"] as const).map((kind) =>
        view.lastErrors[kind] ? (
          <Alert key={kind} variant="warning">
            <CircleAlertIcon aria-hidden="true" />
            <AlertTitle>{t(kind === "calendar" ? "proactive.calendarFailed" : "proactive.whatsappFailed")}</AlertTitle>
            <AlertDescription className="[overflow-wrap:anywhere]">{view.lastErrors[kind]}</AlertDescription>
          </Alert>
        ) : null,
      )}

      <Card size="sm">
        <CardContent className="flex flex-col gap-4">
          <Field>
            <FieldLabel htmlFor="proactive-cal" className="gap-2">
              <CalendarIcon aria-hidden="true" className="size-4 text-muted-foreground" />
              {t("proactive.calendar")}
            </FieldLabel>
            <SimpleSelect
              id="proactive-cal"
              value={cal?.connectorId ?? ""}
              options={withCurrent(calOptions, cal?.connectorId ?? "")}
              onChange={(v) =>
                void save({
                  config: {
                    calendar: v ? { ...(cal ?? view.defaults.calendar), connectorId: v } : null,
                  },
                })
              }
            />
          </Field>
          {cal && (
            <>
              <div className="grid grid-cols-1 gap-4 @xl/main:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="proactive-depart">{t("proactive.departLead")}</FieldLabel>
                  <SimpleSelect
                    id="proactive-depart"
                    value={String(cal.departLeadMin)}
                    options={withCurrent(minutes([0, 15, 30, 45, 60, 90, 120]), String(cal.departLeadMin))}
                    onChange={(v) =>
                      void save({
                        config: { calendar: { departLeadMin: Number(v) } },
                      })
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="proactive-online">{t("proactive.onlineLead")}</FieldLabel>
                  <SimpleSelect
                    id="proactive-online"
                    value={String(cal.onlineLeadMin)}
                    options={withCurrent(minutes([0, 5, 10, 15, 30]), String(cal.onlineLeadMin))}
                    onChange={(v) =>
                      void save({
                        config: { calendar: { onlineLeadMin: Number(v) } },
                      })
                    }
                  />
                </Field>
              </div>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="proactive-changes">{t("proactive.notifyChanges")}</FieldLabel>
                </FieldContent>
                <Switch id="proactive-changes" checked={cal.notifyChanges} onCheckedChange={(v) => void save({ config: { calendar: { notifyChanges: v } } })} />
              </Field>
            </>
          )}
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent className="flex flex-col gap-4">
          <Field>
            <FieldLabel htmlFor="proactive-wa" className="gap-2">
              <WhatsAppIcon aria-hidden="true" className="size-4 text-muted-foreground" />
              {t("proactive.whatsapp")}
            </FieldLabel>
            <SimpleSelect
              id="proactive-wa"
              value={wa?.connectorId ?? ""}
              options={withCurrent(waOptions, wa?.connectorId ?? "")}
              onChange={(v) =>
                void save({
                  config: {
                    whatsapp: v ? { ...(wa ?? view.defaults.whatsapp), connectorId: v } : null,
                  },
                })
              }
            />
          </Field>
          {wa && (
            <>
              <div className="grid grid-cols-1 gap-4 @xl/main:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="proactive-wait">{t("proactive.waitHours")}</FieldLabel>
                  <SimpleSelect
                    id="proactive-wait"
                    value={String(wa.waitHours)}
                    options={withCurrent(hours([1, 2, 3, 4, 6, 8, 12, 24]), String(wa.waitHours))}
                    onChange={(v) =>
                      void save({
                        config: { whatsapp: { waitHours: Number(v) } },
                      })
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="proactive-escalate">{t("proactive.escalate")}</FieldLabel>
                  <SimpleSelect
                    id="proactive-escalate"
                    value={String(wa.escalateHours)}
                    options={withCurrent([{ value: "0", label: t("proactive.never") }, ...hours([1, 2, 4, 6, 8, 12, 24])], String(wa.escalateHours))}
                    onChange={(v) =>
                      void save({
                        config: { whatsapp: { escalateHours: Number(v) } },
                      })
                    }
                  />
                </Field>
              </div>
              <Field className="@xl/main:max-w-[calc(50%-0.5rem)]">
                <FieldLabel htmlFor="proactive-account">{t("proactive.account")}</FieldLabel>
                <Input
                  id="proactive-account"
                  className="font-mono"
                  spellCheck={false}
                  autoCapitalize="none"
                  autoComplete="off"
                  maxLength={32}
                  value={account}
                  onChange={(e) => setAccount(e.target.value)}
                  onBlur={() => {
                    const v = account.trim()
                    if (!v) setAccount(wa.account)
                    else if (v !== wa.account) void save({ config: { whatsapp: { account: v } } })
                  }}
                  onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
                />
              </Field>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="proactive-groups">{t("proactive.skipGroups")}</FieldLabel>
                </FieldContent>
                <Switch id="proactive-groups" checked={wa.skipGroups} onCheckedChange={(v) => void save({ config: { whatsapp: { skipGroups: v } } })} />
              </Field>
            </>
          )}
        </CardContent>
      </Card>
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

function ActivityRow({ n, t, important, onImportant }: { n: ProactiveNudge; t: T; important: boolean; onImportant: () => void }) {
  const Icon = KIND_ICON[n.kind] ?? MessageCircleIcon
  const s = STATUS[n.status] ?? STATUS.pending
  const at = shownAt(n)
  const isSummary = n.kind === "sum_morning" || n.kind === "sum_evening"
  return (
    <li className="flex min-w-0 items-start gap-3 py-2.5 first:pt-0 last:pb-0">
      <span className="w-[8.5ch] shrink-0 pt-0.5 font-mono text-caption text-muted-foreground tabular-nums">{clockTime(at)}</span>
      <Icon aria-hidden={true} className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span dir="auto" className="min-w-0 text-sm [overflow-wrap:anywhere]">
          {isSummary ? t(n.kind === "sum_morning" ? "proactive.morning" : "proactive.evening") : n.label}
          {n.escalatedAt && <span className="text-muted-foreground"> · {t("proactive.escalated")}</span>}
        </span>
        <StatusText tone={s.tone} className="@xl/main:hidden">
          {t(s.key)}
        </StatusText>
        {n.status === "dropped" && n.reason && <span className="min-w-0 text-caption text-muted-foreground [overflow-wrap:anywhere]">{n.reason}</span>}
      </span>
      <StatusText tone={s.tone} className="hidden pt-0.5 @xl/main:inline-flex">
        {t(s.key)}
      </StatusText>
      {n.kind === "wa_waiting" && (
        <Button variant="ghost" size="icon-sm" className="-my-1" aria-pressed={important} aria-label={t("proactive.markImportant", { name: n.label })} onClick={onImportant}>
          <StarIcon className={important ? "fill-current text-foreground" : undefined} />
        </Button>
      )}
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

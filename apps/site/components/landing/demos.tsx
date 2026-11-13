"use client"

import { Bell, Brain, CalendarDays, Check, Cpu, Globe, type LucideIcon, MessagesSquare, NotebookPen, Puzzle, Send, Wrench } from "lucide-react"
import { typeTime } from "@/lib/mascot"
import { AgentMark, Approval, Composer, Status, type StepState } from "./demo-parts"
import { usePlayer, useTyped } from "./use-player"

// The looping demos inside the landing's boxes. Each one is a small script on
// usePlayer: it starts from its first step, holds while it's offscreen, and
// with reduced motion draws one finished picture. Each is a single image to a
// screen reader, with what it shows as the text alternative.

/* ------------------------------------------------------------- thread */
// The chat you'd have with Palui: you type an ask, it uses one tool (and asks
// first when the tool changes something), then it answers.

type Exchange = { ask: string; icon: LucideIcon; tool: string; detail: string; approval?: boolean; reply: string }

const EXCHANGES: Exchange[] = [
  { ask: "My passport expires in March", icon: NotebookPen, tool: "Memory", detail: "save a note", reply: "Noted. I’ll bring it up before March." },
  { ask: "Remind me at 9 to pay the bill", icon: Bell, tool: "Reminders", detail: "tomorrow 09:00", reply: "Okay, I’ll ping you tomorrow at 9." },
  { ask: "Move my dentist to Friday", icon: CalendarDays, tool: "Calendar", detail: "Fri 10:00", approval: true, reply: "Done. Dentist is on Friday at 10." },
]

const THREAD_ORDER = ["type", "sent", "work", "ask", "press", "done", "reply", "clear"] as const
type ThreadPhase = (typeof THREAD_ORDER)[number]

const THREAD_BEATS = EXCHANGES.flatMap((x, exchange) => {
  const phases: [ThreadPhase, number][] = [
    ["type", typeTime(x.ask) + 350],
    ["sent", 450],
    ["work", 900],
    ...(x.approval ? ([["ask", 1300], ["press", 260]] as [ThreadPhase, number][]) : []),
    ["done", 450],
    ["reply", 2600],
    ["clear", 320],
  ]
  return phases.map(([phase, wait]) => ({ exchange, phase, wait }))
})
const THREAD_WAITS = THREAD_BEATS.map((b) => b.wait)

export function ThreadDemo() {
  const player = usePlayer(THREAD_WAITS)
  const beat = player.still ? { exchange: 0, phase: "reply" as ThreadPhase } : THREAD_BEATS[player.index]
  const x = EXCHANGES[beat.exchange]
  const at = (phase: ThreadPhase) => THREAD_ORDER.indexOf(beat.phase) >= THREAD_ORDER.indexOf(phase)
  const typed = useTyped(x.ask, beat.phase === "type" && player.running)
  const asking = beat.phase === "ask" || beat.phase === "press"

  return (
    <div
      className="lp-inset lp-thread"
      ref={player.ref}
      role="img"
      aria-label="A chat with Palui. You ask it to remember a date, to remind you about a bill and to move a calendar event. It uses one tool each time, asks before it changes your calendar, then answers."
    >
      <div className="lp-thread-head">
        <AgentMark />
        Palui
      </div>
      <div key={beat.exchange} className="lp-thread-body" data-clearing={beat.phase === "clear" ? "" : undefined}>
        {at("sent") ? <p className="lp-bubble lp-bubble-me">{x.ask}</p> : null}
        {at("work") ? (
          <div className="lp-step">
            <x.icon aria-hidden="true" />
            <span className="lp-step-tool">{x.tool}</span>
            <span>{x.detail}</span>
            {asking ? <Approval pressed={beat.phase === "press"} /> : <Status state={at("done") ? "done" : "active"} />}
          </div>
        ) : null}
        {at("reply") ? <p className="lp-bubble lp-bubble-agent">{x.reply}</p> : null}
      </div>
      <Composer text={typed} placeholder="Message Palui" />
    </div>
  )
}

/* ---------------------------------------------------------------- run */
// One task as Palui works through it: each tool it reaches for, which ones
// only read and which one changes something, and the pause where it asks.

// `result` is what a step shows once it is done, when that differs from what it set out to do.
type RunStep = { icon: LucideIcon; name: string; action: string; result?: string; kind: "read" | "write" | "reply" }
type RunTask = { title: string; meta: string; alt: string; steps: RunStep[] }
type RunPhase = "start" | "active" | "ask" | "press" | "hold" | "clear"
type RunBeat = { step: number; phase: RunPhase; wait: number }

function script(task: RunTask) {
  const beats: RunBeat[] = [
    { step: -1, phase: "start", wait: 700 },
    ...task.steps.flatMap((s, step): RunBeat[] => [
      { step, phase: "active", wait: 800 },
      ...(s.kind === "write"
        ? ([
            { step, phase: "ask", wait: 1400 },
            { step, phase: "press", wait: 260 },
          ] satisfies RunBeat[])
        : []),
    ]),
    { step: task.steps.length, phase: "hold", wait: 2800 },
    { step: task.steps.length, phase: "clear", wait: 320 },
  ]
  return { task, beats, waits: beats.map((b) => b.wait) }
}

const RUNS = {
  book: script({
    title: "Book the dentist for Friday",
    meta: "from your chat",
    alt: "Palui working through a task step by step. It reads your memory and your calendar on its own, stops to ask before it adds an event, then replies that the dentist is booked for Friday at 10.",
    steps: [
      { icon: NotebookPen, name: "Memory", action: "find the dentist’s details", kind: "read" },
      { icon: CalendarDays, name: "Calendar", action: "look for a free slot", kind: "read" },
      { icon: CalendarDays, name: "Calendar", action: "add Dentist, Fri 10:00", kind: "write" },
      { icon: Send, name: "Reply", action: "answer in your chat", result: "Booked. Friday at 10.", kind: "reply" },
    ],
  }),
  brief: script({
    title: "Morning brief",
    meta: "every day 07:00",
    alt: "A scheduled job running on its own. Palui reads today's calendar, checks which reminders are due, fetches the weather, then sends you a one-line brief.",
    steps: [
      { icon: CalendarDays, name: "Calendar", action: "read today’s events", kind: "read" },
      { icon: Bell, name: "Reminders", action: "check what’s due", kind: "read" },
      { icon: Globe, name: "Web", action: "fetch the weather", kind: "read" },
      { icon: Send, name: "Reply", action: "send it to your chat", result: "Two meetings, one bill, rain at 4.", kind: "reply" },
    ],
  }),
}

export function RunDemo({ run }: { run: keyof typeof RUNS }) {
  const { task, beats, waits } = RUNS[run]
  const player = usePlayer(waits)
  const beat: RunBeat = player.still ? { step: task.steps.length, phase: "hold", wait: 0 } : beats[player.index]

  return (
    <div className="lp-inset lp-run" ref={player.ref} role="img" aria-label={task.alt}>
      <div className="lp-run-body" data-clearing={beat.phase === "clear" ? "" : undefined}>
        <div className="lp-run-head">
          <span className="lp-run-title">{task.title}</span>
          <span className="lp-run-meta">{task.meta}</span>
        </div>
        <ol className="lp-run-list">
          {task.steps.map((step, i) => {
            const state: StepState = i < beat.step ? "done" : i === beat.step ? "active" : "pending"
            const asking = i === beat.step && (beat.phase === "ask" || beat.phase === "press")
            return (
              <li key={`${step.name}:${step.action}`} className="lp-run-row" data-state={state} data-kind={step.kind}>
                <Status state={asking ? "pending" : state} />
                <step.icon className="lp-run-icon" aria-hidden="true" />
                <span className="lp-run-text">
                  <span className="lp-run-name">{step.name}</span>
                  <span>{state === "done" ? (step.result ?? step.action) : step.action}</span>
                </span>
                {step.kind === "reply" ? null : <span className="lp-run-kind">{step.kind}</span>}
                {step.kind === "write" ? (
                  <span className="lp-run-gate">{asking ? <Approval pressed={beat.phase === "press"} /> : state === "done" ? "You approved" : "Asks you first"}</span>
                ) : null}
              </li>
            )
          })}
        </ol>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------- memory */
// A few things you told it once. They take turns lighting up, the way one
// comes back when you ask about it later.

const NOTES = ["Passport expires in March", "Mom’s birthday is 12 May", "The spare key is with the neighbor", "No shrimp, I’m allergic"]
const NOTE_WAITS = NOTES.map(() => 1900)

export function MemoryDemo() {
  const player = usePlayer<HTMLUListElement>(NOTE_WAITS)
  return (
    <ul className="lp-notes" ref={player.ref} role="img" aria-label="Four things Palui remembers for you, such as when your passport expires and where the spare key is. Each lights up in turn.">
      {NOTES.map((note, i) => (
        <li key={note} className="lp-note" data-on={!player.still && i === player.index ? "" : undefined}>
          <NotebookPen aria-hidden="true" />
          {note}
        </li>
      ))}
    </ul>
  )
}

/* ----------------------------------------------------------- reminder */
// A reminder as it lands in your chat, with its own buttons. The picture
// snoozes it for an hour.

const SNOOZES = ["10m", "1h", "Tomorrow"]
const REMINDER_BEATS = [
  { phase: "ring", wait: 2400 },
  { phase: "press", wait: 260 },
  { phase: "snoozed", wait: 2400 },
  { phase: "clear", wait: 320 },
] as const
const REMINDER_WAITS = REMINDER_BEATS.map((b) => b.wait)

export function ReminderDemo() {
  const player = usePlayer(REMINDER_WAITS)
  const phase = player.still ? "ring" : REMINDER_BEATS[player.index].phase
  const snoozed = phase === "snoozed" || phase === "clear"

  return (
    <div className="lp-inset lp-reminder" ref={player.ref} role="img" aria-label="A reminder to pay the power bill, due at 9. It has buttons for done and for snoozing ten minutes, an hour or until tomorrow, and it gets snoozed to 10.">
      <div className="lp-reminder-body" data-clearing={phase === "clear" ? "" : undefined}>
        <div className="lp-reminder-top">
          <span className="lp-bell" data-ringing={phase === "ring" && !player.still ? "" : undefined}>
            <Bell aria-hidden="true" />
          </span>
          <span className="lp-reminder-title">Pay the power bill</span>
          <span className="lp-reminder-time">{snoozed ? "10:00" : "09:00"}</span>
        </div>
        {snoozed ? (
          <p className="lp-reminder-note">
            <Check aria-hidden="true" />
            Snoozed for an hour
          </p>
        ) : (
          <div className="lp-reminder-actions">
            <span className="lp-btn lp-btn-yes">Done</span>
            {SNOOZES.map((label) => (
              <span key={label} className="lp-btn lp-btn-no" data-pressed={label === "1h" && phase === "press" ? "" : undefined}>
                {label}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

/* --------------------------------------------------------- connectors */
// The five kinds of connector, switching on one after another.

const KINDS: { icon: LucideIcon; name: string }[] = [
  { icon: MessagesSquare, name: "Channels" },
  { icon: Cpu, name: "Models" },
  { icon: Brain, name: "Memory" },
  { icon: Wrench, name: "Tools" },
  { icon: Puzzle, name: "Skills" },
]
// One beat with everything off, one per switch, a hold, then all off again.
const KIND_WAITS = [700, ...KINDS.map(() => 420), 2800, 500]

export function ConnectorsDemo() {
  const player = usePlayer<HTMLUListElement>(KIND_WAITS)
  const on = player.still ? KINDS.length : player.index > KINDS.length + 1 ? 0 : Math.min(player.index, KINDS.length)

  return (
    <ul className="lp-kinds" ref={player.ref} role="img" aria-label="The five kinds of connector: channels, models, memory, tools and skills. Their switches turn on one after another.">
      {KINDS.map((kind, i) => (
        <li key={kind.name} className="lp-kind" data-on={i < on ? "" : undefined}>
          <kind.icon aria-hidden="true" />
          <span className="lp-kind-name">{kind.name}</span>
          <span className="lp-switch" />
        </li>
      ))}
    </ul>
  )
}

/* ----------------------------------------------------------- terminal */
// The command that starts it, typed out, then both containers healthy, then
// the command that makes you its owner. Both are from the getting started page.

const COMMAND = "docker compose up -d --build --wait"
const CONTAINERS = ["palui-app-1", "palui-runtime-1"]
const INVITE = "docker compose exec app palui owner invite"
// Beats: the command typed, one per container, the invite typed, a hold, a clear.
const TERMINAL_WAITS = [typeTime(COMMAND) + 350, ...CONTAINERS.map(() => 520), typeTime(INVITE) + 350, 3000, 320]
const INVITE_STEP = CONTAINERS.length + 1

export function TerminalDemo() {
  const player = usePlayer(TERMINAL_WAITS)
  const step = player.still ? INVITE_STEP + 1 : player.index
  const typed = useTyped(COMMAND, step === 0 && player.running)
  const invite = useTyped(INVITE, step === INVITE_STEP && player.running)
  const clearing = step === TERMINAL_WAITS.length - 1

  return (
    <div className="lp-inset lp-term" ref={player.ref} role="img" aria-label="A terminal. The command docker compose up starts Palui, both of its containers report healthy, and a second command makes the owner's invite link.">
      <div className="lp-term-body" data-clearing={clearing ? "" : undefined}>
        <p className="lp-term-line">
          <span className="lp-term-prompt">$</span>
          <span className="lp-term-cmd">{step === 0 ? typed : COMMAND}</span>
        </p>
        {CONTAINERS.map((name, i) =>
          step > i ? (
            <p key={name} className="lp-term-line lp-term-out">
              <Check aria-hidden="true" />
              Container {name}
              <span className="lp-term-ok">Healthy</span>
            </p>
          ) : null,
        )}
        {step >= INVITE_STEP ? (
          <p className="lp-term-line">
            <span className="lp-term-prompt">$</span>
            <span className="lp-term-cmd">{step === INVITE_STEP ? invite : INVITE}</span>
          </p>
        ) : null}
      </div>
    </div>
  )
}

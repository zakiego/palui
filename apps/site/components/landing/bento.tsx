import type { ReactNode } from "react"
import { ConnectorsDemo, MemoryDemo, ReminderDemo, RunDemo, TerminalDemo, ThreadDemo } from "./demos"

// The boxes: one thing Palui does per box, each with a title, one line and a
// live demo. They are cells of one frame, split by hairlines, not cards.
function Cell({ area, title, text, children }: { area: string; title: string; text: string; children: ReactNode }) {
  return (
    <article className="lp-card" data-area={area}>
      <div className="lp-card-head">
        <h3 className="lp-card-title">{title}</h3>
        <p className="lp-card-text">{text}</p>
      </div>
      <div className="lp-card-demo">{children}</div>
    </article>
  )
}

export function Bento() {
  return (
    <div className="lp-bento">
      <Cell area="chat" title="It lives in your chat" text="Ask it the way you’d text a friend">
        <ThreadDemo />
      </Cell>
      <Cell area="ask" title="It asks before it acts" text="One tap to approve or deny">
        <RunDemo run="book" />
      </Cell>
      <Cell area="memory" title="It remembers" text="Tell it once and it keeps it for later">
        <MemoryDemo />
      </Cell>
      <Cell area="remind" title="It reminds you on time" text="Done or snooze, right from the message">
        <ReminderDemo />
      </Cell>
      <Cell area="connect" title="Everything is a connector" text="Switch any part on or off, any time">
        <ConnectorsDemo />
      </Cell>
      <Cell area="jobs" title="It works while you sleep" text="Scheduled jobs run on their own and report back">
        <RunDemo run="brief" />
      </Cell>
      <Cell area="host" title="It runs on your server" text="Two commands on any Docker host">
        <TerminalDemo />
      </Cell>
    </div>
  )
}

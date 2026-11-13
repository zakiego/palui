"use client"

import { Bell, CalendarClock, CalendarDays, Globe, type LucideIcon, NotebookPen, Wrench } from "lucide-react"
import { memo, useRef } from "react"
import { KEEPS, STAGE_BEATS, type StagePhase } from "@/lib/mascot"
import { AgentMark, Composer } from "./demo-parts"
import { MASCOT_VIEWBOX, MascotScene } from "./mascot-scene"
import { useGaze } from "./use-gaze"
import { usePlayer, useTyped } from "./use-player"

// The hero's visual. An ask types itself into the box, and once the line is
// written Palui turns to the matching thing on its slab, which acts, and then
// Palui answers (lib/mascot.ts has the script). Around it sit the tools it
// can reach, each wired to Palui by a thin line. The tool the current ask
// needs brightens while its thing acts, and a light runs down its line. The
// whole stage is one image to a screen reader.

// `keep` is the thing on the slab (lib/mascot.ts) this tool belongs to, if any.
type Tile = { id: string; label: string; icon: LucideIcon; at: [number, number]; path: string; keep?: number }

// The art box around the mascot is a fixed 860 by 320, so tiles and lines are
// in its own pixels and always meet. It only shows where there is room for it.
const ART = { width: 860, height: 320 }
const TILES: Tile[] = [
  { id: "memory", label: "Memory", icon: NotebookPen, at: [77, 51], path: "M77 51C200 51 230 160 318 172", keep: 0 },
  { id: "reminders", label: "Reminders", icon: Bell, at: [34, 166], path: "M34 166C130 166 220 182 318 182", keep: 1 },
  { id: "web", label: "Web", icon: Globe, at: [95, 282], path: "M95 282C210 282 240 200 318 192" },
  { id: "calendar", label: "Calendar", icon: CalendarDays, at: [783, 51], path: "M783 51C660 51 630 160 542 172", keep: 2 },
  { id: "jobs", label: "Jobs", icon: CalendarClock, at: [826, 166], path: "M826 166C730 166 640 182 542 182" },
  { id: "tools", label: "Your tools", icon: Wrench, at: [765, 282], path: "M765 282C650 282 620 200 542 192" },
]

const WAITS = STAGE_BEATS.map((beat) => beat.wait)

const ALT =
  "Palui, a blocky figure in a kopiah cap, behind a stack of notes, an alarm clock and a toolbox, with the tools it can reach around it. Asks type in above it: remember a date, set a reminder, move a calendar event. Palui turns to the matching thing, which acts, and then answers."

// The drawing only changes when a different thing takes the stage, not on
// every typed character.
const Scene = memo(MascotScene)

export function HeroStage() {
  const player = usePlayer(WAITS)
  const figure = useRef<SVGSVGElement>(null)
  // Reduced motion gets the first ask, answered, over the still mascot.
  const beat: { keep: number; phase: StagePhase } = player.still ? { keep: 0, phase: "reply" } : STAGE_BEATS[player.index]
  const keep = KEEPS[beat.keep]
  const typed = useTyped(keep.ask, beat.phase === "type" && player.running)
  const acting = beat.phase === "act" && !player.still ? beat.keep : null
  useGaze(figure, acting === null ? null : KEEPS[acting].gaze, player.motionOk)
  const sent = beat.phase === "act" || beat.phase === "reply"

  return (
    <div className="lp-stagebox" ref={player.ref} role="img" aria-label={ALT}>
      <Composer className="lp-ask" text={sent ? keep.ask : typed} sent={sent} placeholder="Ask Palui anything" />
      <p className="lp-reply" data-show={beat.phase === "reply" ? "" : undefined}>
        <AgentMark size={18} />
        {keep.reply}
      </p>
      <div className="lp-orbit">
        <div className="lp-art" style={{ width: ART.width, height: ART.height }}>
          <svg className="lp-wires" viewBox={`0 0 ${ART.width} ${ART.height}`}>
            {TILES.map((tile) => (
              <g key={tile.id} data-on={acting !== null && tile.keep === acting ? "" : undefined}>
                <path d={tile.path} className="lp-wire" />
                <path d={tile.path} pathLength={100} className="lp-beam" />
              </g>
            ))}
          </svg>
          {TILES.map((tile) => (
            <div key={tile.id} className="lp-tile" data-on={acting !== null && tile.keep === acting ? "" : undefined} style={{ left: tile.at[0], top: tile.at[1] }}>
              <tile.icon aria-hidden="true" />
              <span>{tile.label}</span>
            </div>
          ))}
        </div>
        <div className="lp-mascot" data-idle={player.running ? "" : undefined}>
          <svg ref={figure} viewBox={MASCOT_VIEWBOX} aria-hidden="true">
            <Scene alive acting={acting} />
          </svg>
        </div>
      </div>
    </div>
  )
}

"use client"

import { useRef } from "react"
import { KEEPS } from "@/lib/mascot"
import { MASCOT_ALT, MASCOT_VIEWBOX, MascotScene } from "./mascot-scene"
import { useGaze } from "./use-gaze"
import { useMascotLoop } from "./use-mascot-loop"

// The landing's one visual: Palui behind the three things it keeps for you.
// It blinks, its eyes follow a mouse, and every few seconds it turns to one
// of the three, which then does its bit: a note gets written, the alarm
// rings, a hammer comes out of the toolbox. The picture is one image to a
// screen reader.
export function MascotHero() {
  const loop = useMascotLoop()
  const figure = useRef<SVGSVGElement>(null)
  useGaze(figure, loop.focus === null ? null : KEEPS[loop.focus].gaze, loop.motionOk)

  return (
    <div className="lp-mascot" ref={loop.ref} data-idle={loop.running ? "" : undefined}>
      <svg ref={figure} viewBox={MASCOT_VIEWBOX} role="img" aria-label={MASCOT_ALT}>
        <MascotScene alive acting={loop.focus} />
      </svg>
    </div>
  )
}

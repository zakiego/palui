"use client"

import { type RefObject, useEffect, useRef } from "react"

type Aim = readonly [number, number]

const clamp = (n: number) => Math.max(-1, Math.min(1, n))
// How fast the eyes close in on what they look at, as a time constant in ms.
const FOLLOW_MS = 90

// Moves Palui's eyes. Every `[data-gaze="x y"]` part inside `ref` slides by
// that many units as the gaze goes from -1 to 1. The gaze eases toward its
// target every frame from wherever it is, so a new target can take over at
// any point. The target is `held` when one is given (a thing on the slab),
// otherwise the pointer on a device that has one, otherwise straight ahead.
// Each part gets its own transform, so no CSS variable is recalculated across
// the drawing. Nothing runs until `enabled`, which is how reduced motion gets
// a still picture.
export function useGaze(ref: RefObject<SVGSVGElement | null>, held: Aim | null, enabled: boolean) {
  const state = useRef({ held, pointer: [0, 0] as [number, number], kick: () => {} })

  // A new `held` only has to reach the loop and wake it up.
  useEffect(() => {
    state.current.held = held
    state.current.kick()
  }, [held])

  useEffect(() => {
    const el = ref.current
    if (!el || !enabled) return
    const parts = [...el.querySelectorAll<SVGElement>("[data-gaze]")].map((part) => {
      const [fx, fy] = (part.dataset.gaze ?? "0 0").split(" ").map(Number)
      return { part, fx, fy }
    })
    const now = [0, 0]
    let frame = 0
    let last = 0
    // The picture's center on screen, measured once and again after a scroll
    // or a resize, not on every pointer move.
    let center: [number, number] | null = null
    const forget = () => {
      center = null
    }

    function tick(time: number) {
      const target = state.current.held ?? state.current.pointer
      const k = 1 - Math.exp(-Math.min(time - last, 64) / FOLLOW_MS)
      last = time
      now[0] += (target[0] - now[0]) * k
      now[1] += (target[1] - now[1]) * k
      const settled = Math.abs(target[0] - now[0]) + Math.abs(target[1] - now[1]) < 0.004
      if (settled) [now[0], now[1]] = target
      for (const { part, fx, fy } of parts) part.style.transform = `translate(${(now[0] * fx).toFixed(3)}px, ${(now[1] * fy).toFixed(3)}px)`
      frame = settled ? 0 : requestAnimationFrame(tick)
    }
    const kick = () => {
      if (frame) return
      last = performance.now()
      frame = requestAnimationFrame(tick)
    }
    state.current.kick = kick

    function onMove(e: PointerEvent) {
      if (!el || e.pointerType !== "mouse") return
      if (!center) {
        const box = el.getBoundingClientRect()
        center = [box.left + box.width / 2, box.top + box.height / 2]
      }
      state.current.pointer = [clamp((e.clientX - center[0]) / (window.innerWidth / 2)), clamp((e.clientY - center[1]) / (window.innerHeight / 2))]
      kick()
    }
    function onLeave() {
      state.current.pointer = [0, 0]
      kick()
    }

    window.addEventListener("pointermove", onMove, { passive: true })
    window.addEventListener("scroll", forget, { passive: true })
    window.addEventListener("resize", forget)
    document.documentElement.addEventListener("pointerleave", onLeave)
    kick()
    return () => {
      window.removeEventListener("pointermove", onMove)
      window.removeEventListener("scroll", forget)
      window.removeEventListener("resize", forget)
      document.documentElement.removeEventListener("pointerleave", onLeave)
      cancelAnimationFrame(frame)
      state.current.kick = () => {}
      for (const { part } of parts) part.style.transform = ""
    }
  }, [ref, enabled])
}

"use client"

import { useEffect, useRef, useState } from "react"
import { beatAt } from "@/lib/mascot"

type Motion = "unknown" | "ok" | "reduced"

// Steps through lib/mascot.ts's beats: hold one for `beat.wait`, move to the
// next. It opens on a rest (the still picture), and holds its beat while the
// mascot is offscreen or the tab is hidden. With reduced motion it never
// starts, so the still picture is all there is.
export function useMascotLoop() {
  const ref = useRef<HTMLDivElement>(null)
  const [n, setN] = useState(0)
  const [motion, setMotion] = useState<Motion>("unknown")
  const [onScreen, setOnScreen] = useState(true)
  const [pageVisible, setPageVisible] = useState(true)

  useEffect(() => {
    const query = window.matchMedia("(prefers-reduced-motion: reduce)")
    const sync = () => setMotion(query.matches ? "reduced" : "ok")
    sync()
    query.addEventListener("change", sync)
    return () => query.removeEventListener("change", sync)
  }, [])

  useEffect(() => {
    const onVisibility = () => setPageVisible(document.visibilityState === "visible")
    onVisibility()
    document.addEventListener("visibilitychange", onVisibility)
    const el = ref.current
    const observer = el ? new IntersectionObserver(([entry]) => setOnScreen(entry.isIntersecting)) : null
    if (el) observer?.observe(el)
    return () => {
      document.removeEventListener("visibilitychange", onVisibility)
      observer?.disconnect()
    }
  }, [])

  const motionOk = motion === "ok"
  const running = motionOk && onScreen && pageVisible
  const beat = beatAt(n)

  useEffect(() => {
    if (!running) return
    const timer = window.setTimeout(() => setN((i) => i + 1), beat.wait)
    return () => window.clearTimeout(timer)
  }, [running, n, beat.wait])

  return {
    ref,
    /** Index of the thing that is acting, or null. */
    focus: motionOk ? beat.focus : null,
    motionOk,
    running,
  }
}

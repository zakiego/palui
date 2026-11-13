"use client"

import { useEffect, useRef, useState } from "react"
import { TYPE_LEAD, TYPE_MS } from "@/lib/mascot"

type Motion = "unknown" | "ok" | "reduced"

// Every looping demo on the landing runs on this. It steps through a list of
// beats: hold beat `index` for `waits[index]`, then move to the next, forever.
// It opens on beat 0 (never on the finished picture), holds while the demo is
// offscreen or the tab is hidden, and with reduced motion it never starts, so
// the demo draws its still picture instead (`still`).
export function usePlayer<T extends HTMLElement = HTMLDivElement>(waits: readonly number[]) {
  const ref = useRef<T>(null)
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
    const observer = el ? new IntersectionObserver((entries) => setOnScreen(entries[entries.length - 1].isIntersecting)) : null
    if (el) observer?.observe(el)
    return () => {
      document.removeEventListener("visibilitychange", onVisibility)
      observer?.disconnect()
    }
  }, [])

  const running = motion === "ok" && onScreen && pageVisible
  const index = n % waits.length
  const wait = waits[index]

  useEffect(() => {
    if (!running) return
    const timer = window.setTimeout(() => setN((i) => i + 1), wait)
    return () => window.clearTimeout(timer)
  }, [running, n, wait])

  return { ref, index, running, motionOk: motion === "ok", still: motion === "reduced" }
}

/** `text` typed one character at a time while `run` is on, with no caret. */
export function useTyped(text: string, run: boolean) {
  const [count, setCount] = useState(0)

  useEffect(() => {
    setCount(0)
    if (!run) return
    let interval = 0
    const lead = window.setTimeout(() => {
      interval = window.setInterval(() => setCount((c) => Math.min(c + 1, text.length)), TYPE_MS)
    }, TYPE_LEAD)
    return () => {
      window.clearTimeout(lead)
      window.clearInterval(interval)
    }
  }, [text, run])

  return text.slice(0, count)
}

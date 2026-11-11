// Fixtures for routine tests (not imported by production code).
import type { ToolCaller } from "./sources"

// A schedule server like the one at sholat.zakiego.com: one day of times per call,
// as an MCP text result holding JSON. Times move a minute per day, like the real
// ones do through the year.
export function prayerPayload(date: string) {
  const day = Number(date.slice(8, 10))
  const m = (base: number) => String(base + (day % 5)).padStart(2, "0")
  return {
    date,
    location: { slug: "banjarmasin", timezone: "Asia/Makassar" },
    times: { imsak: `04:${m(24)}`, fajr: `04:${m(34)}`, sunrise: `05:${m(50)}`, dhuhr: `12:0${day % 5}`, asr: `15:${m(25)}`, maghrib: `18:${m(11)}`, isha: `19:${m(23)}` },
  }
}

export type FakeCalls = { calls: { connectorId: string; tool: string; args: Record<string, unknown> }[]; fail: boolean }

export function fakePrayerTool(): ToolCaller & FakeCalls {
  const state: FakeCalls = { calls: [], fail: false }
  const fn = (async (connectorId, tool, args) => {
    state.calls.push({ connectorId, tool, args })
    if (state.fail) throw new Error("fetch failed: ECONNRESET")
    return { content: [{ type: "text", text: JSON.stringify(prayerPayload(String(args.date))) }] }
  }) as ToolCaller & FakeCalls
  Object.defineProperty(fn, "calls", { get: () => state.calls })
  Object.defineProperty(fn, "fail", { get: () => state.fail, set: (v: boolean) => (state.fail = v) })
  return fn
}

// The prayer routine exactly as the agent would create it: plain config, no
// prayer-specific code anywhere in the core.
export const PRAYER_ROUTINE = {
  name: "Sholat",
  tz: "Asia/Makassar",
  source: {
    kind: "tool" as const,
    connectorId: "mcp-jadwal-sholat",
    tool: "get_prayer_times",
    args: { city: "banjarmasin", date: "{{date}}" },
    refreshAt: "00:05",
    items: [
      { field: "times.fajr", label: "Subuh" },
      { field: "times.dhuhr", label: "Dzuhur" },
      { field: "times.asr", label: "Ashar" },
      { field: "times.maghrib", label: "Maghrib" },
      { field: "times.isha", label: "Isya" },
    ],
  },
  action: {
    kind: "checkin" as const,
    text: "Udah sholat {{label}}? ({{time}})",
    answers: [
      { id: "done", label: "Sudah", words: ["udah", "alhamdulillah"], closes: true },
      { id: "not_yet", label: "Belum", closes: false },
    ],
    followUp: { everyMin: 20, max: 3 },
    deadline: { kind: "next" as const },
  },
  recap: { kind: "daily" as const, time: "21:30" },
}

// A routine with nothing to do with prayer: fixed times, custom answers.
export const MEDICINE_ROUTINE = {
  name: "Minum obat",
  tz: "Asia/Makassar",
  source: { kind: "times" as const, times: [{ time: "08:00", label: "pagi" }, { time: "20:00", label: "malam" }] },
  action: {
    kind: "checkin" as const,
    text: "💊 Obat {{label}} udah diminum? Jawab: {{answers}}",
    answers: [
      { id: "taken", label: "Udah minum", words: ["udah", "sudah", "done"], closes: true, reply: "Mantap, obat {{label}} tercatat 👍" },
      { id: "skip", label: "Skip", words: ["lewat", "skip dulu"], closes: true },
      { id: "later", label: "Nanti", closes: false },
    ],
    followUp: { everyMin: 15, max: 2, text: "Pengingat ke-{{n}}: obat {{label}} jangan lupa ya." },
    deadline: { kind: "after" as const, minutes: 120 },
    missedText: "Obat {{label}} ({{time}}) kelewat, dicatat ya.",
  },
}

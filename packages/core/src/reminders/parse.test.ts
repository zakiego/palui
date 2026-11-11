import { MockLanguageModelV4 } from "ai/test"
import { describe, expect, it } from "vitest"
import { buildSystemPrompt, finishParse, parsedLocalStart, parseReminder } from "./parse"
import { NOW, WITA } from "./test-helpers"
import { localToUtc } from "./time"

function mockModel(reply: string | (() => never)) {
  const calls: { prompt: unknown }[] = []
  const model = new MockLanguageModelV4({
    doGenerate: async (opts) => {
      calls.push({ prompt: opts.prompt })
      if (typeof reply === "function") reply()
      return {
        content: [{ type: "text", text: reply as string }],
        finishReason: { unified: "stop", raw: undefined },
        usage: {
          inputTokens: { total: 10, noCache: 10, cacheRead: undefined, cacheWrite: undefined },
          outputTokens: { total: 20, text: 20, reasoning: undefined },
        },
        warnings: [],
      }
    },
  })
  return { model, calls }
}

describe("parseReminder", () => {
  it("returns the instant, ISO with offset and the echo", async () => {
    const { model, calls } = mockModel(JSON.stringify({ text: "bayar listrik", localDateTime: "2026-11-11T09:00", rrule: null }))
    const r = await parseReminder("ingetin bayar listrik besok jam 9", WITA, NOW, model)
    expect(r).toEqual({
      ok: true,
      text: "bayar listrik",
      fireAt: localToUtc("2026-11-11T09:00", WITA),
      fireAtIso: "2026-11-11T09:00:00.000+08:00",
      rrule: null,
      tz: WITA,
      echo: "Besok, Rabu 11 Nov, 09.00 WITA",
    })
    expect(parsedLocalStart(r)).toBe("2026-11-11T09:00:00")
    // The system prompt carries now, the weekday and the zone.
    expect(JSON.stringify(calls[0]?.prompt)).toContain("2026-11-10T10:00 (Tuesday / Selasa), zone Asia/Makassar (WITA, UTC+08:00)")
  })

  it("normalizes a recurring rule and describes it", async () => {
    const { model } = mockModel(JSON.stringify({ text: "kirim laporan", localDateTime: "2026-11-16T08:00", rrule: "BYDAY=MO;FREQ=WEEKLY" }))
    const r = await parseReminder("tiap senin jam 8 kirim laporan", WITA, NOW, model)
    expect(r.ok).toBe(true)
    expect(r.rrule).toBe("FREQ=WEEKLY;BYDAY=MO")
    expect(r.echo).toBe("Tiap Senin, 08.00 WITA, mulai Senin 16 Nov")
  })

  it("never throws on model errors", async () => {
    const { model } = mockModel(() => {
      throw new Error("503 upstream")
    })
    const r = await parseReminder("besok jam 9", WITA, NOW, model, { maxRetries: 0 })
    expect(r.ok).toBe(false)
    expect(r.error).toMatch(/503 upstream/)
    expect(r.text).toBe("besok jam 9")
  })

  it("reports unparseable model output", async () => {
    const { model } = mockModel("not json at all")
    const r = await parseReminder("besok", WITA, NOW, model, { maxRetries: 0 })
    expect(r.ok).toBe(false)
  })

  it("refuses an empty phrase and a bad zone without calling the model", async () => {
    const { model, calls } = mockModel("{}")
    expect((await parseReminder("  ", WITA, NOW, model)).ok).toBe(false)
    expect((await parseReminder("besok", "Bad/Zone", NOW, model)).ok).toBe(false)
    expect(calls).toHaveLength(0)
  })
})

describe("finishParse", () => {
  it("rolls a one-off earlier today over to tomorrow", () => {
    const r = finishParse({ text: "cek email", localDateTime: "2026-11-10T09:00", rrule: null }, "", WITA, NOW)
    expect(r.echo).toBe("Besok, Rabu 11 Nov, 09.00 WITA")
  })

  it("rejects a one-off on an earlier day", () => {
    const r = finishParse({ text: "x", localDateTime: "2026-11-09T09:00", rrule: null }, "", WITA, NOW)
    expect(r.ok).toBe(false)
    expect(r.error).toMatch(/already passed/)
  })

  it("starts a simple series today when its time is still ahead", () => {
    const r = finishParse({ text: "les", localDateTime: "2026-11-12T18:00", rrule: "FREQ=WEEKLY;BYDAY=TU,TH" }, "", WITA, NOW)
    expect(r.fireAt).toBe(localToUtc("2026-11-10T18:00", WITA))
  })

  it("keeps a later start the user asked for", () => {
    // "starting next Monday, every weekday 08:00" said on Tuesday 10:00.
    const r = finishParse({ text: "standup", localDateTime: "2026-11-16T08:00", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" }, "", WITA, NOW)
    expect(r.fireAt).toBe(localToUtc("2026-11-16T08:00", WITA))
    expect(r.echo).toBe("Tiap hari kerja, 08.00 WITA, mulai Senin 16 Nov")
    // Same with a time still ahead today: Monday isn't the next matching day.
    const later = finishParse({ text: "x", localDateTime: "2026-11-16T19:00", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" }, "", WITA, NOW)
    expect(later.fireAt).toBe(localToUtc("2026-11-16T19:00", WITA))
  })

  it("shows the first run in the series echo", () => {
    const today = finishParse({ text: "les", localDateTime: "2026-11-12T18:00", rrule: "FREQ=WEEKLY;BYDAY=TU,TH" }, "", WITA, NOW)
    expect(today.echo).toBe("Tiap Selasa dan Kamis, 18.00 WITA, mulai hari ini")
    const tomorrow = finishParse({ text: "olahraga", localDateTime: "2026-11-11T07:00", rrule: "FREQ=DAILY" }, "", WITA, NOW)
    expect(tomorrow.echo).toBe("Tiap hari, 07.00 WITA, mulai besok, Rabu 11 Nov")
    const hourly = finishParse({ text: "minum", localDateTime: "2026-11-10T13:00", rrule: "FREQ=HOURLY;INTERVAL=3" }, "", WITA, NOW)
    expect(hourly.echo).toBe("Tiap 3 jam, mulai hari ini 13.00 WITA")
  })

  it("rejects a first fire more than 2 years away", () => {
    const r = finishParse({ text: "x", localDateTime: "2031-01-01T09:00", rrule: null }, "", WITA, NOW)
    expect(r.ok).toBe(false)
    expect(r.error).toMatch(/2 years/)
    expect(finishParse({ text: "x", localDateTime: "2028-11-01T09:00", rrule: null }, "", WITA, NOW).ok).toBe(true)
  })

  it("doesn't move a series whose anchor picks the weekday", () => {
    const r = finishParse({ text: "x", localDateTime: "2026-11-13T18:00", rrule: "FREQ=WEEKLY" }, "", WITA, NOW)
    expect(r.fireAt).toBe(localToUtc("2026-11-13T18:00", WITA))
  })

  it("strips a stray offset and falls back to the input text", () => {
    const r = finishParse({ text: "", localDateTime: "2026-11-11T09:00:00+08:00", rrule: "" }, "the input", WITA, NOW)
    expect(r.ok).toBe(true)
    expect(r.text).toBe("the input")
    expect(r.rrule).toBeNull()
  })

  it("reports a bad rule", () => {
    expect(finishParse({ text: "x", localDateTime: "2026-11-11T09:00", rrule: "FREQ=SOMETIMES" }, "", WITA, NOW).ok).toBe(false)
  })
})

describe("buildSystemPrompt", () => {
  it("lists the coming weekdays", () => {
    const p = buildSystemPrompt(WITA, NOW)
    expect(p).toContain("Thursday/Kamis = 2026-11-12")
    expect(p).toContain("Next week starts on Monday 2026-11-16")
    expect(p).toContain("This month ends on 2026-11-30")
  })
})

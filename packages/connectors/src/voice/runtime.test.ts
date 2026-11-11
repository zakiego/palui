import { describe, expect, it } from "vitest"
import type { InboundMedia } from "@palui/core/saklar/define"
import manifest from "./manifest"
import { checkElevenLabsKey, transcribeWithElevenLabs } from "./runtime"

type Call = { url: string; init: RequestInit }

function mockFetch(handler: (url: string, init: RequestInit) => Response) {
  const calls: Call[] = []
  const f = (async (input: string | URL | Request, init: RequestInit = {}) => {
    const url = String(input)
    calls.push({ url, init })
    return handler(url, init)
  }) as typeof fetch
  return { f, calls }
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } })
const audio: InboundMedia = { kind: "audio", mimeType: "audio/ogg; codecs=opus", data: Buffer.from("fake-ogg-bytes").toString("base64") }
const config = manifest.config.parse({})

describe("voice manifest", () => {
  it("defaults to scribe_v1 in Indonesian", () => {
    expect(config).toEqual({ model: "scribe_v1", language: "id" })
  })
})

describe("transcribeWithElevenLabs", () => {
  it("posts multipart model_id, language and file with the key header", async () => {
    const { f, calls } = mockFetch(() => json(200, { language_code: "ind", text: " halo palui, ingetin aku besok jam 7 " }))
    const text = await transcribeWithElevenLabs(audio, "xi_key_123456", config, f)
    expect(text).toBe("halo palui, ingetin aku besok jam 7")
    expect(calls[0]?.url).toBe("https://api.elevenlabs.io/v1/speech-to-text")
    expect(calls[0]?.init.method).toBe("POST")
    expect((calls[0]?.init.headers as Record<string, string>)["xi-api-key"]).toBe("xi_key_123456")
    const form = calls[0]?.init.body as FormData
    expect(form.get("model_id")).toBe("scribe_v1")
    expect(form.get("language_code")).toBe("id")
    const file = form.get("file") as File
    expect(file.name).toBe("voice.ogg")
    expect(Buffer.from(await file.arrayBuffer()).toString()).toBe("fake-ogg-bytes")
  })

  it("omits language_code for auto", async () => {
    const { f, calls } = mockFetch(() => json(200, { text: "hello" }))
    await transcribeWithElevenLabs(audio, "k", manifest.config.parse({ language: "auto" }), f)
    expect((calls[0]?.init.body as FormData).has("language_code")).toBe(false)
  })

  it("explains a 401 and other errors", async () => {
    const bad = mockFetch(() => json(401, { detail: { status: "invalid_api_key", message: "Invalid API key" } }))
    await expect(transcribeWithElevenLabs(audio, "k", config, bad.f)).rejects.toThrow(/rejected the API key \(401\)/)
    const busy = mockFetch(() => json(429, { detail: { status: "too_many_concurrent_requests", message: "Slow down" } }))
    await expect(transcribeWithElevenLabs(audio, "k", config, busy.f)).rejects.toMatchObject({ status: 429, message: expect.stringMatching(/Slow down/) })
  })

  it("refuses empty audio without calling out", async () => {
    const { f, calls } = mockFetch(() => json(200, {}))
    await expect(transcribeWithElevenLabs({ ...audio, data: "" }, "k", config, f)).rejects.toThrow(/empty/)
    expect(calls).toHaveLength(0)
  })
})

describe("checkElevenLabsKey", () => {
  it("passes with a working key", async () => {
    const { f, calls } = mockFetch(() => json(200, { subscription: { tier: "creator" } }))
    expect(await checkElevenLabsKey("k", f)).toEqual({ ok: true, detail: "ElevenLabs key works (creator plan)" })
    expect(calls[0]?.url).toBe("https://api.elevenlabs.io/v1/user")
  })

  it("gives a clear error on 401", async () => {
    const { f } = mockFetch(() => json(401, { detail: { status: "invalid_api_key", message: "Invalid API key" } }))
    const res = await checkElevenLabsKey("k", f)
    expect(res.ok).toBe(false)
    expect(res.detail).toMatch(/rejected the API key \(401\)/)
  })

  it("accepts a key scoped without user_read", async () => {
    const { f } = mockFetch(() => json(401, { detail: { status: "missing_permissions", message: "missing user_read" } }))
    expect((await checkElevenLabsKey("k", f)).ok).toBe(true)
  })
})

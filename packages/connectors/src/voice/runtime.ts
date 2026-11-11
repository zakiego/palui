// Voice-in (PLAN 5.5): ElevenLabs Speech to Text (Scribe). The Gemini fallback
// through CLIProxyAPI isn't built: CLIProxyAPI serves no Gemini models today.
import type { InboundMedia } from "@palui/core/connectors/define"
import { defineRuntime, type ConfigOf } from "@palui/core/connectors/define"
import manifest from "./manifest"

type Config = ConfigOf<typeof manifest>

export const ELEVENLABS_API = "https://api.elevenlabs.io"
export const MAX_AUDIO_BYTES = 25 * 1024 * 1024

export class VoiceError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = "VoiceError"
  }
}

const EXT: Record<string, string> = {
  "audio/ogg": "ogg",
  "audio/opus": "opus",
  "audio/mpeg": "mp3",
  "audio/mp4": "m4a",
  "audio/x-m4a": "m4a",
  "audio/aac": "aac",
  "audio/wav": "wav",
  "audio/x-wav": "wav",
  "audio/webm": "webm",
}

function fileName(audio: InboundMedia): string {
  if (audio.filename) return audio.filename
  const base = audio.mimeType.split(";")[0]?.trim().toLowerCase() ?? ""
  return `voice.${EXT[base] ?? "ogg"}`
}

async function errorDetail(res: Response): Promise<{ status?: string; message: string }> {
  const text = await res.text().catch(() => "")
  try {
    const body = JSON.parse(text) as { detail?: { status?: string; message?: string } | string }
    if (typeof body.detail === "string") return { message: body.detail }
    return { status: body.detail?.status, message: body.detail?.message ?? text.slice(0, 200) }
  } catch {
    return { message: text.slice(0, 200) || res.statusText }
  }
}

export async function transcribeWithElevenLabs(audio: InboundMedia, apiKey: string, config: Config, fetchImpl: typeof fetch = fetch): Promise<string> {
  const bytes = Buffer.from(audio.data, "base64")
  if (!bytes.byteLength) throw new VoiceError(400, "The voice note is empty")
  if (bytes.byteLength > MAX_AUDIO_BYTES) throw new VoiceError(413, "The voice note is larger than 25 MB")
  const form = new FormData()
  form.append("model_id", config.model || "scribe_v1")
  const lang = config.language?.trim()
  if (lang && lang !== "auto") form.append("language_code", lang)
  form.append("tag_audio_events", "false")
  form.append("file", new Blob([bytes], { type: audio.mimeType || "application/octet-stream" }), fileName(audio))
  const res = await fetchImpl(`${ELEVENLABS_API}/v1/speech-to-text`, {
    method: "POST",
    headers: { "xi-api-key": apiKey },
    body: form,
    signal: AbortSignal.timeout(120_000),
  })
  if (!res.ok) {
    const d = await errorDetail(res)
    if (res.status === 401) throw new VoiceError(401, `ElevenLabs rejected the API key (401): ${d.message}`)
    throw new VoiceError(res.status, `ElevenLabs answered ${res.status}: ${d.message}`)
  }
  const body = (await res.json()) as { text?: string }
  return (body.text ?? "").trim()
}

// A cheap authenticated call. A key restricted to Speech to Text can't read
// /v1/user and gets 401 missing_permissions: that still proves the key is real.
export async function checkElevenLabsKey(apiKey: string, fetchImpl: typeof fetch = fetch): Promise<{ ok: boolean; detail: string }> {
  let res: Response
  try {
    res = await fetchImpl(`${ELEVENLABS_API}/v1/user`, { headers: { "xi-api-key": apiKey }, signal: AbortSignal.timeout(10_000) })
  } catch (err) {
    return { ok: false, detail: `Can't reach ElevenLabs: ${(err as Error)?.message ?? String(err)}` }
  }
  if (res.ok) {
    const user = (await res.json().catch(() => ({}))) as { subscription?: { tier?: string; character_count?: number; character_limit?: number } }
    const tier = user.subscription?.tier
    return { ok: true, detail: tier ? `ElevenLabs key works (${tier} plan)` : "ElevenLabs key works" }
  }
  const d = await errorDetail(res)
  if (res.status === 401 && d.status === "missing_permissions") {
    return { ok: true, detail: "ElevenLabs key works (it can't read account info, which is fine)" }
  }
  if (res.status === 401) return { ok: false, detail: `ElevenLabs rejected the API key (401). Check it in elevenlabs.io, Developers, API keys.` }
  return { ok: false, detail: `ElevenLabs answered ${res.status}: ${d.message}` }
}

export default defineRuntime(manifest, {
  async test(ctx) {
    if (!ctx.secrets.elevenLabsKey) return { ok: false, detail: "Add the ElevenLabs API key" }
    return checkElevenLabsKey(ctx.secrets.elevenLabsKey)
  },
  async start(ctx) {
    const key = ctx.secrets.elevenLabsKey
    if (!key) throw new Error("Add the ElevenLabs API key")
    let transcribed = 0
    let failed = 0
    return {
      async stop() {},
      async transcribe(audio) {
        try {
          const text = await transcribeWithElevenLabs(audio, key, ctx.config)
          transcribed++
          return text
        } catch (err) {
          failed++
          throw err
        }
      },
      live() {
        return { engine: "elevenlabs", model: ctx.config.model, language: ctx.config.language || "auto", transcribed, failed, fallback: null }
      },
    }
  },
})

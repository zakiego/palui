import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { Update } from "grammy/types"
import { setConfig } from "@palui/core/config"
import { createClaim, getOwner } from "@palui/core/owner"
import type { RunningConnector } from "@palui/core/saklar/define"
import { sha256 } from "@palui/db/crypto"
import { flush, testContext } from "../channel-utils/testing"
import manifest from "./manifest"
import { COPY, ERRORS, createTelegramRuntime, renderTelegramChunks } from "./runtime"
import { FakeApiError, fakeTelegram, type FakeTelegram } from "./test-transport"

const TOKEN = "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"
const OWNER = { id: 42, is_bot: false, first_name: "Zaki", last_name: "M" }
const STRANGER = { id: 77, is_bot: false, first_name: "Orang" }

let updateId = 1
function messageUpdate(message: Record<string, unknown>, from: { id: number; is_bot: boolean; first_name: string; last_name?: string } = OWNER): Update {
  return {
    update_id: updateId++,
    message: { message_id: updateId + 500, date: 1_700_000_000, chat: { id: from.id, type: "private", first_name: from.first_name }, from, ...message },
  } as unknown as Update
}

function setup(opts: { fetchFile?: (url: string) => Promise<Buffer> } = {}) {
  const tg = fakeTelegram()
  const fetched: string[] = []
  const runtime = createTelegramRuntime({
    createBot: tg.createBot,
    fetchFile: async (url) => {
      fetched.push(url)
      return opts.fetchFile ? opts.fetchFile(url) : Buffer.from(`bytes:${url.split("/").pop()}`)
    },
  })
  const ctx = testContext({ type: "telegram", id: "telegram", secrets: { botToken: TOKEN }, config: {} as { botUsername?: string } })
  return { tg, runtime, ctx, fetched }
}

async function started(s: ReturnType<typeof setup>): Promise<{ running: RunningConnector; tg: FakeTelegram; feed: (u: Update) => Promise<void> }> {
  const running = await s.runtime.start(s.ctx)
  await flush()
  const bot = s.tg.bots.at(-1)
  if (!bot) throw new Error("no bot")
  return { running, tg: s.tg, feed: (u) => bot.handleUpdate(u) }
}

beforeEach(() => setConfig({ mode: "prod", prodTokenHashes: [] }))
afterEach(() => {
  setConfig(undefined)
  vi.useRealTimers()
})

describe("telegram manifest", () => {
  it("validates bot tokens", () => {
    expect(manifest.secrets.botToken.safeParse(TOKEN).success).toBe(true)
    expect(manifest.secrets.botToken.safeParse("nope").success).toBe(false)
    expect(manifest.setup.map((s) => ("field" in s ? s.field : s.action))).toEqual(["botToken", "claimOwner"])
  })
})

describe("telegram test()", () => {
  it("reports the bot username", async () => {
    const s = setup()
    expect(await s.runtime.test(s.ctx)).toEqual({ ok: true, detail: "@palui_test_bot reachable", data: { username: "palui_test_bot" } })
  })

  it("explains a rejected token", async () => {
    const s = setup()
    s.tg.on("getMe", () => new FakeApiError(401, "Unauthorized"))
    expect(await s.runtime.test(s.ctx)).toEqual({ ok: false, detail: ERRORS.unauthorized })
  })
})

describe("telegram start()", () => {
  it("polls, reports on, learns the username and stops cleanly", async () => {
    const s = setup()
    const { running } = await started(s)
    expect(s.ctx.statuses.map((x) => x.status)).toEqual(["connecting", "on"])
    expect(s.ctx.patches).toEqual([{ botUsername: "palui_test_bot" }])
    expect(running.channel?.reachable()).toBe(true)
    expect(running.live?.()).toEqual({ username: "palui_test_bot", ownerClaimed: false, polling: true })
    await running.stop()
    expect(running.channel?.reachable()).toBe(false)
    expect(s.ctx.statuses.at(-1)?.status).toBe("on")
  })

  it("maps a 409 conflict to a clear error", async () => {
    const s = setup()
    s.tg.on("getUpdates", () => new FakeApiError(409, "Conflict: terminated by other getUpdates request"))
    const { running } = await started(s)
    await flush()
    expect(s.ctx.statuses.at(-1)).toEqual({ status: "error", detail: ERRORS.conflict })
    // The message names the cause and the fix.
    expect(ERRORS.conflict).toMatch(/409 Conflict/)
    expect(ERRORS.conflict).toMatch(/@BotFather/)
    expect(running.channel?.reachable()).toBe(false)
    await running.stop()
  })

  it("refuses a prod token in dev mode", async () => {
    setConfig({ mode: "dev", prodTokenHashes: [sha256(TOKEN)] })
    const s = setup()
    await expect(s.runtime.start(s.ctx)).rejects.toThrow(ERRORS.devGuard)
    expect(s.ctx.statuses).toEqual([{ status: "error", detail: ERRORS.devGuard }])
    expect(s.tg.bots).toHaveLength(0)
    expect((await s.runtime.test(s.ctx)).ok).toBe(false)
  })

  it("allows a dev token in dev mode", async () => {
    setConfig({ mode: "dev", prodTokenHashes: [sha256("other-token")] })
    const s = setup()
    const { running } = await started(s)
    expect(s.ctx.statuses.at(-1)?.status).toBe("on")
    await running.stop()
  })
})

describe("telegram owner claim", () => {
  it("claims with a fresh nonce, then refuses reuse", async () => {
    const s = setup()
    const { running, feed, tg } = await started(s)
    const link = await running.action?.("claimLink", {})
    expect(link?.url).toMatch(/^https:\/\/t\.me\/palui_test_bot\?start=[\w-]+$/)
    expect(String(link?.qrSvg)).toContain("<svg")
    expect(typeof link?.expiresAt).toBe("number")
    const nonce = String(link?.url).split("start=")[1]

    await feed(messageUpdate({ text: `/start ${nonce}`, entities: [{ type: "bot_command", offset: 0, length: 6 }] }))
    expect(tg.callsTo("sendMessage").at(-1)).toMatchObject({ chat_id: 42, text: COPY.claimed })
    expect(getOwner(s.ctx.db, "telegram")?.externalId).toBe("42")
    expect(running.live?.().ownerClaimed).toBe(true)

    await feed(messageUpdate({ text: `/start ${nonce}` }, STRANGER))
    expect(tg.callsTo("sendMessage").at(-1)).toMatchObject({ chat_id: 77, text: COPY.claimFailed })
    expect(getOwner(s.ctx.db, "telegram")?.externalId).toBe("42")
    expect(s.ctx.inbound).toHaveLength(0)
    await running.stop()
  })

  it("refuses an expired nonce", async () => {
    const s = setup()
    const { running, feed, tg } = await started(s)
    const { nonce } = createClaim(s.ctx.db, "telegram", Date.now() - 11 * 60_000)
    await feed(messageUpdate({ text: `/start ${nonce}` }))
    expect(tg.callsTo("sendMessage").at(-1)).toMatchObject({ text: COPY.claimFailed })
    expect(getOwner(s.ctx.db, "telegram")).toBeUndefined()
    expect(s.ctx.inbound).toHaveLength(0)
    await running.stop()
  })
})

describe("telegram inbound", () => {
  async function claimed() {
    const s = setup()
    const r = await started(s)
    const { nonce } = createClaim(s.ctx.db, "telegram")
    await r.feed(messageUpdate({ text: `/start ${nonce}` }))
    return { ...s, ...r }
  }

  it("forwards text with the reply target", async () => {
    const s = await claimed()
    await s.feed(messageUpdate({ message_id: 900, text: "ingetin aku besok", reply_to_message: { message_id: 812, date: 0, chat: { id: 42, type: "private" } } }))
    expect(s.ctx.inbound[0]).toEqual({
      channel: "telegram",
      connectorId: "telegram",
      chatExternalId: "42",
      chatKind: "dm",
      chatTitle: "Zaki M",
      senderExternalId: "42",
      senderName: "Zaki M",
      channelMsgId: "900",
      replyToChannelMsgId: "812",
      text: "ingetin aku besok",
      at: 1_700_000_000_000,
    })
    await s.running.stop()
  })

  it("downloads the largest photo as base64 jpeg with its caption", async () => {
    const s = await claimed()
    await s.feed(
      messageUpdate({
        caption: "ini apa?",
        photo: [
          { file_id: "small", file_unique_id: "s", width: 90, height: 90 },
          { file_id: "big", file_unique_id: "b", width: 1280, height: 960 },
          { file_id: "mid", file_unique_id: "m", width: 320, height: 240 },
        ],
      }),
    )
    expect(s.fetched).toEqual([`https://api.telegram.org/file/bot${TOKEN}/files/big`])
    expect(s.ctx.inbound[0]?.text).toBe("ini apa?")
    expect(s.ctx.inbound[0]?.media).toEqual([{ kind: "image", mimeType: "image/jpeg", data: Buffer.from("bytes:big").toString("base64") }])
    await s.running.stop()
  })

  it("downloads voice notes with their duration", async () => {
    const s = await claimed()
    await s.feed(messageUpdate({ voice: { file_id: "v1", file_unique_id: "v", duration: 7, mime_type: "audio/ogg" } }))
    expect(s.ctx.inbound[0]).toMatchObject({ text: "", media: [{ kind: "audio", mimeType: "audio/ogg", durationSec: 7, data: Buffer.from("bytes:v1").toString("base64") }] })
    await s.running.stop()
  })

  it("reads small documents and skips big ones", async () => {
    const s = await claimed()
    await s.feed(messageUpdate({ document: { file_id: "d1", file_unique_id: "d", file_name: "catatan.txt", mime_type: "text/plain", file_size: 10 } }))
    expect(s.ctx.inbound[0]?.media).toEqual([{ kind: "document", mimeType: "text/plain", filename: "catatan.txt", data: Buffer.from("bytes:d1").toString("base64") }])
    await s.feed(messageUpdate({ caption: "baca ini", document: { file_id: "d2", file_unique_id: "e", file_name: "big.pdf", file_size: 5 * 1024 * 1024 } }))
    expect(s.ctx.inbound[1]?.media).toBeUndefined()
    expect(s.ctx.inbound[1]?.text).toContain("baca ini")
    expect(s.ctx.inbound[1]?.text).toContain("over the 1 MB limit")
    expect(s.fetched).toHaveLength(1)
    await s.running.stop()
  })

  it("does not download media from strangers", async () => {
    const s = await claimed()
    await s.feed(messageUpdate({ caption: "spam", photo: [{ file_id: "x", file_unique_id: "x", width: 1, height: 1 }] }, STRANGER))
    expect(s.fetched).toHaveLength(0)
    expect(s.ctx.inbound[0]).toMatchObject({ senderExternalId: "77", text: "spam" })
    expect(s.ctx.inbound[0]?.media).toBeUndefined()
    await s.running.stop()
  })

  it("turns button presses into callbacks and answers them", async () => {
    const s = await claimed()
    await s.feed({
      update_id: updateId++,
      callback_query: {
        id: "cb-1",
        from: OWNER,
        chat_instance: "ci",
        data: "rem:done:r1",
        message: { message_id: 300, date: 0, chat: { id: 42, type: "private", first_name: "Zaki" } },
      },
    } as unknown as Update)
    expect(s.ctx.inbound[0]).toMatchObject({
      chatExternalId: "42",
      senderExternalId: "42",
      text: "",
      callback: { data: "rem:done:r1", callbackId: "cb-1" },
      replyToChannelMsgId: "300",
    })
    expect(s.tg.callsTo("answerCallbackQuery")).toEqual([{ callback_query_id: "cb-1" }])
    await s.running.stop()
  })
})

describe("telegram send()", () => {
  it("sends markdown as HTML with the keyboard and reply target", async () => {
    const s = setup()
    const { running, tg } = await started(s)
    const res = await running.channel?.send("42", {
      text: "**Pengingat**: minum <obat>",
      format: "markdown",
      replyToChannelMsgId: "55",
      actions: [
        { id: "rem:done:r1", label: "Done" },
        { id: "rem:snooze:r1:1h", label: "1 jam" },
        { id: "x".repeat(65), label: "too long" },
      ],
    })
    const sent = tg.callsTo("sendMessage")
    expect(sent).toHaveLength(1)
    expect(sent[0]).toEqual({
      chat_id: "42",
      text: "<b>Pengingat</b>: minum &lt;obat&gt;",
      parse_mode: "HTML",
      reply_parameters: { message_id: 55, allow_sending_without_reply: true },
      reply_markup: {
        inline_keyboard: [
          [
            { text: "Done", callback_data: "rem:done:r1" },
            { text: "1 jam", callback_data: "rem:snooze:r1:1h" },
          ],
        ],
      },
    })
    expect(res).toEqual({ channelMsgId: "100" })
    await running.stop()
  })

  it("splits long replies, keyboard on the last chunk only", async () => {
    const s = setup()
    const { running, tg } = await started(s)
    const para = "Kalimat yang lumayan panjang buat ngetes pemotongan. ".repeat(40)
    const text = [para, para, para].join("\n\n")
    const res = await running.channel?.send("42", { text, actions: [{ id: "approve:a1", label: "Ya" }], replyToChannelMsgId: "9" })
    const sent = tg.callsTo("sendMessage")
    expect(sent.length).toBeGreaterThan(1)
    for (const [i, call] of sent.entries()) {
      expect(String(call.text).length).toBeLessThanOrEqual(4096)
      expect(call.reply_markup !== undefined).toBe(i === sent.length - 1)
      expect(call.reply_parameters !== undefined).toBe(i === 0)
    }
    expect(res?.channelMsgId).toBe(String(100 + sent.length - 1))
    await running.stop()
  })

  it("falls back to plain text when Telegram rejects the HTML", async () => {
    const s = setup()
    const { running, tg } = await started(s)
    tg.on("sendMessage", (p) =>
      p.parse_mode ? new FakeApiError(400, "Bad Request: can't parse entities: unexpected end tag") : { message_id: 7, date: 0, chat: { id: 42, type: "private" }, text: p.text },
    )
    const res = await running.channel?.send("42", { text: "**a *b** c*" })
    const sent = tg.callsTo("sendMessage")
    expect(sent).toHaveLength(2)
    expect(sent[1]).toEqual({ chat_id: "42", text: "**a *b** c*" })
    expect(res).toEqual({ channelMsgId: "7" })
    await running.stop()
  })

  it("rethrows other send errors so the outbox retries", async () => {
    const s = setup()
    const { running, tg } = await started(s)
    tg.on("sendMessage", () => new FakeApiError(403, "Forbidden: bot was blocked by the user"))
    await expect(running.channel?.send("42", { text: "hai" })).rejects.toThrow(/403|Forbidden/)
    await running.stop()
  })

  it("renders chunks whose HTML fits", () => {
    const chunks = renderTelegramChunks("&".repeat(5000), 4096)
    for (const c of chunks) expect(c.html.length).toBeLessThanOrEqual(4096)
    expect(chunks.map((c) => c.plain).join("")).toBe("&".repeat(5000))
  })
})

describe("telegram typing", () => {
  it("repeats every 4 s until switched off", async () => {
    const s = setup()
    const { running, tg } = await started(s)
    vi.useFakeTimers()
    await running.channel?.typing?.("42", true)
    expect(tg.callsTo("sendChatAction")).toEqual([{ chat_id: "42", action: "typing" }])
    await vi.advanceTimersByTimeAsync(8_100)
    expect(tg.callsTo("sendChatAction")).toHaveLength(3)
    await running.channel?.typing?.("42", false)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(tg.callsTo("sendChatAction")).toHaveLength(3)
    vi.useRealTimers()
    await running.stop()
  })

  it("stops on send and on stop()", async () => {
    const s = setup()
    const { running, tg } = await started(s)
    vi.useFakeTimers()
    await running.channel?.typing?.("42", true)
    await running.channel?.send("42", { text: "selesai" })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(tg.callsTo("sendChatAction")).toHaveLength(1)
    await running.channel?.typing?.("43", true)
    vi.useRealTimers()
    await running.stop()
    vi.useFakeTimers()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(tg.callsTo("sendChatAction")).toHaveLength(2)
  })
})

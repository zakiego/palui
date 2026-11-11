import { describe, expect, it } from "vitest"
import { ensureChat } from "@palui/core/chats"
import { createHook, ingestHook, getHookBySlug } from "@palui/core/hooks/index"
import type { TurnInfo } from "@palui/core/connectors/define"
import { fakeCtx } from "../mcp/test-helpers"
import manifest from "./manifest"
import runtime from "./runtime"

const KEY = "test-master-key-0123456789-abcdefghijklmnop"
const turn: TurnInfo = { runId: "r", chatId: "c", channel: "web", trigger: "hook", tz: "Asia/Makassar", now: 0 }

describe("hooks connector", () => {
  it("is a builtin skill with the agreed summary", () => {
    expect(manifest).toMatchObject({ id: "hooks", kind: "skill", builtin: true, icon: "webhook", summary: "Forward events from other apps to you" })
    expect(manifest.pauseNote).toMatch(/503/)
  })

  it("has no tools and reports hook counts", async () => {
    const ctx = fakeCtx({}, {}, { id: "hooks" })
    expect(await runtime.test(ctx)).toMatchObject({ ok: true, detail: expect.stringMatching(/Create a hook/) })
    const running = await runtime.start(ctx)
    expect(await running.tools!(turn)).toEqual({})
    expect(running.live!()).toEqual({ hooks: 0, enabled: 0, lastAt: null })

    const chat = ensureChat(ctx.db, { channel: "web", externalId: "web" }, 1)
    const { hook } = createHook(ctx.db, { name: "Uptime", mode: "summarize", chatId: chat.id }, KEY, 2)
    ingestHook(ctx.db, getHookBySlug(ctx.db, hook.slug)!, '{"status":"down"}', 99)
    expect(running.live!()).toEqual({ hooks: 1, enabled: 1, lastAt: 99 })
    expect(await runtime.test(ctx)).toMatchObject({ detail: "1 hook set up" })
  })
})

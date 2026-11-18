import { describe, expect, it } from "vitest"
import { transcriptToMarkdown } from "./transcript-text"

describe("transcriptToMarkdown", () => {
  it("writes the header, each message, and tool calls under their reply", () => {
    const md = transcriptToMarkdown({
      chat: { id: "chat_1", channel: "web", title: "Palui (web)" },
      messages: [
        { role: "user", text: "hi", createdAt: 0, tools: [] },
        { role: "assistant", text: "**hello**", createdAt: 1000, tools: [{ tool: "c1__search", class: "read", status: "ok" }] },
        { role: "user", source: "hook", text: "deploy done", createdAt: 2000, tools: [] },
      ],
    })
    expect(md).toBe(
      [
        "# Palui (web)",
        "",
        "Channel: web",
        "Chat ID: chat_1",
        "",
        "## User (1970-01-01T00:00:00.000Z)",
        "",
        "hi",
        "",
        "## Palui (1970-01-01T00:00:01.000Z)",
        "",
        "- tool `c1__search` (read, ok)",
        "",
        "**hello**",
        "",
        "## Webhook event (1970-01-01T00:00:02.000Z)",
        "",
        "deploy done",
        "",
      ].join("\n")
    )
  })

  it("handles an empty chat", () => {
    expect(transcriptToMarkdown({ chat: { id: "c", channel: "telegram", title: "T" }, messages: [] })).toBe("# T\n\nChannel: telegram\nChat ID: c\n")
  })

  it("lists files a reply sent under it", () => {
    const md = transcriptToMarkdown({
      chat: { id: "chat_1", channel: "web", title: "Palui (web)" },
      messages: [{ role: "assistant", text: "Ini", createdAt: 0, tools: [], media: [{ kind: "image", filename: "inhaler.png", status: "sent" }, { kind: "voice", filename: "", status: "failed" }] }],
    })
    expect(md).toContain("- sent image `inhaler.png` (sent)\n- sent voice `file` (failed)\n\nIni")
  })
})

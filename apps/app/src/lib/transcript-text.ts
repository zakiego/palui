// Turns a loaded transcript into Markdown for the clipboard, so a conversation
// can be pasted into an issue, a note, or another model. Tool calls go in as a
// short list under the reply they belong to; their args and results stay out,
// since they can be long and are rarely what you want to paste.

type ToolLine = { tool: string; status: string; class: string }
type MessageLine = { role: "user" | "assistant"; source?: "hook" | null; text: string; createdAt: number; tools: ToolLine[] }
export type TranscriptForCopy = { chat: { id: string; channel: string; title: string }; messages: MessageLine[] }

export function transcriptToMarkdown(t: TranscriptForCopy): string {
  const head = [`# ${t.chat.title}`, "", `Channel: ${t.chat.channel}`, `Chat ID: ${t.chat.id}`]
  const body = t.messages.map((m) => {
    const who = m.source === "hook" ? "Webhook event" : m.role === "user" ? "User" : "Palui"
    const lines = [`## ${who} (${new Date(m.createdAt).toISOString()})`, ""]
    if (m.tools.length > 0) {
      for (const x of m.tools) lines.push(`- tool \`${x.tool}\` (${x.class}, ${x.status})`)
      lines.push("")
    }
    lines.push(m.text)
    return lines.join("\n")
  })
  return [head.join("\n"), ...body].join("\n\n") + "\n"
}

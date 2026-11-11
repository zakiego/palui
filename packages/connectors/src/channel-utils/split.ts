// Split a long message into chunks that fit a channel's size limit (Telegram 4096).
// Break preference: paragraph, line, sentence, word, then a hard cut. A cut that
// would land inside a fenced code block moves to just before the fence when that
// leaves a reasonable chunk; otherwise the fence is closed at the end of the chunk
// and reopened at the start of the next one, so every chunk renders on its own.

const FENCE = /^\s*```/
// Only the backticks and the language word are carried over to the next chunk.
// Carrying the whole opening line made a long line that starts with ``` grow the
// input on every pass, so the loop never ended.
const OPENER = /^\s*(```[\w+#.-]*)/
const MAX_OPENER = 24

function openerOf(line: string): string {
  const lang = OPENER.exec(line)?.[1] ?? "```"
  return lang.length <= MAX_OPENER ? lang : "```"
}

type FenceState = { open: boolean; opener: string }

function fenceStateAt(text: string): FenceState {
  let open = false
  let opener = ""
  for (const line of text.split("\n")) {
    if (FENCE.test(line)) {
      open = !open
      opener = open ? openerOf(line) : ""
    }
  }
  return { open, opener }
}

// Index just after the best natural break inside window (0 when none found).
function naturalBreak(window: string, min: number): number {
  const para = window.lastIndexOf("\n\n")
  if (para >= min) return para + 2
  const line = window.lastIndexOf("\n")
  if (line >= min) return line + 1
  let sentence = -1
  for (const m of window.matchAll(/[.!?][)"'\]]?\s/g)) sentence = (m.index ?? 0) + m[0].length
  if (sentence >= min) return sentence
  const space = window.lastIndexOf(" ")
  if (space >= min) return space + 1
  return 0
}

export function splitMessage(text: string, limit: number): string[] {
  if (limit < 16) throw new Error("splitMessage: limit too small")
  const chunks: string[] = []
  let rest = text
  // When a fence was split, the next chunk starts by reopening it.
  let reopen = ""

  while (rest.length > 0) {
    const input = reopen ? `${reopen}\n${rest}` : rest
    reopen = ""
    if (input.length <= limit) {
      chunks.push(input)
      break
    }
    // Leave room to close a fence ("\n```") if the cut lands inside one.
    const budget = limit - 4
    const window = input.slice(0, budget)
    const min = Math.floor(budget * 0.3)
    let cut = naturalBreak(window, min) || budget

    let state = fenceStateAt(input.slice(0, cut))
    if (state.open) {
      // Try cutting right before the fence opened.
      const before = input.slice(0, cut)
      const lines = before.split("\n")
      let pos = 0
      let openAt = -1
      let inFence = false
      for (const line of lines) {
        if (FENCE.test(line)) {
          inFence = !inFence
          if (inFence) openAt = pos
        }
        pos += line.length + 1
      }
      if (openAt >= min) {
        cut = openAt
        state = { open: false, opener: "" }
      } else {
        // The block itself is too big: cut on a line inside it and carry the fence over.
        const inner = input.slice(0, cut)
        const nl = inner.lastIndexOf("\n")
        if (nl > openAt + state.opener.length) cut = nl + 1
      }
    }

    let chunk = input.slice(0, cut).replace(/\s+$/, "")
    let next = input.slice(cut)
    if (state.open) {
      chunk = `${chunk}\n\`\`\``
      reopen = state.opener
      next = next.replace(/^\n+/, "")
    } else {
      next = next.replace(/^\s+/, "")
    }
    // Safety net: every pass must consume part of the original text. If it didn't,
    // hard cut the original text and drop the fence carry-over.
    if (next.length >= rest.length) {
      chunk = rest.slice(0, limit)
      next = rest.slice(limit)
      reopen = ""
    }
    if (chunk.trim().length) chunks.push(chunk)
    rest = next
    if (!rest.length && reopen) reopen = ""
  }
  return chunks
}

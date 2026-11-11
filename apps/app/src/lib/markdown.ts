// A small, safe Markdown subset for model text in the chat transcript: bold,
// italic, inline code, fenced code blocks, bullet and numbered lists, pipe
// tables, links and bare URLs. It parses to a plain tree that
// `components/markdown.tsx` renders as React elements, so model text never
// reaches the DOM as HTML. Anything outside the subset (raw HTML, images)
// stays literal text.

export type Inline =
  | { type: "text"; text: string }
  | { type: "strong"; children: Inline[] }
  | { type: "em"; children: Inline[] }
  | { type: "code"; text: string }
  | { type: "link"; href: string; children: Inline[] }
  | { type: "br" }

export type Block =
  | { type: "p"; children: Inline[] }
  | { type: "heading"; children: Inline[] }
  | { type: "code"; lang: string | null; text: string }
  | { type: "list"; ordered: boolean; start: number; items: Inline[][] }
  | { type: "table"; head: Inline[][]; rows: Inline[][][] }

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([\w+-]*)\s*$/
const BULLET = /^ {0,3}[-*+]\s+(.*)$/
const ORDERED = /^ {0,3}(\d{1,9})[.)]\s+(.*)$/
const HEADING = /^ {0,3}#{1,6}\s+(.*?)\s*#*\s*$/
// The line under a table's header: dashes between pipes, with optional colons.
const TABLE_RULE = /^ {0,3}\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/

// The cells of one table row. A pipe at either end is a border, not a cell,
// and `\|` is a literal pipe inside one.
function tableCells(line: string): string[] {
  const inner = line.trim().replace(/^\|/, "").replace(/(?<!\\)\|$/, "")
  return inner.split(/(?<!\\)\|/).map((cell) => cell.trim())
}

// Only these schemes become links. Everything else (javascript:, data:, a
// relative path) stays text.
export function safeHref(raw: string): string | null {
  const href = raw.trim()
  if (/^https?:\/\/[^\s]+$/i.test(href) || /^mailto:[^\s]+$/i.test(href)) return href
  return null
}

export function parseMarkdown(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n")
  const blocks: Block[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) blocks.push({ type: "p", children: parseInline(para.join("\n")) })
    para = []
  }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const fence = FENCE.exec(line)
    if (fence) {
      flush()
      const marker = fence[1]
      const body: string[] = []
      i++
      // An unclosed fence (a reply still streaming) runs to the end.
      while (i < lines.length && !(lines[i].trim().startsWith(marker[0].repeat(marker.length)) && lines[i].trim().replace(/[`~]/g, "") === "")) {
        body.push(lines[i])
        i++
      }
      blocks.push({ type: "code", lang: fence[2] || null, text: body.join("\n") })
      continue
    }
    if (line.trim() === "") {
      flush()
      continue
    }
    const heading = HEADING.exec(line)
    if (heading) {
      flush()
      blocks.push({ type: "heading", children: parseInline(heading[1]) })
      continue
    }
    // A table needs its rule line, so a header that is still streaming in
    // stays a paragraph until the next line arrives.
    if (line.includes("|") && i + 1 < lines.length && lines[i + 1].includes("|") && TABLE_RULE.test(lines[i + 1])) {
      flush()
      const head = tableCells(line)
      const rows: string[][] = []
      i += 2
      while (i < lines.length && lines[i].trim() !== "" && lines[i].includes("|")) {
        const cells = tableCells(lines[i])
        // Every row has the header's width: missing cells are empty, extras drop.
        rows.push(head.map((_, col) => cells[col] ?? ""))
        i++
      }
      i--
      blocks.push({ type: "table", head: head.map((c) => parseInline(c)), rows: rows.map((r) => r.map((c) => parseInline(c))) })
      continue
    }
    const bullet = BULLET.exec(line)
    const ordered = ORDERED.exec(line)
    if (bullet || ordered) {
      flush()
      const isOrdered = !bullet
      const start = ordered ? Number(ordered[1]) : 1
      const items: string[] = []
      while (i < lines.length) {
        const l = lines[i]
        const m = isOrdered ? ORDERED.exec(l) : BULLET.exec(l)
        if (m) {
          items.push(isOrdered ? m[2] : m[1])
        } else if (l.trim() !== "" && /^\s+/.test(l) && items.length) {
          // An indented continuation line belongs to the item above.
          items[items.length - 1] += `\n${l.trim()}`
        } else break
        i++
      }
      i--
      blocks.push({ type: "list", ordered: isOrdered, start, items: items.map((t) => parseInline(t)) })
      continue
    }
    para.push(line)
  }
  flush()
  return blocks
}

const URL_RE = /https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"\]]/y

export function parseInline(src: string): Inline[] {
  const out: Inline[] = []
  let text = ""
  const push = (node: Inline) => {
    if (text) out.push({ type: "text", text })
    text = ""
    out.push(node)
  }
  let i = 0
  while (i < src.length) {
    const ch = src[i]
    const rest = src.slice(i)
    // Backslash escapes a markdown character.
    if (ch === "\\" && i + 1 < src.length && /[\\`*_[\]()#+\-.!~]/.test(src[i + 1])) {
      text += src[i + 1]
      i += 2
      continue
    }
    if (ch === "\n") {
      push({ type: "br" })
      i++
      continue
    }
    if (ch === "`") {
      const run = /^`+/.exec(rest)![0]
      const end = src.indexOf(run, i + run.length)
      if (end > i) {
        push({ type: "code", text: src.slice(i + run.length, end).replace(/^ (.+) $/, "$1") })
        i = end + run.length
        continue
      }
      text += run
      i += run.length
      continue
    }
    if (ch === "*" || ch === "_") {
      const double = src[i + 1] === ch
      const marker = double ? ch + ch : ch
      const open = i + marker.length
      // Opening needs a non-space after it; `_` also can't sit inside a word
      // (snake_case_names stay as they are).
      const wordBefore = i > 0 && /[\p{L}\p{N}]/u.test(src[i - 1])
      if (src[open] && !/\s/.test(src[open]) && !(ch === "_" && wordBefore)) {
        const close = findClose(src, open, marker)
        if (close > open) {
          const children = parseInline(src.slice(open, close))
          push(double ? { type: "strong", children } : { type: "em", children })
          i = close + marker.length
          continue
        }
      }
      text += marker
      i += marker.length
      continue
    }
    if (ch === "[") {
      const m = /^\[([^\]\n]+)\]\(([^)\s]+)\)/.exec(rest)
      if (m) {
        const href = safeHref(m[2])
        if (href) push({ type: "link", href, children: parseInline(m[1]) })
        else text += m[1]
        i += m[0].length
        continue
      }
    }
    if ((ch === "h" || ch === "H") && (i === 0 || !/[\p{L}\p{N}/]/u.test(src[i - 1]))) {
      URL_RE.lastIndex = i
      const m = URL_RE.exec(src)
      if (m) {
        push({ type: "link", href: m[0], children: [{ type: "text", text: m[0] }] })
        i += m[0].length
        continue
      }
    }
    text += ch
    i++
  }
  if (text) out.push({ type: "text", text })
  return out
}

function findClose(src: string, from: number, marker: string): number {
  let j = from
  while (j < src.length) {
    const k = src.indexOf(marker, j)
    if (k < 0) return -1
    // The closing marker needs a non-space before it, isn't part of a longer
    // run (so `**` doesn't close `*`), and `_` can't be followed by a letter.
    const longer = src[k + marker.length] === marker[0] && marker.length === 1
    const after = src[k + marker.length]
    const blockedUnderscore = marker[0] === "_" && after !== undefined && /[\p{L}\p{N}]/u.test(after)
    if (k > from && !/\s/.test(src[k - 1]) && !longer && !blockedUnderscore && !src.slice(from, k).includes("\n\n")) return k
    j = k + (longer ? 2 : marker.length)
  }
  return -1
}

// Plain text for one-line previews (the chat list): the markers go, the words
// stay, and block breaks become spaces.
export function markdownToPlain(src: string): string {
  const flat = (nodes: Inline[]): string => nodes.map((n) => (n.type === "text" || n.type === "code" ? n.text : n.type === "br" ? " " : flat(n.children))).join("")
  return parseMarkdown(src)
    .map((b) => {
      if (b.type === "code") return b.text
      if (b.type === "list") return b.items.map(flat).join(", ")
      if (b.type === "table") return [b.head, ...b.rows].map((row) => row.map(flat).join(" ")).join(", ")
      return flat(b.children)
    })
    .join(" ")
    .replace(/\s+/g, " ")
    .trim()
}

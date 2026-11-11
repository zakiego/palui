// Markdown from the model, converted to what each channel renders.
//
// Telegram: HTML parse mode, which only accepts a small tag set (b, i, u, s, code,
// pre, a, blockquote) and rejects the whole message on a stray "<" or "&". So all
// text is escaped first and markdown markers are turned into tags afterwards.
// WhatsApp: *bold*, _italic_, ~strike~, ```mono```, `inline`. Links become text.
//
// Fenced code is never rewritten in either converter.

type Segment = { code: boolean; lang: string; body: string }

// Split into prose and fenced code runs. An unclosed fence runs to the end.
function splitFences(md: string): Segment[] {
  const out: Segment[] = []
  const lines = md.split("\n")
  let buf: string[] = []
  let inFence = false
  let lang = ""
  const flush = (code: boolean) => {
    if (buf.length || code) out.push({ code, lang, body: buf.join("\n") })
    buf = []
  }
  for (const line of lines) {
    const m = /^\s*```\s*([\w+#.-]*)\s*$/.exec(line) ?? (/^\s*```/.test(line) ? ["", ""] : null)
    if (m) {
      if (inFence) {
        flush(true)
        inFence = false
        lang = ""
      } else {
        flush(false)
        inFence = true
        lang = m[1] ?? ""
      }
      continue
    }
    buf.push(line)
  }
  flush(inFence)
  return out
}

const PH = "\u0000"
const ph = (i: number) => `${PH}${i}${PH}`
const restore = (s: string, saved: string[]) => s.replace(new RegExp(`${PH}(\\d+)${PH}`, "g"), (_m, i: string) => saved[Number(i)] ?? "")

export function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
}

function safeHref(url: string): string | null {
  const u = url.trim()
  if (!/^(https?:\/\/|tg:\/\/|mailto:)/i.test(u)) return null
  return escapeHtml(u).replace(/"/g, "&quot;")
}

function inlineToHtml(line: string, saved: string[]): string {
  let s = line
  // Inline code first, so markers inside it are left alone.
  s = s.replace(/`([^`\n]+)`/g, (_m, code: string) => ph(saved.push(`<code>${escapeHtml(code)}</code>`) - 1))
  // Links: [text](url). The text is formatted later with the rest of the line.
  s = s.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/g, (m, label: string, url: string) => {
    const href = safeHref(url)
    if (!href) return m
    const open = ph(saved.push(`<a href="${href}">`) - 1)
    const close = ph(saved.push("</a>") - 1)
    return `${open}${label}${close}`
  })
  s = escapeHtml(s)
  s = s.replace(/\*\*(?=\S)([^*\n]*?\S)\*\*/g, "<b>$1</b>")
  s = s.replace(/__(?=\S)([^_\n]*?\S)__/g, "<b>$1</b>")
  s = s.replace(/~~(?=\S)([^~\n]*?\S)~~/g, "<s>$1</s>")
  s = s.replace(/(^|[^\w*])\*(?=\S)([^*\n]*?\S)\*(?![\w*])/g, "$1<i>$2</i>")
  s = s.replace(/(^|[^\w])_(?=\S)([^_\n]*?\S)_(?!\w)/g, "$1<i>$2</i>")
  return s
}

export function markdownToTelegramHtml(md: string): string {
  const saved: string[] = []
  const parts: string[] = []
  for (const seg of splitFences(md)) {
    if (seg.code) {
      const cls = seg.lang ? ` class="language-${escapeHtml(seg.lang)}"` : ""
      parts.push(`<pre><code${cls}>${escapeHtml(seg.body)}</code></pre>`)
      continue
    }
    const lines = seg.body.split("\n")
    const out: string[] = []
    let quote: string[] = []
    const flushQuote = () => {
      if (quote.length) out.push(`<blockquote>${quote.join("\n")}</blockquote>`)
      quote = []
    }
    for (const raw of lines) {
      const q = /^\s*>\s?(.*)$/.exec(raw)
      if (q) {
        quote.push(inlineToHtml(q[1] ?? "", saved))
        continue
      }
      flushQuote()
      const h = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.exec(raw)
      if (h) {
        out.push(`<b>${inlineToHtml(h[1] ?? "", saved)}</b>`)
        continue
      }
      const bullet = /^(\s*)[-*+]\s+(.*)$/.exec(raw)
      if (bullet) {
        out.push(`${bullet[1] ?? ""}• ${inlineToHtml(bullet[2] ?? "", saved)}`)
        continue
      }
      if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(raw)) {
        out.push("──────")
        continue
      }
      out.push(inlineToHtml(raw, saved))
    }
    flushQuote()
    parts.push(out.join("\n"))
  }
  return restore(parts.join("\n"), saved)
}

const BOLD = "\u0001"

function inlineToWhatsApp(line: string, saved: string[]): string {
  let s = line
  s = s.replace(/`([^`\n]+)`/g, (m) => ph(saved.push(m) - 1))
  s = s.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/g, (_m, label: string, url: string) => (label.trim() === url.trim() ? url : `${label} (${url})`))
  s = s.replace(/\*\*(?=\S)([^*\n]*?\S)\*\*/g, `${BOLD}$1${BOLD}`)
  s = s.replace(/__(?=\S)([^_\n]*?\S)__/g, `${BOLD}$1${BOLD}`)
  s = s.replace(/~~(?=\S)([^~\n]*?\S)~~/g, "~$1~")
  s = s.replace(/(^|[^\w*])\*(?=\S)([^*\n]*?\S)\*(?![\w*])/g, "$1_$2_")
  return s
}

export function markdownToWhatsApp(md: string): string {
  const saved: string[] = []
  const parts: string[] = []
  for (const seg of splitFences(md)) {
    if (seg.code) {
      parts.push(`\`\`\`\n${seg.body}\n\`\`\``)
      continue
    }
    const out = seg.body.split("\n").map((raw) => {
      const h = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.exec(raw)
      if (h) return `${BOLD}${inlineToWhatsApp(h[1] ?? "", saved).replaceAll(BOLD, "")}${BOLD}`
      const bullet = /^(\s*)[*+]\s+(.*)$/.exec(raw)
      if (bullet) return `${bullet[1] ?? ""}- ${inlineToWhatsApp(bullet[2] ?? "", saved)}`
      return inlineToWhatsApp(raw, saved)
    })
    parts.push(out.join("\n"))
  }
  return restore(parts.join("\n"), saved).replaceAll(BOLD, "*")
}

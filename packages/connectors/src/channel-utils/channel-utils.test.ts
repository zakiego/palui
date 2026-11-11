import { describe, expect, it } from "vitest"
import { actionsHint, matchActionReply, markdownToTelegramHtml, markdownToWhatsApp, splitMessage } from "./index"

describe("splitMessage", () => {
  it("returns short text as one chunk", () => {
    expect(splitMessage("halo", 100)).toEqual(["halo"])
  })

  it("prefers paragraph breaks", () => {
    const a = "a".repeat(60)
    const b = "b".repeat(60)
    expect(splitMessage(`${a}\n\n${b}`, 100)).toEqual([a, b])
  })

  it("falls back to lines, then sentences, then words", () => {
    const line = `${"x".repeat(50)}\n${"y".repeat(70)}`
    expect(splitMessage(line, 100)).toEqual(["x".repeat(50), "y".repeat(70)])
    const sentences = `${"Kalimat satu panjang sekali. ".repeat(3)}${"z".repeat(40)}`
    const chunks = splitMessage(sentences, 100)
    expect(chunks[0]?.endsWith(".")).toBe(true)
    const words = Array.from({ length: 40 }, (_, i) => `kata${i}`).join(" ")
    for (const c of splitMessage(words, 50)) {
      expect(c.length).toBeLessThanOrEqual(50)
      expect(c).not.toMatch(/^\s|\s$/)
    }
  })

  it("hard cuts a single long word", () => {
    const chunks = splitMessage("w".repeat(250), 100)
    expect(chunks.join("")).toBe("w".repeat(250))
    for (const c of chunks) expect(c.length).toBeLessThanOrEqual(100)
  })

  it("never exceeds the limit and keeps all words", () => {
    const text = Array.from({ length: 300 }, (_, i) => (i % 17 === 0 ? `kalimat${i}.\n\n` : `kata${i} `)).join("")
    const chunks = splitMessage(text, 200)
    for (const c of chunks) expect(c.length).toBeLessThanOrEqual(200)
    expect(chunks.join(" ").replace(/\s+/g, " ").trim()).toBe(text.replace(/\s+/g, " ").trim())
  })

  it("moves the cut before a code fence when possible", () => {
    const intro = "Intro text that is long enough to stand alone. ".repeat(2).trim()
    const code = `\`\`\`ts\n${"const x = 1\n".repeat(6)}\`\`\``
    const chunks = splitMessage(`${intro}\n${code}`, 120)
    expect(chunks[0]).toBe(intro)
    expect(chunks[1]?.startsWith("```ts")).toBe(true)
  })

  it("closes and reopens a fence that is bigger than the limit", () => {
    const code = `\`\`\`py\n${Array.from({ length: 30 }, (_, i) => `print(${i})`).join("\n")}\n\`\`\``
    const chunks = splitMessage(code, 100)
    expect(chunks.length).toBeGreaterThan(1)
    for (const c of chunks) {
      expect(c.length).toBeLessThanOrEqual(100)
      expect(c.startsWith("```py")).toBe(true)
      expect(c.trimEnd().endsWith("```")).toBe(true)
    }
  })

  it("terminates when a long line starting with a fence is the opener", () => {
    // Regression: the whole line used to be re-prefixed on every chunk (OOM).
    const longFenceLine = `\`\`\`${"kata ".repeat(300)}`
    const text = `${"intro ".repeat(50)}\n${longFenceLine}\n${"isi baris\n".repeat(400)}`
    const chunks = splitMessage(text, 4096)
    expect(chunks.length).toBeGreaterThan(1)
    for (const c of chunks) expect(c.length).toBeLessThanOrEqual(4096)
    for (const c of chunks.slice(1)) expect(c.startsWith("```kata\n") || !c.startsWith("```")).toBe(true)
  })

  it("terminates on random long inputs and keeps every chunk within the limit", () => {
    let seed = 42
    const rand = () => {
      seed = (seed * 1103515245 + 12345) % 2147483648
      return seed / 2147483648
    }
    const pieces = ["```", "```ts", "```" + "x".repeat(40), "\n", "\n\n", " ", ". ", "word", "y".repeat(300), "kata ".repeat(80)]
    for (let round = 0; round < 200; round++) {
      const limit = [16, 50, 100, 4096][round % 4]
      let text = ""
      const target = limit * (2 + Math.floor(rand() * 6))
      while (text.length < target) text += pieces[Math.floor(rand() * pieces.length)]
      const chunks = splitMessage(text, limit)
      expect(chunks.length).toBeLessThan(text.length + 2)
      for (const c of chunks) expect(c.length).toBeLessThanOrEqual(limit)
    }
  })
})

describe("markdownToTelegramHtml", () => {
  it("escapes html special characters", () => {
    expect(markdownToTelegramHtml("a < b && c > d")).toBe("a &lt; b &amp;&amp; c &gt; d")
    expect(markdownToTelegramHtml("<script>alert(1)</script>")).toBe("&lt;script&gt;alert(1)&lt;/script&gt;")
  })

  it("converts bold, italic, strike and inline code", () => {
    expect(markdownToTelegramHtml("**tebal** dan *miring* dan _miring_ ~~coret~~ `kode <x>`")).toBe(
      "<b>tebal</b> dan <i>miring</i> dan <i>miring</i> <s>coret</s> <code>kode &lt;x&gt;</code>",
    )
  })

  it("leaves snake_case and markers inside code alone", () => {
    expect(markdownToTelegramHtml("pakai snake_case_name dan `**bukan**`")).toBe("pakai snake_case_name dan <code>**bukan**</code>")
  })

  it("renders code blocks as pre", () => {
    expect(markdownToTelegramHtml("lihat:\n```ts\nif (a < b) **x**\n```\nselesai")).toBe(
      'lihat:\n<pre><code class="language-ts">if (a &lt; b) **x**</code></pre>\nselesai',
    )
  })

  it("renders links and drops unsafe schemes", () => {
    expect(markdownToTelegramHtml("[Palui](https://palui.zakiego.com/?a=1&b=2)")).toBe('<a href="https://palui.zakiego.com/?a=1&amp;b=2">Palui</a>')
    expect(markdownToTelegramHtml("[x](javascript:alert(1))")).toBe("[x](javascript:alert(1))")
  })

  it("renders headings, bullets and quotes", () => {
    expect(markdownToTelegramHtml("# Judul\n- satu\n* dua\n> kutip")).toBe("<b>Judul</b>\n• satu\n• dua\n<blockquote>kutip</blockquote>")
  })
})

describe("markdownToWhatsApp", () => {
  it("converts bold, italic, strike and headings", () => {
    expect(markdownToWhatsApp("**tebal** *miring* _miring_ ~~coret~~")).toBe("*tebal* _miring_ _miring_ ~coret~")
    expect(markdownToWhatsApp("## Judul")).toBe("*Judul*")
  })

  it("keeps code untouched and turns links into text", () => {
    expect(markdownToWhatsApp("```\na **b**\n```")).toBe("```\na **b**\n```")
    expect(markdownToWhatsApp("`**x**` lihat [docs](https://x.id)")).toBe("`**x**` lihat docs (https://x.id)")
    expect(markdownToWhatsApp("[https://x.id](https://x.id)")).toBe("https://x.id")
  })

  it("turns star bullets into dashes", () => {
    expect(markdownToWhatsApp("* satu\n- dua")).toBe("- satu\n- dua")
  })
})

describe("actionsHint", () => {
  const reminder = [
    { id: "rem:done:r1", label: "Done" },
    { id: "rem:snooze:r1:1h", label: "Tunda 1 jam" },
    { id: "rem:snooze:r1:tomorrow", label: "Besok" },
  ]

  it("builds reply hints from action ids", () => {
    expect(actionsHint(reminder)).toBe("Balas: done / tunda 1j / tunda besok")
    expect(actionsHint([{ id: "approve:a1", label: "Setuju" }, { id: "deny:a1", label: "Tolak" }])).toBe("Balas: ya / batal")
    expect(actionsHint([{ id: "custom", label: "Lanjut" }])).toBe("Balas: lanjut")
    expect(actionsHint([])).toBe("")
    expect(actionsHint(undefined)).toBe("")
  })

  it("matches a typed reply back to its action", () => {
    expect(matchActionReply(" Tunda Besok ", reminder)?.id).toBe("rem:snooze:r1:tomorrow")
    expect(matchActionReply("nanti", reminder)).toBeUndefined()
  })
})

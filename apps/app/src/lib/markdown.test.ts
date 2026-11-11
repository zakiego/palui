import { describe, expect, it } from "vitest"
import { markdownToPlain, parseInline, parseMarkdown, safeHref } from "./markdown"

describe("parseInline", () => {
  it("parses bold, italic and inline code", () => {
    expect(parseInline("a **b** *c* `d`")).toEqual([
      { type: "text", text: "a " },
      { type: "strong", children: [{ type: "text", text: "b" }] },
      { type: "text", text: " " },
      { type: "em", children: [{ type: "text", text: "c" }] },
      { type: "text", text: " " },
      { type: "code", text: "d" },
    ])
  })

  it("nests italic inside bold", () => {
    expect(parseInline("**a *b* c**")).toEqual([{ type: "strong", children: [{ type: "text", text: "a " }, { type: "em", children: [{ type: "text", text: "b" }] }, { type: "text", text: " c" }] }])
  })

  it("leaves snake_case and lone stars alone", () => {
    expect(parseInline("mcp_vorneo__list_deals and 2 * 3 * 4")).toEqual([{ type: "text", text: "mcp_vorneo__list_deals and 2 * 3 * 4" }])
  })

  it("keeps markdown inside code literal", () => {
    expect(parseInline("`**x**`")).toEqual([{ type: "code", text: "**x**" }])
  })

  it("links only http, https and mailto", () => {
    expect(parseInline("[ok](https://palui.zakiego.com)")).toEqual([{ type: "link", href: "https://palui.zakiego.com", children: [{ type: "text", text: "ok" }] }])
    expect(parseInline("[bad](javascript:alert(1))").some((n) => n.type === "link")).toBe(false)
    expect(parseInline("[x](data:text/html,hi)").some((n) => n.type === "link")).toBe(false)
  })

  it("autolinks bare URLs without trailing punctuation", () => {
    expect(parseInline("see https://example.com/a?b=1.")).toEqual([
      { type: "text", text: "see " },
      { type: "link", href: "https://example.com/a?b=1", children: [{ type: "text", text: "https://example.com/a?b=1" }] },
      { type: "text", text: "." },
    ])
  })

  it("keeps raw HTML as text", () => {
    expect(parseInline('<img src=x onerror="alert(1)">')).toEqual([{ type: "text", text: '<img src=x onerror="alert(1)">' }])
  })

  it("turns single newlines into breaks", () => {
    expect(parseInline("a\nb")).toEqual([{ type: "text", text: "a" }, { type: "br" }, { type: "text", text: "b" }])
  })

  it("honors backslash escapes", () => {
    expect(parseInline("\\*not em\\*")).toEqual([{ type: "text", text: "*not em*" }])
  })
})

describe("parseMarkdown", () => {
  it("splits paragraphs, lists and code blocks", () => {
    const blocks = parseMarkdown("Siap.\n\n- satu\n- **dua**\n\n1. a\n2. b\n\n```ts\nconst x = 1\n```")
    expect(blocks.map((b) => b.type)).toEqual(["p", "list", "list", "code"])
    expect(blocks[1]).toMatchObject({ type: "list", ordered: false, items: [[{ type: "text", text: "satu" }], [{ type: "strong" }]] })
    expect(blocks[2]).toMatchObject({ type: "list", ordered: true, start: 1 })
    expect(blocks[3]).toEqual({ type: "code", lang: "ts", text: "const x = 1" })
  })

  it("runs an unclosed fence to the end, for a reply still streaming", () => {
    expect(parseMarkdown("```\nline 1\nline 2")).toEqual([{ type: "code", lang: null, text: "line 1\nline 2" }])
  })

  it("keeps the start number of an ordered list", () => {
    expect(parseMarkdown("3. tiga\n4. empat")[0]).toMatchObject({ ordered: true, start: 3 })
  })

  it("reads a heading as a heading block", () => {
    expect(parseMarkdown("## Ringkasan")[0]).toEqual({ type: "heading", children: [{ type: "text", text: "Ringkasan" }] })
  })

  it("joins an indented continuation into the list item", () => {
    expect(parseMarkdown("- a\n  still a\n- b")[0]).toMatchObject({ items: [[{ type: "text", text: "a" }, { type: "br" }, { type: "text", text: "still a" }], [{ type: "text", text: "b" }]] })
  })
})

describe("safeHref", () => {
  it("accepts web and mail links only", () => {
    expect(safeHref("https://a.b")).toBe("https://a.b")
    expect(safeHref("mailto:z@a.b")).toBe("mailto:z@a.b")
    expect(safeHref("javascript:alert(1)")).toBeNull()
    expect(safeHref("/app/saklar")).toBeNull()
  })
})

describe("markdownToPlain", () => {
  it("drops the markers and keeps the words", () => {
    expect(markdownToPlain("**Rekap minggu ini** (W46):\n\n1. *3 deal* masuk\n2. `bayar`")).toBe("Rekap minggu ini (W46): 3 deal masuk, bayar")
  })
})

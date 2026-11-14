// Exports M7 channel goldens from the real TS code into
// apps/server/testdata/channels.json:
//   split      splitMessage on a wide table of texts and limits (paragraphs,
//              lines, sentences, words, hard cuts, fences, emoji and mixed
//              scripts at the boundaries)
//   telegram   markdownToTelegramHtml on hand-written and generated markdown
//   whatsapp   markdownToWhatsApp on the same inputs
//   render     renderTelegramChunks
//   actions    actionReplyWord, actionsHint and matchActionReply
//   manifest   channel manifest config and secret parses (zod issues)
//   tgSends    Telegram sends: the Bot API calls for scripted payloads
//              (chunks, keyboards, replies, the HTML fallback, errors)
//   tgInbound  Telegram updates in, the inbound messages and calls out
//   waInbound  Baileys messages in, the inbound messages out
//   waSends    WhatsApp sends: the socket messages for scripted payloads
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/channels.ts
import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import type { Update } from "grammy/types"
import type { WAMessage } from "baileys"
import { setConfig } from "../../../../packages/core/src/config.ts"
import { claimOwner, createClaim } from "../../../../packages/core/src/owner.ts"
import { PALUI_VERSION } from "../../../../packages/core/src/version.ts"
import { actionReplyWord, actionsHint, markdownToTelegramHtml, markdownToWhatsApp, matchActionReply, splitMessage } from "../../../../packages/connectors/src/channel-utils/index.ts"
import { testContext } from "../../../../packages/connectors/src/channel-utils/testing.ts"
import { createTelegramRuntime, renderTelegramChunks } from "../../../../packages/connectors/src/telegram/runtime.ts"
import { FakeApiError, fakeTelegram } from "../../../../packages/connectors/src/telegram/test-transport.ts"
import telegramManifest from "../../../../packages/connectors/src/telegram/manifest.ts"
import whatsappManifest from "../../../../packages/connectors/src/whatsapp/manifest.ts"
import { createWhatsAppRuntime } from "../../../../packages/connectors/src/whatsapp/runtime.ts"
import { FakeSocket } from "../../../../packages/connectors/src/whatsapp/test-socket.ts"

setConfig({ mode: "prod", prodTokenHashes: [] })

// --- generated inputs ------------------------------------------------------------------

function prng(seed: number) {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

const MD_TOKENS = [
  "**", "*", "_", "__", "~~", "~", "`", "```", "```ts", "```py ", "\n", "\n", "\n\n", " ", "  ", "\t", "a", "kata", "Halo", "dua kata",
  "snake_case", "x*y", "2*3*4", "_x_", "*b*", "**c**", "~~d~~", "`e`", "[link](https://x.id/a_b)", "[x](javascript:alert(1))",
  "[a](tg://resolve?domain=x)", "[m](mailto:a@b.c)", "[spasi](http://x y)", "[**tebal**](https://t.me)", "[a [b]](https://c.d)",
  "# ", "### ", "####### ", "    # ", "- ", "* ", "+ ", "> ", ">", "  - ", "---", "***", "___", "- - -", "<", ">", "&", "\"", "'",
  "😀", "👨‍👩‍👧", "日本語", "ünïcödé", "مرحبا", "\r", " ", " ", "|", "| a | b |", "|---|---|", "1. ", "#tag", "@user",
  "!", ".", "?", ") ", "\u0001", "\u00000\u0000",
]

function randomMarkdown(rand: () => number, n: number): string {
  let s = ""
  for (let i = 0; i < n; i++) s += MD_TOKENS[Math.floor(rand() * MD_TOKENS.length)]
  return s
}

const HAND_MD = [
  "",
  "halo",
  "a < b && c > d",
  "<script>alert(1)</script>",
  "**tebal** dan *miring* dan _miring_ ~~coret~~ `kode <x>`",
  "pakai snake_case_name dan `**bukan**`",
  "lihat:\n```ts\nif (a < b) **x**\n```\nselesai",
  "[Palui](https://palui.zakiego.com/?a=1&b=2)",
  "[x](javascript:alert(1))",
  "# Judul\n- satu\n* dua\n> kutip",
  "**tebal** *miring* _miring_ ~~coret~~",
  "## Judul",
  "```\na **b**\n```",
  "`**x**` lihat [docs](https://x.id)",
  "[https://x.id](https://x.id)",
  "* satu\n- dua",
  "# Rekap minggu ini ##\n\n## Deal **baru** ###   \n###### enam\n####### tujuh",
  "- satu\n  - dua\n    * tiga\n      + empat\n1. nomor\n2. dua",
  "> kutip satu\n> kutip **dua**\n>tanpa spasi\nbukan kutip\n> lagi",
  "---\n***\n___\n- - -\n* * *\n--",
  "| Kabupaten | Deal |\n|---|---:|\n| Banjarbaru | 1,284 |\n| Hulu Sungai | 0 |",
  "```py\nprint('<a>')\n```\n```\nunclosed fence **tebal**",
  "   ```js  \nconst a = 1\n   ```  ",
  "```c++\nint x;\n```",
  "```a b\nlang with space\n```",
  "Emoji 😀 **tebal 😀** _miring 日本_ ~~coret مرحبا~~",
  "**a *b** c*",
  "***tebal miring***",
  "**unclosed bold and *italic",
  "a**b**c a*b*c a_b_c a__b__c",
  "** spasi** *spasi * _ spasi_",
  "2*3*4 = 24 dan x * y",
  "file_name_here.txt dan __init__.py",
  "~~a~~b ~~ c~~ ~d~",
  "[link **tebal**](https://x.id) dan [a](https://b.c \"title\")",
  "[](https://empty.label) [label]() [x](https://a.b/c)d",
  "teks\r\n# judul\r\n- item\r\n> kutip\r",
  "baris pemisah dan paragraf",
  "nbsp **tebal** x",
  "`` kosong `` dan `satu` dan ` spasi `",
  "inline `kode\nbaris` putus",
  "\u00000\u0000 placeholder injection `x` \u00001\u0000 \u000099\u0000",
  "#tag bukan judul\n#\n# \n#  \n  ## dua spasi",
  "> # judul dalam kutip\n> - bullet dalam kutip\n> ```\n> kode\n> ```",
  "Halo **Zaki**!\n\nIni rekap:\n- **3 deal** masuk\n- `bayar` jam 9\n\n> catatan: jangan lupa\n\n[Buka dashboard](https://palui.zakiego.com/app)",
]

// --- split --------------------------------------------------------------------------

const SPLIT_PIECES = ["```", "```ts", "```" + "x".repeat(40), "\n", "\n\n", " ", ". ", "word", "y".repeat(300), "kata ".repeat(80), "😀", "日本語 ", "! ", "?) ", ".\" ", "\t"]

const splitCases: { text: string; limit: number }[] = []
{
  const a = "a".repeat(60)
  const b = "b".repeat(60)
  splitCases.push({ text: "halo", limit: 100 })
  splitCases.push({ text: `${a}\n\n${b}`, limit: 100 })
  splitCases.push({ text: `${"x".repeat(50)}\n${"y".repeat(70)}`, limit: 100 })
  splitCases.push({ text: `${"Kalimat satu panjang sekali. ".repeat(3)}${"z".repeat(40)}`, limit: 100 })
  splitCases.push({ text: Array.from({ length: 40 }, (_, i) => `kata${i}`).join(" "), limit: 50 })
  splitCases.push({ text: "w".repeat(250), limit: 100 })
  splitCases.push({ text: Array.from({ length: 300 }, (_, i) => (i % 17 === 0 ? `kalimat${i}.\n\n` : `kata${i} `)).join(""), limit: 200 })
  const intro = "Intro text that is long enough to stand alone. ".repeat(2).trim()
  splitCases.push({ text: `${intro}\n\`\`\`ts\n${"const x = 1\n".repeat(6)}\`\`\``, limit: 120 })
  splitCases.push({ text: `\`\`\`py\n${Array.from({ length: 30 }, (_, i) => `print(${i})`).join("\n")}\n\`\`\``, limit: 100 })
  splitCases.push({ text: `${"intro ".repeat(50)}\n\`\`\`${"kata ".repeat(300)}\n${"isi baris\n".repeat(400)}`, limit: 4096 })
  splitCases.push({ text: "😀".repeat(40), limit: 16 })
  splitCases.push({ text: "😀".repeat(40), limit: 17 })
  splitCases.push({ text: `${"a".repeat(11)}😀${"b".repeat(30)}`, limit: 16 })
  splitCases.push({ text: "日本語のテキスト。".repeat(30), limit: 50 })
  splitCases.push({ text: "مرحبا بالعالم. ".repeat(20), limit: 64 })
  splitCases.push({ text: "Satu! Dua? Tiga.) Empat.\" Lima.' Enam.] Tujuh. ".repeat(6), limit: 60 })
  splitCases.push({ text: "baris\n".repeat(50), limit: 32 })
  splitCases.push({ text: "   spasi di depan dan belakang   ".repeat(10), limit: 40 })
  splitCases.push({ text: "\n\n\n" + "x".repeat(40) + "\n\n\n", limit: 20 })
  splitCases.push({ text: "```\n" + "kode panjang ".repeat(40) + "\n```", limit: 64 })
  splitCases.push({ text: "pembuka\n```ts\n" + "const a = 1\n".repeat(20) + "```\npenutup", limit: 80 })
  splitCases.push({ text: "a\n```\nb\n```\nc\n```\nd\n" + "e".repeat(80), limit: 30 })
  splitCases.push({ text: "Halo **Zaki**!\n\nIni rekap:\n- **3 deal** masuk\n- `bayar`\n\n".repeat(80), limit: 4096 })
  splitCases.push({ text: ("Kalimat yang lumayan panjang buat ngetes pemotongan. ".repeat(40) + "\n\n").repeat(3).trim(), limit: 4096 })
  const rand = prng(7)
  for (let round = 0; round < 120; round++) {
    const limit = [16, 17, 20, 32, 50, 64, 100, 128][round % 8]
    let text = ""
    const target = limit * (2 + Math.floor(rand() * 6))
    while (text.length < target) text += SPLIT_PIECES[Math.floor(rand() * SPLIT_PIECES.length)]
    splitCases.push({ text, limit })
  }
  for (let round = 0; round < 4; round++) {
    let text = ""
    while (text.length < 4096 * 3) text += SPLIT_PIECES[Math.floor(rand() * SPLIT_PIECES.length)]
    splitCases.push({ text, limit: 4096 })
  }
}
const split = splitCases.map((c) => ({ ...c, chunks: splitMessage(c.text, c.limit) }))

// --- markdown ------------------------------------------------------------------------

const mdInputs = [...HAND_MD]
{
  const rand = prng(42)
  for (let i = 0; i < 400; i++) mdInputs.push(randomMarkdown(rand, 1 + Math.floor(rand() * 30)))
}
const telegram = mdInputs.map((md) => ({ md, out: markdownToTelegramHtml(md) }))
const whatsapp = mdInputs.map((md) => ({ md, out: markdownToWhatsApp(md) }))

const render = [
  { md: "&".repeat(5000), limit: 4096 },
  { md: "<".repeat(3000) + " " + ">".repeat(3000), limit: 4096 },
  { md: "**tebal** & <x> ".repeat(600), limit: 4096 },
  { md: "&".repeat(700), limit: 300 },
  { md: "a & b ".repeat(200), limit: 300 },
  { md: HAND_MD[HAND_MD.length - 1]!.repeat(40), limit: 4096 },
  { md: "```ts\n" + "if (a < b && c > d) {}\n".repeat(300) + "```", limit: 4096 },
].map((c) => ({ ...c, chunks: renderTelegramChunks(c.md, c.limit) }))

// --- actions -------------------------------------------------------------------------

const ACTION_SETS = [
  [
    { id: "rem:done:r1", label: "Done" },
    { id: "rem:snooze:r1:1h", label: "Tunda 1 jam" },
    { id: "rem:snooze:r1:tomorrow", label: "Besok" },
  ],
  [
    { id: "approve:a1", label: "Setuju" },
    { id: "deny:a1", label: "Tolak" },
  ],
  [{ id: "custom", label: "  Lanjut Aja  " }],
  [
    { id: "rem:snooze:r1:10m", label: "10 menit" },
    { id: "rem:snooze:r1:2h", label: "Dua Jam" },
    { id: "rem:snooze:r1", label: "Tunda" },
  ],
  [
    { id: "connector:off:whatsapp", label: "Matikan" },
    { id: "connector:on:mcp-a:b", label: "Nyalakan" },
    { id: "connector:on", label: "Kosong" },
  ],
  [
    { id: "fl:flr_1:ya", label: "Ya" },
    { id: "fl:flr_1:tidak", label: "Ya" },
    { id: "flowver:approve:fv_1", label: "" },
  ],
  [],
]
const TYPED = [" Tunda Besok ", "nanti", "YA", "batal", "done", "lanjut aja", "/connectors off whatsapp", "", "  ", "tunda 10m", "ya"]
const actions = ACTION_SETS.map((set) => ({
  actions: set,
  words: set.map(actionReplyWord),
  hint: actionsHint(set),
  matches: TYPED.map((text) => ({ text, id: matchActionReply(text, set)?.id ?? null })),
}))

// --- manifests -----------------------------------------------------------------------

type Zodish = { safeParse(v: unknown): { success: boolean; data?: unknown; error?: { issues: unknown[] } } }
function parseOf(schema: Zodish, input: unknown) {
  const r = schema.safeParse(input)
  return r.success ? { ok: true, value: r.data } : { ok: false, issues: JSON.parse(JSON.stringify(r.error!.issues)) }
}
const TOKENS = ["123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq", "nope", "123:short", "12345:" + "a-_".repeat(10), "abc:" + "x".repeat(40), ""]
const PHONES = ["0812-3456-789", "+62 812 3456 789", "(0812) 3456.789", "abc", "0812", "+".repeat(1) + "1".repeat(24), "1".repeat(25), "0812 3456 789x"]
const manifest = [
  ...TOKENS.map((v) => ({ type: "telegram", kind: "secret", key: "botToken", input: v, result: parseOf(telegramManifest.secrets.botToken as Zodish, v) })),
  ...PHONES.map((v) => ({ type: "whatsapp", kind: "config", key: "ownerPhone", input: { ownerPhone: v }, result: parseOf(whatsappManifest.config as Zodish, { ownerPhone: v }) })),
  ...PHONES.map((v) => ({ type: "whatsapp", kind: "config", key: "pairPhone", input: { pairPhone: v, linkedNumber: "628" }, result: parseOf(whatsappManifest.config as Zodish, { pairPhone: v, linkedNumber: "628" }) })),
  { type: "telegram", kind: "config", key: "botUsername", input: { botUsername: 5 }, result: parseOf(telegramManifest.config as Zodish, { botUsername: 5 }) },
  { type: "whatsapp", kind: "config", key: "linkedNumber", input: { linkedNumber: 628 }, result: parseOf(whatsappManifest.config as Zodish, { linkedNumber: 628 }) },
]

// --- telegram ------------------------------------------------------------------------

const TOKEN = "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"
const json = (v: unknown) => JSON.parse(JSON.stringify(v)) as unknown
const callsOf = (calls: { method: string; payload: Record<string, unknown> }[]) =>
  calls.filter((c) => c.method !== "getMe" && c.method !== "deleteWebhook").map((c) => ({ method: c.method, payload: json(c.payload) }))

async function settle() {
  for (let i = 0; i < 30; i++) await new Promise((r) => setImmediate(r))
}

type TgSend = { name: string; payload: Record<string, unknown>; fail?: "parse" | "forbidden" | "parse-always" }
const longPara = "Kalimat yang lumayan panjang buat ngetes pemotongan. ".repeat(40)
const TG_SENDS: TgSend[] = [
  {
    name: "html with keyboard and reply",
    payload: { text: "**Pengingat**: minum <obat>", format: "markdown", replyToChannelMsgId: "55", actions: [{ id: "rem:done:r1", label: "Done" }, { id: "rem:snooze:r1:1h", label: "1 jam" }, { id: "x".repeat(65), label: "too long" }] },
  },
  { name: "long reply split, keyboard last", payload: { text: [longPara, longPara, longPara].join("\n\n"), actions: [{ id: "approve:a1", label: "Ya" }], replyToChannelMsgId: "9" } },
  { name: "html fallback to plain", payload: { text: "**a *b** c*" }, fail: "parse" },
  { name: "html fallback on every chunk", payload: { text: ("**x** " + "y".repeat(50) + "\n\n").repeat(120), replyToChannelMsgId: "3", actions: [{ id: "a", label: "A" }] }, fail: "parse-always" },
  { name: "other errors propagate", payload: { text: "hai" }, fail: "forbidden" },
  { name: "plain format", payload: { text: "**tidak** <diubah> & " + "kata ".repeat(1000), format: "plain", replyToChannelMsgId: "12" } },
  { name: "empty text", payload: { text: "" } },
  { name: "whitespace only", payload: { text: "   \n\n  " } },
  { name: "seven buttons in rows of three", payload: { text: "pilih", actions: Array.from({ length: 7 }, (_, i) => ({ id: `opt:${i}`, label: `Opsi ${i} 😀` })) } },
  { name: "multibyte callback data", payload: { text: "x", actions: [{ id: "é".repeat(32), label: "64 bytes" }, { id: "é".repeat(33), label: "66 bytes" }, { id: "", label: "empty" }] } },
  { name: "reply id not a number", payload: { text: "x", replyToChannelMsgId: "abc" } },
  { name: "reply id padded", payload: { text: "x", replyToChannelMsgId: " 12 " } },
  { name: "reply id hex", payload: { text: "x", replyToChannelMsgId: "0x10" } },
  { name: "reply id fraction", payload: { text: "x", replyToChannelMsgId: "1.5" } },
  { name: "markdown everything", payload: { text: HAND_MD[HAND_MD.length - 1], format: "markdown" } },
  { name: "html over the limit", payload: { text: "&".repeat(5000) } },
]

async function tgStarted(fail?: TgSend["fail"]) {
  const tg = fakeTelegram()
  const fetched: string[] = []
  const runtime = createTelegramRuntime({
    createBot: tg.createBot,
    fetchFile: async (url) => {
      fetched.push(url)
      const name = url.split("/").pop() ?? ""
      return name.startsWith("huge") ? Buffer.alloc(2 * 1024 * 1024, 65) : Buffer.from(`bytes:${name}`)
    },
  })
  let next = 100
  const ok = (p: Record<string, unknown>) => ({ message_id: next++, date: 0, chat: { id: Number(p.chat_id), type: "private" }, text: p.text })
  if (fail === "parse") tg.on("sendMessage", (p) => (p.parse_mode ? new FakeApiError(400, "Bad Request: can't parse entities: unexpected end tag") : ok(p)))
  if (fail === "parse-always") tg.on("sendMessage", (p) => (p.parse_mode ? new FakeApiError(400, "Bad Request: unsupported start TAG \"x\"") : ok(p)))
  if (fail === "forbidden") tg.on("sendMessage", () => new FakeApiError(403, "Forbidden: bot was blocked by the user"))
  const ctx = testContext({ type: "telegram", id: "telegram", secrets: { botToken: TOKEN }, config: {} as { botUsername?: string } })
  const running = await runtime.start(ctx)
  await settle()
  const bot = tg.bots.at(-1)!
  return { tg, ctx, running, fetched, feed: (u: Update) => bot.handleUpdate(u) }
}

const tgSends: unknown[] = []
for (const s of TG_SENDS) {
  const r = await tgStarted(s.fail)
  const before = r.tg.calls.length
  let result: unknown
  try {
    result = { ok: json(await r.running.channel!.send("42", s.payload as never)) }
  } catch (err) {
    result = { error: (err as Error).message }
  }
  tgSends.push({ name: s.name, fail: s.fail ?? null, payload: s.payload, calls: callsOf(r.tg.calls.slice(before)), result })
  await r.running.stop()
}

const OWNER = { id: 42, is_bot: false, first_name: "Zaki", last_name: "M" }
const STRANGER = { id: 77, is_bot: false, first_name: "Orang" }
const GROUP = { id: -100777, type: "group", title: "Kebun" }
const SUPER = { id: -100888, type: "supergroup", title: "Kebun Besar" }
let uid = 1
const msgUpdate = (message: Record<string, unknown>, from: Record<string, unknown> | null = OWNER): Record<string, unknown> => ({
  update_id: uid++,
  message: { message_id: 500 + uid, date: 1_700_000_000, chat: { id: (from?.id as number) ?? 1, type: "private", first_name: from?.first_name }, ...(from ? { from } : {}), ...message },
})
const inGroup = (m: Record<string, unknown>, from: Record<string, unknown> = STRANGER, chat = GROUP) => msgUpdate({ chat, ...m }, from)

const TG_STEPS: ({ claim: number } | { name: string; update: Record<string, unknown> })[] = [
  { name: "stranger before claim", update: msgUpdate({ text: "halo" }, STRANGER) },
  { name: "start without nonce", update: msgUpdate({ text: "/start" }) },
  { name: "start with a bad nonce", update: msgUpdate({ text: "/start nope-nonce", entities: [{ type: "bot_command", offset: 0, length: 6 }] }) },
  { name: "start addressed with two words", update: msgUpdate({ text: "/start@palui_test_bot a b" }) },
  { claim: 42 },
  { name: "text with reply", update: msgUpdate({ message_id: 900, text: "ingetin aku besok", reply_to_message: { message_id: 812, date: 0, chat: { id: 42, type: "private" } } }) },
  { name: "owner without last name", update: msgUpdate({ text: "tes" }, { id: 42, is_bot: false, first_name: "Zaki" }) },
  { name: "username only", update: msgUpdate({ text: "tes" }, { id: 43, is_bot: false, first_name: "", username: "orang_aja" }) },
  { name: "bot sender dropped", update: msgUpdate({ text: "beep" }, { id: 5, is_bot: true, first_name: "Bot" }) },
  { name: "no from dropped", update: msgUpdate({ text: "anon" }, null) },
  { name: "group plain", update: inGroup({ text: "ngobrol biasa" }) },
  { name: "group mention", update: inGroup({ text: "@palui_test_bot halo", entities: [{ type: "mention", offset: 0, length: 15 }] }) },
  { name: "group mention other bot", update: inGroup({ text: "@other_bot halo", entities: [{ type: "mention", offset: 0, length: 10 }] }) },
  { name: "group mention mixed case", update: inGroup({ text: "hai @Palui_Test_Bot", entities: [{ type: "mention", offset: 4, length: 15 }] }) },
  { name: "group mention after emoji", update: inGroup({ text: "😀😀 @palui_test_bot", entities: [{ type: "mention", offset: 5, length: 15 }] }) },
  { name: "group reply to bot", update: inGroup({ text: "lanjut", reply_to_message: { message_id: 5, date: 0, chat: GROUP, from: { id: 123456789, is_bot: true, first_name: "Palui" } } }) },
  { name: "group command", update: inGroup({ text: "/new@palui_test_bot", entities: [{ type: "bot_command", offset: 0, length: 19 }] }) },
  { name: "group text mention", update: inGroup({ text: "Palui tolong", entities: [{ type: "text_mention", offset: 0, length: 5, user: { id: 123456789, is_bot: true, first_name: "Palui" } }] }) },
  { name: "group caption entities", update: inGroup({ caption: "lihat @palui_test_bot", caption_entities: [{ type: "mention", offset: 6, length: 15 }], photo: [{ file_id: "g1", file_unique_id: "g", width: 1, height: 1 }] }) },
  { name: "group entities win over caption entities", update: inGroup({ text: "x", entities: [], caption_entities: [{ type: "mention", offset: 0, length: 1 }] }) },
  { name: "supergroup from owner", update: inGroup({ text: "di supergroup" }, OWNER, SUPER) },
  { name: "photo largest", update: msgUpdate({ caption: "ini apa?", photo: [{ file_id: "small", file_unique_id: "s", width: 90, height: 90 }, { file_id: "big", file_unique_id: "b", width: 1280, height: 960 }, { file_id: "mid", file_unique_id: "m", width: 320, height: 240 }, { file_id: "tie", file_unique_id: "t", width: 960, height: 1280 }] }) },
  { name: "voice", update: msgUpdate({ voice: { file_id: "v1", file_unique_id: "v", duration: 7, mime_type: "audio/ogg" } }) },
  { name: "voice without mime", update: msgUpdate({ voice: { file_id: "v2", file_unique_id: "v", duration: 3 } }) },
  { name: "small document", update: msgUpdate({ document: { file_id: "d1", file_unique_id: "d", file_name: "catatan.txt", mime_type: "text/plain", file_size: 10 } }) },
  { name: "big document by size", update: msgUpdate({ caption: "baca ini", document: { file_id: "d2", file_unique_id: "e", file_name: "big.pdf", file_size: 5 * 1024 * 1024 } }) },
  { name: "document without name or size, too big when read", update: msgUpdate({ document: { file_id: "huge1", file_unique_id: "h" } }) },
  { name: "document size 1.25 MB", update: msgUpdate({ document: { file_id: "d3", file_unique_id: "f", file_name: "a.zip", file_size: 1310720 } }) },
  { name: "stranger photo", update: msgUpdate({ caption: "spam", photo: [{ file_id: "x", file_unique_id: "x", width: 1, height: 1 }] }, STRANGER) },
  { name: "stranger photo without caption", update: msgUpdate({ photo: [{ file_id: "x2", file_unique_id: "x", width: 1, height: 1 }] }, STRANGER) },
  { name: "sticker only dropped", update: msgUpdate({ sticker: { file_id: "st", file_unique_id: "st", width: 1, height: 1, is_animated: false, is_video: false, type: "regular" } }) },
  { name: "start again from stranger", update: msgUpdate({ text: "/start abc" }, STRANGER) },
  {
    name: "callback",
    update: { update_id: uid++, callback_query: { id: "cb-1", from: OWNER, chat_instance: "ci", data: "rem:done:r1", message: { message_id: 300, date: 0, chat: { id: 42, type: "private", first_name: "Zaki" } } } },
  },
  {
    name: "callback in a group",
    update: { update_id: uid++, callback_query: { id: "cb-2", from: STRANGER, chat_instance: "ci", data: "approve:a1", message: { message_id: 301, date: 0, chat: GROUP } } },
  },
  { name: "callback without message", update: { update_id: uid++, callback_query: { id: "cb-3", from: OWNER, chat_instance: "ci", data: "deny:a1" } } },
  { name: "callback without data", update: { update_id: uid++, callback_query: { id: "cb-4", from: OWNER, chat_instance: "ci", game_short_name: "g" } } },
]

const tgInbound: unknown[] = []
{
  const r = await tgStarted()
  for (const step of TG_STEPS) {
    if ("claim" in step) {
      const { nonce } = createClaim(r.ctx.db, "telegram")
      claimOwner(r.ctx.db, { channel: "telegram", nonce, externalId: String(step.claim), displayName: "Owner" })
      tgInbound.push({ claim: step.claim })
      continue
    }
    const inBefore = r.ctx.inbound.length
    const callsBefore = r.tg.calls.length
    const fetchedBefore = r.fetched.length
    await r.feed(step.update as unknown as Update)
    await settle()
    const inbound = json(r.ctx.inbound.slice(inBefore)) as Record<string, unknown>[]
    for (const m of inbound) if (m.callback) m.at = 0
    tgInbound.push({ name: step.name, update: step.update, inbound, calls: callsOf(r.tg.calls.slice(callsBefore)), fetched: r.fetched.slice(fetchedBefore) })
  }
  await r.running.stop()
}

// --- whatsapp ------------------------------------------------------------------------

const OWNER_JID = "628123456789@s.whatsapp.net"
const OWNER_LID = "555000111@lid"
const SELF = "628980001472:3@s.whatsapp.net"
const SELF_LID = "111222333:3@lid"
const dm = (id: string, message: Record<string, unknown>, key: Record<string, unknown> = {}, extra: Record<string, unknown> = {}) => ({
  key: { remoteJid: OWNER_JID, fromMe: false, id, ...key },
  message,
  messageTimestamp: 1_700_000_000,
  pushName: "Zaki",
  ...extra,
})
const G = { remoteJid: "120363@g.us", participant: "62811@s.whatsapp.net" }

type WaStep = { lids: Record<string, string> } | { name: string; msg: Record<string, unknown>; type?: string }
const WA_STEPS: WaStep[] = [
  { name: "phone DM, LID unknown", msg: dm("IN1", { conversation: "pesan IN1" }) },
  { lids: { [OWNER_LID]: OWNER_JID } },
  { name: "phone DM with LID from the mapping", msg: dm("IN2", { conversation: "pesan IN2" }) },
  { name: "LID DM through remoteJidAlt", msg: dm("IN3", { conversation: "lewat lid" }, { remoteJid: OWNER_LID, remoteJidAlt: OWNER_JID }) },
  { lids: { [OWNER_LID]: "628123456789:5@s.whatsapp.net" } },
  { name: "LID DM through the mapping", msg: dm("IN4", { conversation: "lid map" }, { remoteJid: OWNER_LID }) },
  { name: "LID DM alt with device suffix", msg: dm("IN5", { conversation: "alt device" }, { remoteJid: OWNER_LID, remoteJidAlt: "628123456789:9@s.whatsapp.net" }) },
  { name: "fromMe dropped", msg: dm("A", { conversation: "x" }, { fromMe: true }) },
  { name: "status dropped", msg: dm("C", { conversation: "x" }, { remoteJid: "status@broadcast" }) },
  { name: "newsletter dropped", msg: dm("D", { conversation: "x" }, { remoteJid: "1203630@newsletter" }) },
  { name: "broadcast list dropped", msg: dm("D2", { conversation: "x" }, { remoteJid: "123@broadcast" }) },
  { name: "other server dropped", msg: dm("D3", { conversation: "x" }, { remoteJid: "123@c.us" }) },
  { name: "append dropped", msg: dm("E", { conversation: "x" }), type: "append" },
  { name: "empty message dropped", msg: dm("E2", {}) },
  { name: "no pushName", msg: dm("E3", { conversation: "tanpa nama" }, {}, { pushName: undefined }) },
  { name: "group plain", msg: dm("G1", { conversation: "ngobrol biasa" }, G, { pushName: "Budi" }) },
  { name: "group mention by phone", msg: dm("G2", { extendedTextMessage: { text: "@bot halo", contextInfo: { mentionedJid: [SELF] } } }, G, { pushName: "Budi" }) },
  { name: "group mention by LID", msg: dm("G3", { extendedTextMessage: { text: "@bot", contextInfo: { mentionedJid: ["111222333@lid"] } } }, G) },
  { name: "group reply to us", msg: dm("G4", { extendedTextMessage: { text: "lanjut", contextInfo: { stanzaId: "BOT9", participant: SELF, quotedMessage: { conversation: "tadi" } } } }, G) },
  { name: "group reply to someone else", msg: dm("G5", { extendedTextMessage: { text: "setuju", contextInfo: { stanzaId: "X1", participant: "62899@s.whatsapp.net", quotedMessage: { conversation: "ayo" } } } }, G) },
  { name: "group reply to the sender", msg: dm("G6", { extendedTextMessage: { text: "ralat", contextInfo: { stanzaId: "X2", participant: "62811:4@s.whatsapp.net", quotedMessage: { conversation: "salah" } } } }, G) },
  { name: "group mention in image caption", msg: dm("G7", { imageMessage: { caption: "lihat", contextInfo: { mentionedJid: [SELF] } } }, G) },
  { name: "group without participant dropped", msg: dm("G8", { conversation: "x" }, { remoteJid: "120363@g.us" }) },
  { name: "group participant alt", msg: dm("G9", { conversation: "alt" }, { remoteJid: "120363@g.us", participant: "777@lid", participantAlt: "62877:2@s.whatsapp.net" }) },
  { name: "extended text with quoted id", msg: dm("Q1", { extendedTextMessage: { text: "yang ini", contextInfo: { stanzaId: "OLD1" } } }) },
  { name: "quote from self", msg: dm("Q2", { extendedTextMessage: { text: "Ini?", contextInfo: { stanzaId: "BOT1", participant: "628980001472@s.whatsapp.net", quotedMessage: { conversation: "Jadwal besok jam 9" } } } }) },
  { name: "quote of an image by the sender", msg: dm("Q3", { extendedTextMessage: { text: "maksudnya", contextInfo: { stanzaId: "OWN1", participant: OWNER_LID, quotedMessage: { imageMessage: { caption: "struk" } } } } }) },
  { name: "quote without body", msg: dm("Q4", { extendedTextMessage: { text: "hilang?", contextInfo: { stanzaId: "GONE1" } } }) },
  { name: "quote of a voice note", msg: dm("Q5", { extendedTextMessage: { text: "dengar", contextInfo: { stanzaId: "V1", quotedMessage: { audioMessage: { ptt: true } } } } }) },
  { name: "quote of audio", msg: dm("Q6", { extendedTextMessage: { text: "lagu", contextInfo: { stanzaId: "V2", quotedMessage: { audioMessage: {} } } } }) },
  { name: "quote of a document", msg: dm("Q7", { imageMessage: { caption: "lihat", contextInfo: { stanzaId: "Q1", quotedMessage: { documentMessage: { fileName: "a.pdf" } } } } }) },
  { name: "quote nested", msg: dm("Q8", { extendedTextMessage: { text: "x", contextInfo: { stanzaId: "Q3", quotedMessage: { extendedTextMessage: { text: " lapis dua ", contextInfo: { stanzaId: "Q0", quotedMessage: { conversation: "lapis satu" } } } } } } }) },
  { name: "quote wrapped in ephemeral", msg: dm("Q9", { extendedTextMessage: { text: "x", contextInfo: { stanzaId: "Q4", quotedMessage: { ephemeralMessage: { message: { conversation: "sementara" } } } } } }) },
  { name: "quote of a sticker, location, contact, poll", msg: dm("Q10", { extendedTextMessage: { text: "s", contextInfo: { stanzaId: "S", quotedMessage: { stickerMessage: {} } } } }) },
  { name: "quote of a location", msg: dm("Q11", { extendedTextMessage: { text: "l", contextInfo: { stanzaId: "L", quotedMessage: { locationMessage: { degreesLatitude: 1 } } } } }) },
  { name: "quote of a contact", msg: dm("Q12", { extendedTextMessage: { text: "c", contextInfo: { stanzaId: "C", quotedMessage: { contactMessage: { displayName: "Budi" } } } } }) },
  { name: "quote of contacts", msg: dm("Q13", { extendedTextMessage: { text: "cs", contextInfo: { stanzaId: "CS", quotedMessage: { contactsArrayMessage: { displayName: "x" } } } } }) },
  { name: "quote of a poll", msg: dm("Q14", { extendedTextMessage: { text: "p", contextInfo: { stanzaId: "P", quotedMessage: { pollCreationMessageV3: { name: "pilih" } } } } }) },
  { name: "quote of a video", msg: dm("Q15", { extendedTextMessage: { text: "v", contextInfo: { stanzaId: "VID", participant: "6200@s.whatsapp.net", quotedMessage: { videoMessage: { caption: " klip " } } } } }) },
  { name: "ephemeral text", msg: dm("W1", { ephemeralMessage: { message: { conversation: "hilang nanti" } } }) },
  { name: "view once image", msg: dm("W2", { viewOnceMessageV2: { message: { imageMessage: { caption: "sekali lihat", mimetype: "image/png" } } } }) },
  { name: "view once v1", msg: dm("W3", { viewOnceMessage: { message: { imageMessage: { caption: "v1" } } } }) },
  { name: "view once extension audio", msg: dm("W4", { viewOnceMessageV2Extension: { message: { audioMessage: { ptt: true, seconds: 3 } } } }) },
  { name: "document with caption", msg: dm("W5", { documentWithCaptionMessage: { message: { documentMessage: { caption: "laporan", fileName: "r.pdf", mimetype: "application/pdf", fileLength: 2048 } } } }) },
  { name: "edited text", msg: dm("W6", { editedMessage: { message: { conversation: "versi baru" } } }) },
  { name: "ephemeral wrapping view once", msg: dm("W7", { ephemeralMessage: { message: { viewOnceMessageV2: { message: { imageMessage: { caption: "lapis" } } } } } }) },
  { name: "owner image", msg: dm("IMG1", { imageMessage: { caption: "ini apa", mimetype: "image/jpeg" } }) },
  { name: "owner image without mimetype", msg: dm("IMG3", { imageMessage: {} }) },
  { name: "owner voice note", msg: dm("VN1", { audioMessage: { mimetype: "audio/ogg; codecs=opus", seconds: 4, ptt: true } }) },
  { name: "owner audio without seconds", msg: dm("AU1", { audioMessage: {} }) },
  { name: "owner document", msg: dm("DOC0", { documentMessage: { fileName: "catatan.txt", fileLength: 10, mimetype: "text/plain" } }) },
  { name: "owner document without name", msg: dm("DOC2", { documentMessage: {} }) },
  { name: "document over 1 MB by size", msg: dm("DOC1", { documentMessage: { fileName: "big.pdf", fileLength: 5 * 1024 * 1024, mimetype: "application/pdf" } }) },
  { name: "document over 1 MB when read", msg: dm("BIGDOC", { documentMessage: { fileName: "liar.pdf", fileLength: 100 } }) },
  { name: "image over 16 MB by size", msg: dm("IMG4", { imageMessage: { caption: "besar", fileLength: 17 * 1024 * 1024 } }) },
  { name: "image over 16 MB when read", msg: dm("BIGIMG", { imageMessage: {} }) },
  { name: "video is caption only", msg: dm("VID1", { videoMessage: { caption: "klip", mimetype: "video/mp4" } }) },
  { name: "video without caption dropped", msg: dm("VID2", { videoMessage: { mimetype: "video/mp4" } }) },
  { name: "sticker dropped", msg: dm("ST1", { stickerMessage: {} }) },
  { name: "stranger image", msg: dm("IMG2", { imageMessage: { caption: "spam" } }, { remoteJid: "629999@s.whatsapp.net" }) },
  { name: "stranger image without caption dropped", msg: dm("IMG5", { imageMessage: {} }, { remoteJid: "629999@s.whatsapp.net" }) },
  { name: "owner by LID image", msg: dm("IMG6", { imageMessage: { caption: "lid" } }, { remoteJid: OWNER_LID, remoteJidAlt: OWNER_JID }) },
  { name: "no timestamp keeps the message", msg: dm("TS0", { conversation: "jam?" }, {}, { messageTimestamp: 0 }) },
  { name: "unicode text", msg: dm("U1", { conversation: "😀 日本語 مرحبا\n**tebal**" }) },
]

async function waRuntime() {
  const dataDir = mkdtempSync(path.join(tmpdir(), "palui-wa-gold-"))
  const sockets: FakeSocket[] = []
  const downloads: string[] = []
  const runtime = createWhatsAppRuntime({
    createSocket: async () => {
      const s = new FakeSocket({ registered: true })
      sockets.push(s)
      return s
    },
    downloadMedia: async (m) => {
      const id = m.key.id ?? ""
      downloads.push(id)
      if (id === "BIGDOC") return Buffer.alloc(1024 * 1024 + 1)
      if (id === "BIGIMG") return Buffer.alloc(16 * 1024 * 1024 + 1)
      return Buffer.from(`media:${id}`)
    },
  })
  const ctx = testContext({ type: "whatsapp", id: "whatsapp", config: { ownerPhone: "0812-3456-789" }, dataDir })
  const running = await runtime.start(ctx)
  const sock = sockets.at(-1)!
  sock.open(SELF)
  return { ctx, running, sock, downloads, dataDir }
}

const waInbound: unknown[] = []
{
  const r = await waRuntime()
  for (const step of WA_STEPS) {
    if ("lids" in step) {
      for (const [k, v] of Object.entries(step.lids)) r.sock.lids.set(k, v)
      waInbound.push({ lids: step.lids })
      continue
    }
    const before = r.ctx.inbound.length
    const dlBefore = r.downloads.length
    r.sock.message(json(step.msg) as WAMessage, step.type ?? "notify")
    await settle()
    waInbound.push({ name: step.name, msg: json(step.msg), type: step.type ?? "notify", inbound: json(r.ctx.inbound.slice(before)), downloads: r.downloads.slice(dlBefore) })
  }
  await r.running.stop()
  rmSync(r.dataDir, { recursive: true, force: true })
}

const WA_SENDS: { name: string; payload: Record<string, unknown> }[] = [
  { name: "markdown with hint quoting a known message", payload: { text: "**Minum obat** sekarang", replyToChannelMsgId: "IN1", actions: [{ id: "rem:done:r1", label: "Done" }, { id: "rem:snooze:r1:1h", label: "1 jam" }, { id: "rem:snooze:r1:tomorrow", label: "Besok" }] } },
  { name: "unknown quote", payload: { text: "halo", replyToChannelMsgId: "NOPE", format: "plain" } },
  { name: "plain keeps markdown", payload: { text: "**tetap** _sama_", format: "plain" } },
  { name: "empty text", payload: { text: "" } },
  { name: "hint only", payload: { text: "", actions: [{ id: "approve:a1", label: "Ya" }] } },
  { name: "long text split, quote on the first chunk", payload: { text: "Kalimat yang lumayan panjang buat ngetes pemotongan. ".repeat(200), replyToChannelMsgId: "IN1" } },
  { name: "quote our own sent message", payload: { text: "lanjutan", replyToChannelMsgId: "OUT1" } },
  { name: "markdown everything", payload: { text: HAND_MD[HAND_MD.length - 1] } },
]

const waSends: unknown[] = []
{
  const r = await waRuntime()
  r.sock.message(json(dm("IN1", { conversation: "pesan IN1" })) as WAMessage)
  await settle()
  for (const s of WA_SENDS) {
    const before = r.sock.sent.length
    const res = await r.running.channel!.send(OWNER_JID, s.payload as never)
    const sent = r.sock.sent.slice(before).map((x) => ({ jid: x.jid, content: json(x.content), quoted: x.options?.quoted?.key.id ?? null }))
    waSends.push({ name: s.name, payload: s.payload, sent, result: json(res) })
  }
  await r.running.stop()
  rmSync(r.dataDir, { recursive: true, force: true })
}

const out = { version: PALUI_VERSION, split, telegram, whatsapp, render, actions, manifest, tgSends, tgInbound, waInbound, waSends }
const file = path.resolve(import.meta.dirname, "../channels.json")
writeFileSync(file, `${JSON.stringify(out, null, 1)}\n`)
console.log(`channels.json: ${split.length} splits, ${telegram.length} markdown, ${tgSends.length} tg sends, ${tgInbound.length} tg steps, ${waInbound.length} wa steps, ${waSends.length} wa sends`)
process.exit(0)

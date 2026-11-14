// Exports M2 model goldens from the real TS code and AI SDK providers into
// apps/server/testdata/models:
//   messages.json  ModelMessage arrays exactly as JSON.stringify writes them to
//                  messages.content / pending_approvals.messages (hand-built
//                  shapes plus a real streamText run with an approval)
//   wire.json      the HTTP request the providers send (URL, headers, body) for
//                  ModelMessages, tools and options, through streamText and the
//                  real failoverModel (effort and cache marks applied per link)
//   stream.json    SSE fixtures fed to each provider's doStream: the stream
//                  parts it yields, or the APICallError it throws
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/models.ts
import { mkdirSync, writeFileSync } from "node:fs"
import { createRequire } from "node:module"
import path from "node:path"
import { cacheMark } from "../../../../packages/core/src/models/cache.ts"
import { failoverModel, isFailoverError } from "../../../../packages/core/src/models/failover.ts"

const coreRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/core/package.json"))
const connRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/connectors/package.json"))
const ai = coreRequire("ai") as typeof import("ai")
const aiTest = coreRequire("ai/test") as typeof import("ai/test")
const { createAnthropic } = connRequire("@ai-sdk/anthropic") as typeof import("@ai-sdk/anthropic")
const { createOpenAICompatible } = connRequire("@ai-sdk/openai-compatible") as typeof import("@ai-sdk/openai-compatible")

const out = path.resolve(import.meta.dirname, "../models")
mkdirSync(out, { recursive: true })
const write = (name: string, value: unknown) => {
  writeFileSync(path.join(out, name), `${JSON.stringify(value, null, 1)}\n`)
  console.log(`wrote testdata/models/${name}`)
}

// A 1x1 PNG and a tiny PDF, base64.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
const JPEG = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQH/wAALCAABAAEBAREA/8QAFAABAAAAAAAAAAAAAAAAAAAACv/EABQQAQAAAAAAAAAAAAAAAAAAAAD/2gAIAQEAAD8AN//Z"
const PDF = Buffer.from("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF").toString("base64")

// --- messages.json ---------------------------------------------------------------------

type Msg = import("ai").ModelMessage
const handBuilt: Record<string, Msg[]> = {
  textOnly: [
    { role: "user", content: "halo" },
    { role: "assistant", content: "hai, ada apa?" },
  ],
  systemBlocks: [
    { role: "system", content: "stable", providerOptions: cacheMark("1h") },
    { role: "system", content: "summary" },
  ] as Msg[],
  userParts: [
    {
      role: "user",
      content: [
        { type: "text", text: "lihat ini", providerOptions: cacheMark("1h") },
        { type: "image", image: PNG, mediaType: "image/png" },
        { type: "file", data: PDF, mediaType: "application/pdf", filename: "ktp.pdf" },
        { type: "text", text: "[Palui context]\nNow: 14.05" },
      ],
    },
  ],
  toolRound: [
    { role: "assistant", content: [{ type: "text", text: "Aku cek dulu." }, { type: "tool-call", toolCallId: "call_1", toolName: "notes__list", input: { q: "listrik", limit: 3 } }, { type: "tool-call", toolCallId: "call_2", toolName: "web__fetch", input: { url: "https://example.com/?a=1&b=<2>" } }] },
    {
      role: "tool",
      content: [
        { type: "tool-result", toolCallId: "call_1", toolName: "notes__list", output: { type: "json", value: [{ id: "n1", title: "bayar listrik", done: false, n: 1.5 }] } },
        { type: "tool-result", toolCallId: "call_2", toolName: "web__fetch", output: { type: "error-text", value: "fetch failed" } },
      ],
    },
  ],
  approvals: [
    { role: "assistant", content: [{ type: "tool-call", toolCallId: "call_3", toolName: "alpha__wipe", input: {} }, { type: "tool-approval-request", approvalId: "aprq_1", toolCallId: "call_3" }] },
    { role: "tool", content: [{ type: "tool-approval-response", approvalId: "aprq_1", approved: false, reason: "owner said no" }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId: "call_3", toolName: "alpha__wipe", output: { type: "execution-denied", reason: "Not run: the owner did not approve it." } }] },
  ],
  reasoning: [
    { role: "assistant", content: [{ type: "reasoning", text: "Thinking about it.", providerOptions: { anthropic: { signature: "sig_abc" } } }, { type: "text", text: "Done ✅ ·   ok" }] },
  ],
  contentOutput: [
    { role: "tool", content: [{ type: "tool-result", toolCallId: "call_4", toolName: "attachments__read", output: { type: "content", value: [{ type: "text", text: "page 1" }, { type: "file", data: { type: "data", data: PNG }, mediaType: "image/png" }] } }] } as unknown as Msg,
  ],
  unicode: [{ role: "user", content: "Ừ, được rồi 😀 \"quoted\" \\ back \n new \t tab </script> & <b>" }],
}

// A real run: one step that calls a tool needing approval, as stored in pending_approvals.messages.
async function realApprovalRun(): Promise<Msg[]> {
  const model = new aiTest.MockLanguageModelV4({
    doStream: async () => ({
      stream: new ReadableStream({
        start(c) {
          for (const ch of [
            { type: "reasoning-start", id: "r" },
            { type: "reasoning-delta", id: "r", delta: "hmm" },
            { type: "reasoning-end", id: "r" },
            { type: "text-start", id: "t" },
            { type: "text-delta", id: "t", delta: "Sebentar." },
            { type: "text-end", id: "t" },
            { type: "tool-call", toolCallId: "call_a", toolName: "notes__add", input: JSON.stringify({ title: "x" }) },
            { type: "tool-call", toolCallId: "call_b", toolName: "notes__list", input: "{}" },
            { type: "finish", finishReason: { unified: "tool-calls", raw: undefined }, usage: { inputTokens: { total: 10, noCache: 10, cacheRead: undefined, cacheWrite: undefined }, outputTokens: { total: 5, text: 5, reasoning: undefined } } },
          ])
            c.enqueue(ch)
          c.close()
        },
      }),
    }),
  })
  const r = ai.streamText({
    model,
    messages: [{ role: "user", content: "catat x" }],
    tools: {
      notes__add: ai.tool({ description: "add", inputSchema: ai.jsonSchema({ type: "object", properties: { title: { type: "string" } } }), execute: async () => "ok" }),
      notes__list: ai.tool({ description: "list", inputSchema: ai.jsonSchema({ type: "object", properties: {} }), execute: async () => ({ items: [] }) }),
    },
    toolApproval: ({ toolCall }) => (toolCall.toolName === "notes__add" ? "user-approval" : undefined),
  } as never)
  for await (const _ of r.fullStream) {
    // drain
  }
  return (await r.responseMessages) as Msg[]
}

// --- wire.json -------------------------------------------------------------------------

type Rec = { url: string; headers: Record<string, string>; body: unknown }
const sseOk = (provider: "anthropic" | "openai") =>
  provider === "anthropic"
    ? [
        'event: message_start\ndata: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}\n\n',
        'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n',
        'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}\n\n',
        'event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n',
        'event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}\n\n',
        'event: message_stop\ndata: {"type":"message_stop"}\n\n',
      ].join("")
    : 'data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'

function recordingFetch(provider: "anthropic" | "openai", sink: Rec[]): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers: Record<string, string> = {}
    new Headers(init?.headers).forEach((v, k) => {
      headers[k] = v
    })
    sink.push({ url: String(input), headers, body: JSON.parse(String(init?.body)) })
    return new Response(sseOk(provider), { status: 200, headers: { "content-type": "text/event-stream" } })
  }) as typeof fetch
}

const BASE = "http://gateway.test:8317"
function models(provider: "anthropic" | "openai", sink: Rec[]) {
  const f = recordingFetch(provider, sink)
  if (provider === "anthropic") return createAnthropic({ baseURL: `${BASE}/v1`, apiKey: "test-key", fetch: f })
  const p = createOpenAICompatible({ name: "cliproxy", baseURL: `${BASE}/v1`, apiKey: "test-key", fetch: f })
  return (id: string) => p.chatModel(id)
}

type ToolIn = { name: string; description: string; inputSchema: Record<string, unknown> }
const TOOLS: ToolIn[] = [
  { name: "notes__list", description: "List notes.", inputSchema: { type: "object", properties: { q: { type: "string", maxLength: 200 }, limit: { type: "integer", minimum: 1 } }, additionalProperties: false, $schema: "http://json-schema.org/draft-07/schema#" } },
  { name: "web__fetch", description: "Fetch a page <b>&</b>.", inputSchema: { type: "object", properties: { url: { type: "string" } }, required: ["url"] } },
  { name: "palui__search_tools", description: "Find tools.", inputSchema: { type: "object", properties: {} } },
]

type WireInput = {
  system?: Msg[]
  messages: Msg[]
  tools?: string[]
  activeTools?: string[]
  toolChoice?: "auto" | "none" | "required" | { type: "tool"; toolName: string }
  maxOutputTokens?: number
  temperature?: number
}

const SYSTEM: Msg[] = [
  { role: "system", content: "Kamu Palui.", providerOptions: cacheMark("1h") },
  { role: "system", content: "Summary so far." },
] as Msg[]

const PROMPTS: Record<string, WireInput> = {
  text: {
    system: SYSTEM,
    messages: [
      { role: "user", content: [{ type: "text", text: "halo" }] },
      { role: "assistant", content: [{ type: "text", text: "hai" }] },
      { role: "user", content: [{ type: "text", text: "jam berapa?", providerOptions: cacheMark("1h") }, { type: "text", text: "[Palui context]\nNow: 14.05" }] },
    ],
  },
  plain: { messages: [{ role: "user", content: "Reply with OK." }], maxOutputTokens: 1 },
  tools: {
    system: SYSTEM,
    tools: ["notes__list", "web__fetch", "palui__search_tools"],
    messages: [
      { role: "user", content: [{ type: "text", text: "cek catatan", providerOptions: cacheMark("1h") }] },
      ...handBuilt.toolRound!,
    ],
  },
  toolsActive: {
    system: SYSTEM,
    tools: ["notes__list", "web__fetch", "palui__search_tools"],
    activeTools: ["web__fetch", "palui__search_tools"],
    messages: [{ role: "user", content: "fetch" }],
  },
  toolsNone: { tools: ["notes__list"], toolChoice: "none", messages: [{ role: "user", content: "wrap up" }, { role: "assistant", content: "" }, { role: "user", content: "ok" }] },
  approvals: { tools: ["notes__list"], messages: [{ role: "user", content: "hapus" }, ...handBuilt.approvals!] },
  media: {
    messages: [
      {
        role: "user",
        content: [
          { type: "text", text: "baca" },
          { type: "image", image: PNG, mediaType: "image/png" },
          { type: "image", image: JPEG },
          { type: "file", data: PDF, mediaType: "application/pdf", filename: "ktp.pdf" },
          { type: "file", data: Buffer.from("halo dunia").toString("base64"), mediaType: "text/plain", filename: "a.txt" },
          { type: "text", text: "" },
        ],
      },
    ],
  },
  reasoningHistory: { messages: [{ role: "user", content: "pikir" }, ...handBuilt.reasoning!, { role: "user", content: "lagi" }] },
  contentOutput: {
    tools: ["notes__list"],
    messages: [
      { role: "user", content: "baca lampiran" },
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "call_4", toolName: "attachments__read", input: { id: "att_1" } }] },
      ...handBuilt.contentOutput!,
    ],
  },
  sampling: { messages: [{ role: "user", content: "x" }], temperature: 1.7, maxOutputTokens: 500 },
}

const MODELS: Record<"anthropic" | "openai", string[]> = {
  anthropic: ["claude-sonnet-4-6", "claude-haiku-4-5-20251001", "claude-opus-5-5", "claude-sonnet-5-5", "gemini-2.5-pro", "claude-next-1"],
  openai: ["claude-sonnet-4-6", "gemini-2.5-pro"],
}
const EFFORTS = [undefined, "none", "minimal", "low", "medium", "high", "xhigh", "max"] as const

async function wireCase(provider: "anthropic" | "openai", modelId: string, effort: (typeof EFFORTS)[number], promptName: string) {
  const input = PROMPTS[promptName]!
  const sink: Rec[] = []
  const lm = models(provider, sink)(modelId) as never
  const fm = failoverModel([{ connectorId: "cliproxy", model: modelId, ...(effort ? { effort } : {}), lm }])
  const tools = Object.fromEntries(
    (input.tools ?? []).map((n) => {
      const t = TOOLS.find((x) => x.name === n)!
      return [n, ai.tool({ description: t.description, inputSchema: ai.jsonSchema(t.inputSchema as never), execute: async () => "ok" })]
    }),
  )
  let error: string | undefined
  try {
    const r = ai.streamText({
      model: fm as never,
      ...(input.system ? { system: input.system as never } : {}),
      messages: input.messages,
      ...(input.tools ? { tools } : {}),
      ...(input.activeTools ? { activeTools: input.activeTools } : {}),
      ...(input.toolChoice ? { toolChoice: input.toolChoice } : {}),
      ...(input.maxOutputTokens !== undefined ? { maxOutputTokens: input.maxOutputTokens } : {}),
      ...(input.temperature !== undefined ? { temperature: input.temperature } : {}),
      maxRetries: 0,
      onError: () => {},
    } as never)
    for await (const p of r.fullStream) if ((p as { type: string }).type === "error") error = String((p as { error: Error }).error?.message ?? p)
  } catch (e) {
    error = (e as Error).message
  }
  return { provider, modelId, effort: effort ?? null, prompt: promptName, request: sink[0] ?? null, ...(error ? { error } : {}) }
}

// --- stream.json -----------------------------------------------------------------------

type Fixture = { name: string; provider: "anthropic" | "openai"; status: number; contentType: string; body: string }
const A = (type: string, data: Record<string, unknown>) => `event: ${type}\ndata: ${JSON.stringify({ type, ...data })}\n\n`
const msgStart = (usage: Record<string, unknown> = { input_tokens: 25, output_tokens: 1 }) => A("message_start", { message: { id: "msg_1", type: "message", role: "assistant", model: "claude-sonnet-4-6", content: [], stop_reason: null, usage } })
const O = (chunk: Record<string, unknown>) => `data: ${JSON.stringify({ id: "c1", object: "chat.completion.chunk", created: 1, model: "m", ...chunk })}\n\n`

const FIXTURES: Fixture[] = [
  {
    name: "anthropic text",
    provider: "anthropic",
    status: 200,
    contentType: "text/event-stream",
    body:
      msgStart() +
      A("ping", {}) +
      A("content_block_start", { index: 0, content_block: { type: "text", text: "" } }) +
      A("content_block_delta", { index: 0, delta: { type: "text_delta", text: "Halo " } }) +
      A("content_block_delta", { index: 0, delta: { type: "text_delta", text: "Zaki 😀" } }) +
      A("content_block_stop", { index: 0 }) +
      A("message_delta", { delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 12 } }) +
      A("message_stop", {}),
  },
  {
    name: "anthropic thinking and tool call",
    provider: "anthropic",
    status: 200,
    contentType: "text/event-stream",
    body:
      msgStart({ input_tokens: 5, cache_creation_input_tokens: 1200, cache_read_input_tokens: 800, output_tokens: 1 }) +
      A("content_block_start", { index: 0, content_block: { type: "thinking", thinking: "" } }) +
      A("content_block_delta", { index: 0, delta: { type: "thinking_delta", thinking: "Let me check." } }) +
      A("content_block_delta", { index: 0, delta: { type: "signature_delta", signature: "sig_1" } }) +
      A("content_block_stop", { index: 0 }) +
      A("content_block_start", { index: 1, content_block: { type: "text", text: "" } }) +
      A("content_block_delta", { index: 1, delta: { type: "text_delta", text: "Aku cek." } }) +
      A("content_block_stop", { index: 1 }) +
      A("content_block_start", { index: 2, content_block: { type: "tool_use", id: "toolu_1", name: "notes__list", input: {} } }) +
      A("content_block_delta", { index: 2, delta: { type: "input_json_delta", partial_json: "" } }) +
      A("content_block_delta", { index: 2, delta: { type: "input_json_delta", partial_json: '{"q": "lis' } }) +
      A("content_block_delta", { index: 2, delta: { type: "input_json_delta", partial_json: 'trik"}' } }) +
      A("content_block_stop", { index: 2 }) +
      A("message_delta", { delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 40 } }) +
      A("message_stop", {}),
  },
  {
    name: "anthropic parallel tool calls",
    provider: "anthropic",
    status: 200,
    contentType: "text/event-stream",
    body:
      msgStart() +
      A("content_block_start", { index: 0, content_block: { type: "tool_use", id: "toolu_a", name: "notes__list", input: {} } }) +
      A("content_block_stop", { index: 0 }) +
      A("content_block_start", { index: 1, content_block: { type: "tool_use", id: "toolu_b", name: "web__fetch", input: {} } }) +
      A("content_block_delta", { index: 1, delta: { type: "input_json_delta", partial_json: '{"url":"https://x.test"}' } }) +
      A("content_block_stop", { index: 1 }) +
      A("message_delta", { delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 30, cache_read_input_tokens: 900 } }) +
      A("message_stop", {}),
  },
  {
    name: "anthropic max tokens",
    provider: "anthropic",
    status: 200,
    contentType: "text/event-stream",
    body:
      msgStart() +
      A("content_block_start", { index: 0, content_block: { type: "text", text: "" } }) +
      A("content_block_delta", { index: 0, delta: { type: "text_delta", text: "Panjang" } }) +
      A("content_block_stop", { index: 0 }) +
      A("message_delta", { delta: { stop_reason: "max_tokens", stop_sequence: null }, usage: { output_tokens: 1 } }) +
      A("message_stop", {}),
  },
  {
    name: "anthropic mid-stream error",
    provider: "anthropic",
    status: 200,
    contentType: "text/event-stream",
    body:
      msgStart() +
      A("content_block_start", { index: 0, content_block: { type: "text", text: "" } }) +
      A("content_block_delta", { index: 0, delta: { type: "text_delta", text: "Sebagian" } }) +
      A("error", { error: { type: "overloaded_error", message: "Overloaded" } }),
  },
  { name: "anthropic first-chunk error", provider: "anthropic", status: 200, contentType: "text/event-stream", body: A("error", { error: { type: "overloaded_error", message: "Overloaded" } }) },
  { name: "anthropic 429", provider: "anthropic", status: 429, contentType: "application/json", body: JSON.stringify({ type: "error", error: { type: "rate_limit_error", message: "Rate limited, slow down" } }) },
  { name: "anthropic 529", provider: "anthropic", status: 529, contentType: "application/json", body: JSON.stringify({ type: "error", error: { type: "overloaded_error", message: "Overloaded" } }) },
  { name: "anthropic 500 not json", provider: "anthropic", status: 500, contentType: "text/plain", body: "upstream exploded" },
  { name: "anthropic 400", provider: "anthropic", status: 400, contentType: "application/json", body: JSON.stringify({ type: "error", error: { type: "invalid_request_error", message: "messages: text content blocks must be non-empty" } }) },
  { name: "anthropic 403 quota", provider: "anthropic", status: 403, contentType: "application/json", body: JSON.stringify({ type: "error", error: { type: "permission_error", message: "Your credit balance is too low" } }) },
  {
    name: "openai text",
    provider: "openai",
    status: 200,
    contentType: "text/event-stream",
    body:
      O({ choices: [{ index: 0, delta: { role: "assistant", content: "" }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { content: "Halo " }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { content: "Zaki" }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 100, completion_tokens: 7, prompt_tokens_details: { cached_tokens: 64 } } }) +
      "data: [DONE]\n\n",
  },
  {
    name: "openai reasoning and tool calls",
    provider: "openai",
    status: 200,
    contentType: "text/event-stream",
    body:
      O({ choices: [{ index: 0, delta: { role: "assistant", reasoning_content: "mikir" }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { content: "Sebentar." }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { tool_calls: [{ index: 0, id: "call_a", type: "function", function: { name: "notes__list", arguments: "" } }] }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { tool_calls: [{ index: 0, function: { arguments: '{"q":' } }] }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { tool_calls: [{ index: 0, function: { arguments: '"x"}' } }] }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: { tool_calls: [{ index: 1, id: "call_b", type: "function", function: { name: "web__fetch", arguments: '{"url":"https://x.test"}' } }] }, finish_reason: null }] }) +
      O({ choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 50, completion_tokens: 20, completion_tokens_details: { reasoning_tokens: 4 } } }) +
      "data: [DONE]\n\n",
  },
  { name: "openai mid-stream error", provider: "openai", status: 200, contentType: "text/event-stream", body: O({ choices: [{ index: 0, delta: { content: "Seb" }, finish_reason: null }] }) + `data: ${JSON.stringify({ error: { message: "upstream died", type: "server_error" } })}\n\n` },
  { name: "openai no finish", provider: "openai", status: 200, contentType: "text/event-stream", body: O({ choices: [{ index: 0, delta: { content: "Seb" }, finish_reason: null }] }) + "data: [DONE]\n\n" },
  { name: "openai 429", provider: "openai", status: 429, contentType: "application/json", body: JSON.stringify({ error: { message: "Too many requests", type: "rate_limit", code: "429" } }) },
  { name: "openai 503", provider: "openai", status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "Service unavailable" } }) },
  { name: "openai 401", provider: "openai", status: 401, contentType: "application/json", body: JSON.stringify({ error: { message: "Invalid API key" } }) },
]

async function runFixture(f: Fixture) {
  const fetchImpl = (async () => new Response(f.body, { status: f.status, headers: { "content-type": f.contentType } })) as typeof fetch
  const lm =
    f.provider === "anthropic"
      ? createAnthropic({ baseURL: `${BASE}/v1`, apiKey: "k", fetch: fetchImpl })("claude-sonnet-4-6")
      : createOpenAICompatible({ name: "cliproxy", baseURL: `${BASE}/v1`, apiKey: "k", fetch: fetchImpl }).chatModel("m")
  try {
    const r = await (lm as unknown as import("../../../../packages/core/src/models/failover.ts").ModelV4).doStream({ prompt: [{ role: "user", content: [{ type: "text", text: "x" }] }] } as never)
    const parts: unknown[] = []
    const reader = r.stream.getReader()
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      const p = value as Record<string, unknown>
      if (["stream-start", "response-metadata", "raw", "custom"].includes(p.type as string)) continue
      if (p.type === "error") {
        const e = p.error as { message?: string; statusCode?: number; isRetryable?: boolean }
        parts.push({ type: "error", message: e?.message ?? String(e) })
        continue
      }
      const { providerMetadata: _pm, ...rest } = p
      if (rest.type === "finish") {
        const u = rest.usage as { inputTokens: Record<string, unknown>; outputTokens: Record<string, unknown> }
        parts.push({ type: "finish", finishReason: rest.finishReason, usage: { inputTokens: u.inputTokens, outputTokens: u.outputTokens } })
        continue
      }
      if (rest.type === "reasoning-delta" && _pm) parts.push({ ...rest, providerMetadata: _pm })
      else parts.push(rest)
    }
    return { ...f, parts }
  } catch (e) {
    const err = e as { name?: string; message: string; statusCode?: number; isRetryable?: boolean; responseBody?: string }
    return { ...f, thrown: { message: err.message, statusCode: err.statusCode ?? null, isRetryable: err.isRetryable ?? false, responseBody: err.responseBody ?? null, failover: isFailoverError(e) } }
  }
}

async function main() {
  const real = await realApprovalRun()
  write("messages.json", [...Object.entries(handBuilt).map(([name, msgs]) => ({ name, json: JSON.stringify(msgs) })), { name: "realApprovalRun", json: JSON.stringify(real) }])

  const wire: unknown[] = []
  for (const provider of ["anthropic", "openai"] as const) {
    for (const modelId of MODELS[provider]) {
      for (const prompt of Object.keys(PROMPTS)) wire.push(await wireCase(provider, modelId, undefined, prompt))
      for (const effort of EFFORTS.slice(1)) wire.push(await wireCase(provider, modelId, effort, "tools"))
      wire.push(await wireCase(provider, modelId, "high", "sampling"))
    }
  }
  write("wire.json", { tools: TOOLS, prompts: PROMPTS, cases: wire })

  const streams: unknown[] = []
  for (const f of FIXTURES) streams.push(await runFixture(f))
  write("stream.json", streams)
}

await main()

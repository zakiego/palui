// Dev only: a stand-in for the runtime's control API, so the dashboard can be
// clicked through and screenshotted against a seeded scratch database without
// real channel tokens. It reports each connector's stored status, and a resumed
// connector goes from connecting to on after a moment (except `recall`, which
// the seed leaves connecting on purpose, to show the busy state).
//   DATA_DIR=/tmp/palui-demo CONTROL_PORT=4895 pnpm tsx scripts/demo-runtime.ts
// Refuses to run when NODE_ENV is production.
import { createServer } from "node:http"
import { config } from "@palui/core/config"
import { listConnectors, setConnectorStatus } from "@palui/core/connectors/store"
import { dbPath, migrations, openDb } from "@palui/db"

if (config().mode === "prod") throw new Error("demo-runtime is for dev databases only")

const db = openDb(dbPath(config().dataDir), { migrate: false })
const startedAt = Date.now()
const schema = migrations.at(-1)?.name ?? "none"

function settle() {
  setTimeout(() => {
    for (const c of listConnectors(db)) {
      if (c.status === "connecting" && c.id !== "recall") setConnectorStatus(db, c.id, "on", null)
    }
  }, 1200)
}

const DEMO_MODELS = [
  "claude-sonnet-5-5",
  "claude-opus-5-5",
  "claude-haiku-5-5",
  "claude-fable-5-2",
  "gpt-5.2",
  "gpt-5.2-codex",
  "o4-mini",
  "gemini-3-pro-preview",
  "gemini-3-flash-preview",
  "llama-4-maverick",
  "deepseek-v3.2",
  "qwen3-coder-480b-a35b-instruct-with-a-long-model-id",
]

function status() {
  return {
    ok: true,
    startedAt,
    schema,
    connectors: listConnectors(db).map((c) => ({ id: c.id, running: c.status === "on", status: c.status, detail: c.statusDetail, live: {} })),
    turnsPaused: { until: null, reason: null },
    activeTurns: [],
  }
}

const server = createServer((req, res) => {
  const url = req.url ?? "/"
  const json = (body: unknown, code = 200) => {
    res.writeHead(code, { "content-type": "application/json" })
    res.end(JSON.stringify(body))
  }
  if (url === "/control/stream") {
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache", connection: "keep-alive" })
    res.write(": demo runtime\n\n")
    const ping = setInterval(() => res.write(": ping\n\n"), 15_000)
    req.on("close", () => clearInterval(ping))
    return
  }
  if (url === "/live") return json({ ok: true, schema })
  if (url === "/control/status") return json(status())
  if (url === "/control/reconcile") {
    settle()
    return json({ ok: true })
  }
  if (url.startsWith("/control/test/")) return json({ ok: true, detail: "Reachable (demo runtime)" })
  if (url.startsWith("/control/action/")) {
    let raw = ""
    req.on("data", (chunk: Buffer) => (raw += chunk))
    req.on("end", () => {
      const name = (JSON.parse(raw || "{}") as { name?: string }).name
      // The settings model picker lists these.
      if (name === "listModels") return json({ models: DEMO_MODELS })
      json({})
    })
    return
  }
  if (url.startsWith("/control/remove/")) return json({ ok: false, detail: "The demo runtime doesn't remove connectors." })
  if (url.startsWith("/control/reminders/parse")) return json({ ok: false, text: "", tz: config().defaultTz, error: "The demo runtime doesn't parse reminders." })
  if (url.startsWith("/control/")) return json({ ok: true })
  json({ error: "not found" }, 404)
})

server.listen(config().controlPort, "127.0.0.1", () => console.log(`demo runtime on http://127.0.0.1:${config().controlPort}`))

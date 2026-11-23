// palui-host-bridge: lets Palui run an allowlist of CLI commands on the Docker
// host, as tools of an MCP server connector. Runs on the host, not in Docker.
//
//   node index.mjs [serve] [--config <path>]   start the server
//   node index.mjs init [--config <path>]      write a starter config with a fresh token
//   node index.mjs check [--config <path>]     validate the config and list the tools
//   node index.mjs status [--config <path>]    is the bridge up? (polls /health for a few seconds)
//
// The config path defaults to ~/.config/palui/host-bridge.json
// (or PALUI_HOST_BRIDGE_CONFIG).
import { randomBytes } from "node:crypto"
import { accessSync, constants, existsSync, mkdirSync, writeFileSync } from "node:fs"
import { dirname, resolve } from "node:path"
import { DEFAULT_PORT, defaultConfigPath, loadConfig, MissingConfigError, type BridgeConfig } from "./config.ts"
import { startBridge } from "./server.ts"

const STARTER = (token: string) => ({
  host: "127.0.0.1",
  port: DEFAULT_PORT,
  token,
  commands: [
    {
      name: "disk_free",
      title: "Free disk space on the host",
      description: "Show free disk space on the host.",
      class: "read",
      command: "/bin/df",
      args: ["-h", "/"],
    },
    {
      name: "uptime",
      title: "Host uptime and load",
      description: "Show how long the host has been up and its load average.",
      class: "read",
      command: "/usr/bin/uptime",
    },
  ],
})

function parseArgs(argv: string[]): { cmd: string; config: string } {
  let cmd = "serve"
  let config = defaultConfigPath()
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!
    if (a === "--config") config = argv[++i] ?? fail("--config needs a path")
    else if (a === "-h" || a === "--help") cmd = "help"
    else if (!a.startsWith("-")) cmd = a
    else fail(`unknown flag ${a}`)
  }
  return { cmd, config }
}

// The launchd label ops/install-host-bridge.sh used; its plist passes it in.
const LABEL = process.env.PALUI_HOST_BRIDGE_LABEL || "palui-host-bridge"
const self = () => `node ${resolve(process.argv[1] ?? "palui-host-bridge.mjs")}`
const restartHint = () => `launchctl kickstart -k gui/${process.getuid?.() ?? "$(id -u)"}/${LABEL}`

function fail(msg: string): never {
  console.error(msg)
  process.exit(1)
}

// Missing or non-executable binaries are reported, not fatal, so one stale
// entry doesn't take every other tool down with it.
function problems(config: BridgeConfig): string[] {
  const out: string[] = []
  for (const c of config.commands) {
    try {
      accessSync(c.command, constants.X_OK)
    } catch {
      out.push(`${c.name}: ${c.command} isn't an executable file`)
    }
    if (c.cwd && !existsSync(c.cwd)) out.push(`${c.name}: cwd ${c.cwd} doesn't exist`)
  }
  return out
}

async function main(): Promise<void> {
  const { cmd, config: path } = parseArgs(process.argv.slice(2))

  if (cmd === "help") {
    console.log("usage: palui-host-bridge [serve|init|check|status] [--config <path>]")
    return
  }

  if (cmd === "init") {
    if (existsSync(path)) fail(`${path} already exists, not touching it`)
    mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
    const token = randomBytes(32).toString("base64url")
    writeFileSync(path, `${JSON.stringify(STARTER(token), null, 2)}\n`, { mode: 0o600 })
    console.log(`wrote ${path}`)
    console.log("Paste this token into the Palui MCP connector (Auth: bearer). It's also the \"token\" field in that file:")
    console.log(token)
    return
  }

  let config: BridgeConfig
  try {
    config = loadConfig(path)
  } catch (err) {
    if (err instanceof MissingConfigError) {
      const flag = path === defaultConfigPath() ? "" : ` --config ${path}`
      fail(`${err.message}. Create one with:\n  ${self()} init${flag}`)
    }
    fail((err as Error).message)
  }

  if (cmd === "status") {
    const url = `http://${config.host}:${config.port}`
    for (let i = 0; i < 10; i++) {
      const up = await fetch(`${url}/health`, { signal: AbortSignal.timeout(1000) }).then((r) => r.ok, () => false)
      if (up) {
        console.log(`running on ${url}/mcp with ${config.commands.length} commands`)
        return
      }
      await new Promise((r) => setTimeout(r, 500))
    }
    fail(`not running on ${url}. See the log, ${process.env.PALUI_HOST_DIR || "~/.palui"}/palui-host-bridge.log when installed with launchd.`)
  }
  const issues = problems(config)

  const danger = config.dangerouslyAllowAnyCommand
  const dangerLine = danger
    ? `DANGER: dangerouslyAllowAnyCommand is on. The "shell" tool runs any command as this user through ${danger.shell} -lc.`
    : null

  if (cmd === "check") {
    if (dangerLine) console.error(dangerLine)
    for (const c of config.commands) {
      const params = Object.keys(c.params)
      console.log(`${c.name} [${c.class}] ${c.command} ${c.args.join(" ")}${params.length ? `  params: ${params.join(", ")}` : ""}`)
    }
    for (const p of issues) console.error(`warning: ${p}`)
    if (issues.length) fail(`fix the warnings above, then restart the bridge:\n  ${restartHint()}`)
    console.log(`config OK: ${config.commands.length} commands${danger ? " plus shell" : ""} on ${config.host}:${config.port}`)
    console.log(`Restart the bridge to apply changes:\n  ${restartHint()}`)
    console.log("Then in Palui: open the connector, press Reload in the tool table, check the classes and Save tool settings.")
    return
  }

  if (cmd !== "serve") fail(`unknown command ${cmd}`)
  for (const p of issues) console.error(`warning: ${p}`)
  if (dangerLine) console.error(dangerLine)
  const bridge = await startBridge(config)
  console.log(`palui-host-bridge on ${bridge.url} with ${config.commands.length} commands (config ${path})`)
  const stop = () => {
    void bridge.close().then(() => process.exit(0))
  }
  process.on("SIGTERM", stop)
  process.on("SIGINT", stop)
}

void main().catch((err) => fail((err as Error).stack ?? String(err)))

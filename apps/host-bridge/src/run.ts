// Runs one allowlisted command: no shell, a hard timeout that kills the whole
// process group, and stdout/stderr capped so a chatty command can't flood the turn.
import { spawn } from "node:child_process"
import type { CommandSpec } from "./config.ts"

export type RunResult = {
  exitCode: number | null
  signal: string | null
  timedOut: boolean
  stdout: string
  stderr: string
  truncated: boolean
  durationMs: number
}

export function runCommand(spec: CommandSpec, argv: string[], now: () => number = Date.now): Promise<RunResult> {
  const started = now()
  return new Promise((resolve) => {
    let child: ReturnType<typeof spawn>
    try {
      child = spawn(spec.command, argv, {
        cwd: spec.cwd,
        env: { ...process.env, ...spec.env },
        stdio: ["ignore", "pipe", "pipe"],
        // Its own process group, so a timeout also kills anything it spawned.
        detached: true,
        shell: false,
      })
    } catch (err) {
      resolve({ exitCode: null, signal: null, timedOut: false, stdout: "", stderr: (err as Error).message, truncated: false, durationMs: 0 })
      return
    }

    const out = new Capped(spec.maxOutputBytes)
    const err = new Capped(spec.maxOutputBytes)
    child.stdout!.on("data", (b: Buffer) => out.push(b))
    child.stderr!.on("data", (b: Buffer) => err.push(b))

    let timedOut = false
    const timer = setTimeout(() => {
      timedOut = true
      killGroup(child.pid)
    }, spec.timeoutMs)

    let spawnError: Error | undefined
    child.on("error", (e) => {
      spawnError = e
    })
    child.on("close", (code, signal) => {
      clearTimeout(timer)
      const stderr = spawnError ? `${err.text()}${spawnError.message}` : err.text()
      resolve({
        exitCode: code,
        signal,
        timedOut,
        stdout: out.text(),
        stderr,
        truncated: out.truncated || err.truncated,
        durationMs: now() - started,
      })
    })
  })
}

function killGroup(pid: number | undefined): void {
  if (!pid) return
  try {
    process.kill(-pid, "SIGKILL")
  } catch {
    try {
      process.kill(pid, "SIGKILL")
    } catch {
      // already gone
    }
  }
}

class Capped {
  private chunks: Buffer[] = []
  private size = 0
  truncated = false
  constructor(private readonly max: number) {}
  push(b: Buffer): void {
    const room = this.max - this.size
    if (room <= 0) {
      this.truncated = true
      return
    }
    const part = b.length > room ? b.subarray(0, room) : b
    if (part !== b) this.truncated = true
    this.chunks.push(part)
    this.size += part.length
  }
  text(): string {
    return Buffer.concat(this.chunks).toString("utf8")
  }
}

// What the model sees: exit status first, then each non-empty stream.
export function formatResult(r: RunResult): string {
  const status = r.timedOut ? "timed out (killed)" : r.exitCode !== null ? `exit ${r.exitCode}` : `killed by ${r.signal ?? "unknown"}`
  const parts = [status]
  if (r.stdout) parts.push(`--- stdout ---\n${r.stdout}`)
  if (r.stderr) parts.push(`--- stderr ---\n${r.stderr}`)
  if (r.truncated) parts.push("(output truncated)")
  return parts.join("\n")
}

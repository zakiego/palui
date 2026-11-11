// Runs one allowlisted command: no shell, a hard timeout that kills the whole
// process group, and stdout/stderr capped so a chatty command can't flood the turn.
import { spawn } from "node:child_process"
import type { CommandSpec } from "./config.ts"

export type RunResult = {
  exitCode: number | null
  signal: string | null
  timedOut: boolean
  // Whether the exit code is in the command's okExitCodes.
  ok: boolean
  stdout: string
  stderr: string
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
      })
    } catch (err) {
      resolve({ exitCode: null, signal: null, timedOut: false, ok: false, stdout: "", stderr: (err as Error).message, durationMs: 0 })
      return
    }

    const out = new HeadTail(spec.maxOutputBytes)
    const err = new HeadTail(spec.maxOutputBytes)
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
      const stderr = spawnError ? `${err.text("stderr")}${spawnError.message}` : err.text("stderr")
      resolve({
        exitCode: code,
        signal,
        timedOut,
        ok: !timedOut && code !== null && spec.okExitCodes.includes(code),
        stdout: out.text("stdout"),
        stderr,
        durationMs: now() - started,
      })
    })
  })
}

// detached: true makes the child its group's leader, so -pid is the whole group.
function killGroup(pid: number | undefined): void {
  if (!pid) return
  try {
    process.kill(-pid, "SIGKILL")
  } catch {
    // already gone
  }
}

// Keeps the first and last max/2 bytes of a stream. Logs and build output put
// the useful part (the error) at the end, so dropping the tail would hide it.
class HeadTail {
  private head: Buffer[] = []
  private headSize = 0
  private tail: Buffer[] = []
  private tailSize = 0
  private total = 0
  private readonly half: number
  constructor(max: number) {
    this.half = Math.floor(max / 2)
  }
  push(b: Buffer): void {
    this.total += b.length
    let rest = b
    if (this.headSize < this.half) {
      const take = rest.subarray(0, this.half - this.headSize)
      this.head.push(take)
      this.headSize += take.length
      rest = rest.subarray(take.length)
    }
    if (!rest.length) return
    this.tail.push(rest)
    this.tailSize += rest.length
    while (this.tailSize > this.half) {
      const first = this.tail[0]!
      const extra = this.tailSize - this.half
      if (first.length <= extra) {
        this.tail.shift()
        this.tailSize -= first.length
      } else {
        this.tail[0] = first.subarray(extra)
        this.tailSize -= extra
      }
    }
  }
  get truncated(): boolean {
    return this.total > this.headSize + this.tailSize
  }
  text(stream: string): string {
    const head = Buffer.concat(this.head).toString("utf8")
    const tail = Buffer.concat(this.tail).toString("utf8")
    if (!this.truncated) return head + tail
    const kept = this.headSize + this.tailSize
    return `${head}\n[... ${stream} cut: showing the first and last ${bytes(kept / 2)} of ${bytes(this.total)} ...]\n${tail}`
  }
}

function bytes(n: number): string {
  if (n < 1024) return `${Math.round(n)} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

// What the model sees: exit status first, then each non-empty stream.
export function formatResult(r: RunResult, timeoutMs: number): string {
  const status = r.timedOut
    ? `timed out after ${seconds(timeoutMs)} (killed)`
    : r.exitCode !== null
      ? `exit ${r.exitCode}`
      : `killed by ${r.signal ?? "unknown"}`
  const parts = [status]
  if (r.stdout) parts.push(`--- stdout ---\n${r.stdout}`)
  if (r.stderr) parts.push(`--- stderr ---\n${r.stderr}`)
  if (!r.stdout && !r.stderr) parts.push("(no output)")
  return parts.join("\n")
}

function seconds(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${Math.round(ms / 100) / 10} s`
}

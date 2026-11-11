// One process per WhatsApp auth dir. Two sockets on the same credentials make
// WhatsApp evict both with 440 conflicts, forever (utusan src/lock.ts).
//
// The lock file is created with O_EXCL and holds the pid plus a random nonce. The
// holder touches the file's mtime every 30 s; a lock whose mtime is older than
// 2 minutes is stale. Pids say nothing across containers that share the volume
// (every container's process is pid 1, and a pid from another pid namespace can't
// be probed with kill(pid, 0)), so the pid is only kept for the error message. The
// container hostname is never used as an identity either.
//
// After a crash the lock stays fresh for up to 2 minutes. The Saklar supervisor
// retries a failed start (5 s, 30 s, 120 s), so the channel comes back on its own.
import { randomBytes } from "node:crypto"
import { closeSync, mkdirSync, openSync, readFileSync, rmSync, statSync, utimesSync, writeSync } from "node:fs"
import { dirname } from "node:path"

export const HEARTBEAT_MS = 30_000
export const STALE_AFTER_MS = 120_000

export type LockInfo = { pid: number; nonce: string; startedAt: number }

export class AuthLockedError extends Error {
  constructor(
    readonly pid: number,
    readonly path: string,
  ) {
    super(`WhatsApp session is in use by another Palui process (pid ${pid}, lock ${path}). If that process is gone, the lock frees itself within 2 minutes.`)
    this.name = "AuthLockedError"
  }
}

// Locks this process holds right now: path -> nonce.
const held = new Map<string, string>()

function readLock(path: string): LockInfo | null {
  try {
    const v = JSON.parse(readFileSync(path, "utf8")) as Partial<LockInfo>
    if (typeof v.pid !== "number" || typeof v.nonce !== "string") return null
    return { pid: v.pid, nonce: v.nonce, startedAt: Number(v.startedAt ?? 0) }
  } catch {
    return null
  }
}

function mtimeOf(path: string): number | null {
  try {
    return statSync(path).mtimeMs
  } catch {
    return null
  }
}

export type LockOptions = {
  pid?: number
  // Clock for the staleness check and startedAt.
  now?: () => number
  heartbeatMs?: number
  staleAfterMs?: number
}

export type AuthLock = { path: string; nonce: string; touch(): void; release(): void }

export function acquireAuthLock(path: string, opts: LockOptions = {}): AuthLock {
  const pid = opts.pid ?? process.pid
  const now = opts.now ?? Date.now
  const heartbeatMs = opts.heartbeatMs ?? HEARTBEAT_MS
  const staleAfterMs = opts.staleAfterMs ?? STALE_AFTER_MS
  mkdirSync(dirname(path), { recursive: true })

  for (let attempt = 0; attempt < 2; attempt++) {
    const nonce = randomBytes(8).toString("hex")
    try {
      const fd = openSync(path, "wx")
      try {
        writeSync(fd, JSON.stringify({ pid, nonce, startedAt: now() } satisfies LockInfo))
      } finally {
        closeSync(fd)
      }
      held.set(path, nonce)
      const ours = () => held.get(path) === nonce && readLock(path)?.nonce === nonce
      const touch = () => {
        if (!ours()) return
        try {
          const t = new Date(now())
          utimesSync(path, t, t)
        } catch {
          // The file went away; the next acquire sorts it out.
        }
      }
      const beat = setInterval(touch, heartbeatMs)
      beat.unref?.()
      return {
        path,
        nonce,
        touch,
        release() {
          clearInterval(beat)
          if (held.get(path) !== nonce) return
          const mine = readLock(path)?.nonce === nonce
          held.delete(path)
          if (mine) rmSync(path, { force: true })
        },
      }
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== "EEXIST") throw err
      const info = readLock(path)
      const mtime = mtimeOf(path)
      const ownedHere = info !== null && held.get(path) === info.nonce
      const fresh = mtime !== null && now() - mtime < staleAfterMs
      if (info && (ownedHere || fresh)) throw new AuthLockedError(info.pid, path)
      // An unreadable file that is still fresh may be a lock mid-write: leave it.
      if (!info && fresh) throw new AuthLockedError(-1, path)
      // Stale: no heartbeat for staleAfterMs. Re-check it's the same file first.
      if (readLock(path)?.nonce === info?.nonce) rmSync(path, { force: true })
    }
  }
  const info = readLock(path)
  throw new AuthLockedError(info?.pid ?? -1, path)
}

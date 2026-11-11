// Consistent, gzipped snapshot of the live database. Uses SQLite's online
// backup API through better-sqlite3, so it's safe while the app is writing.
// The gzip lands in a temp file next to `dest` and is renamed into place, so a
// crash never leaves a half-written snapshot under the real name (it would
// count toward pre-deploy's KEEP=3 and show as the latest backup in Settings).
import fs from "node:fs"
import path from "node:path"
import { pipeline } from "node:stream/promises"
import { createGzip } from "node:zlib"
import Database from "better-sqlite3"

export type BackupResult = { dest: string; bytes: number; dbBytes: number }

export async function backupDb(src: string, dest: string): Promise<BackupResult> {
  fs.mkdirSync(path.dirname(dest), { recursive: true })
  // Neither temp name ends in .db or .gz, so pre-deploy's prune glob and
  // latestBackup() never pick them up.
  const tmp = `${dest}.tmp-${process.pid}`
  const snapFile = `${tmp}-db`
  // Plain connection: no migrations. The backup copies pages as they are.
  const live = new Database(src, { readonly: true, fileMustExist: true })
  try {
    live.pragma("busy_timeout = 5000")
    await live.backup(snapFile)
  } finally {
    live.close()
  }
  try {
    const snap = new Database(snapFile)
    try {
      // The copy inherits WAL mode. Switch it back so the snapshot is one
      // self-contained file; the app turns WAL on again when it opens it.
      snap.pragma("journal_mode = DELETE")
      const check = snap.pragma("quick_check", { simple: true })
      if (check !== "ok") throw new Error(`snapshot failed quick_check: ${String(check)}`)
    } finally {
      snap.close()
    }
    const dbBytes = fs.statSync(snapFile).size
    await pipeline(fs.createReadStream(snapFile), createGzip({ level: 9 }), fs.createWriteStream(tmp))
    fs.renameSync(tmp, dest)
    return { dest, bytes: fs.statSync(dest).size, dbBytes }
  } finally {
    for (const f of [snapFile, `${snapFile}-wal`, `${snapFile}-shm`, `${snapFile}-journal`, tmp]) {
      try {
        fs.rmSync(f, { force: true })
      } catch {
        // best effort; a leftover temp name is never mistaken for a backup
      }
    }
  }
}

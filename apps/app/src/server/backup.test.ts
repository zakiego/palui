import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { openDb } from "@palui/db"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { backupDb } from "./backup"
import { latestBackup } from "./queries"

let dir: string
beforeEach(() => {
  dir = fs.mkdtempSync(path.join(os.tmpdir(), "palui-backup-"))
})
afterEach(() => {
  vi.restoreAllMocks()
  fs.rmSync(dir, { recursive: true, force: true })
})

function liveDb() {
  const file = path.join(dir, "palui.db")
  openDb(file).$client.close()
  return file
}

describe("backupDb", () => {
  it("leaves only the finished snapshot behind", async () => {
    const src = liveDb()
    const dest = path.join(dir, "backups", "pre-deploy-20261110T000000Z.db.gz")
    await backupDb(src, dest)
    expect(fs.readdirSync(path.dirname(dest))).toEqual([path.basename(dest)])
  })

  it("writes the gzip under a temp name and renames it into place", async () => {
    const src = liveDb()
    const dest = path.join(dir, "backups", "pre-deploy-20261110T000000Z.db.gz")
    const writes: string[] = []
    const real = fs.createWriteStream
    vi.spyOn(fs, "createWriteStream").mockImplementation(((p: fs.PathLike, o?: never) => {
      writes.push(String(p))
      return real(p, o)
    }) as typeof fs.createWriteStream)
    await backupDb(src, dest)
    expect(writes).toEqual([`${dest}.tmp-${process.pid}`])
    expect(fs.existsSync(dest)).toBe(true)
  })

  it("never creates dest when writing the gzip fails", async () => {
    const src = liveDb()
    const dest = path.join(dir, "backups", "pre-deploy-20261110T000000Z.db.gz")
    // A directory where the gzip temp file goes makes the write fail.
    fs.mkdirSync(`${dest}.tmp-${process.pid}`, { recursive: true })
    await expect(backupDb(src, dest)).rejects.toThrow()
    expect(fs.existsSync(dest)).toBe(false)
  })
})

describe("latestBackup", () => {
  it("ignores half-written temp files", () => {
    const backups = path.join(dir, "backups")
    fs.mkdirSync(backups)
    const done = path.join(backups, "pre-deploy-20261109T000000Z.db.gz")
    fs.writeFileSync(done, "x")
    fs.utimesSync(done, new Date(1_000_000), new Date(1_000_000))
    fs.writeFileSync(path.join(backups, "pre-deploy-20261110T000000Z.db.gz.tmp-42"), "partial")
    fs.writeFileSync(path.join(backups, "pre-deploy-20261110T000000Z.db.gz.tmp-42-db"), "partial")
    expect(latestBackup(dir)?.file).toBe("pre-deploy-20261109T000000Z.db.gz")
  })
})

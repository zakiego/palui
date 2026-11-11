// Dev helper: create (or update) the cliproxy model connector in a local
// database and switch it on, reading the API key from a file so it never lands
// in shell history or the repo.
//
//   DATA_DIR=./data pnpm --filter @palui/core exec tsx scripts/dev-connect-cliproxy.ts [keyFile] [baseUrl]
//
// Defaults: keyFile ~/.config/palui/cliproxy-key, baseUrl http://127.0.0.1:8317.
// MASTER_KEY must match what the app and runtime use (unset = the dev fallback).
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { dbPath, openDb } from "@palui/db"
import { config, masterKey } from "@palui/core/config"
import { connectConnector, upsertConnector } from "@palui/core/connectors/store"

const keyFile = process.argv[2] ?? path.join(os.homedir(), ".config/palui/cliproxy-key")
const baseUrl = process.argv[3] ?? "http://127.0.0.1:8317"
const apiKey = fs.readFileSync(keyFile, "utf8").trim()
if (!apiKey) throw new Error(`No key in ${keyFile}`)

const db = openDb(dbPath(config().dataDir))
upsertConnector(db, { id: "cliproxy", manifest: { id: "cliproxy", kind: "model", name: "CLIProxyAPI" }, config: { baseUrl }, secrets: { apiKey } }, masterKey())
connectConnector(db, "cliproxy")
console.log(`cliproxy connector saved and switched on (${baseUrl}) in ${dbPath(config().dataDir)}`)
db.$client.close()

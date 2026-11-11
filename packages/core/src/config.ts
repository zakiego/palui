// Process configuration from the environment. Only three keys are secret and come
// from the deploy .env (PLAN 13): MASTER_KEY, BETTER_AUTH_SECRET, APP_URL. Every
// other credential lives encrypted in the database.
import { controlToken } from "@palui/db/crypto"

export type Config = {
  dataDir: string
  masterKey: string
  authSecret: string
  // Public origin of the dashboard (cookies, passkey rpID, invite links).
  appUrl: string
  // Where the app reaches the runtime's control API (compose network only).
  runtimeUrl: string
  // Where the runtime reaches the app (health, nothing else today).
  controlPort: number
  defaultTz: string
  // "prod" refuses nothing; "dev" refuses channel tokens that match PALUI_PROD_TOKEN_HASHES.
  mode: "dev" | "prod"
  prodTokenHashes: string[]
}

let cached: Config | undefined

export function loadConfig(env: Record<string, string | undefined> = process.env): Config {
  return {
    dataDir: env.DATA_DIR || "./data",
    masterKey: env.MASTER_KEY || "",
    authSecret: env.BETTER_AUTH_SECRET || "",
    appUrl: (env.APP_URL || "http://localhost:4890").replace(/\/+$/, ""),
    runtimeUrl: (env.RUNTIME_URL || "http://127.0.0.1:4891").replace(/\/+$/, ""),
    controlPort: Number(env.CONTROL_PORT || 4891),
    defaultTz: env.PALUI_TZ || "Asia/Makassar",
    mode: env.NODE_ENV === "production" ? "prod" : "dev",
    prodTokenHashes: (env.PALUI_PROD_TOKEN_HASHES || "").split(",").map((s) => s.trim()).filter(Boolean),
  }
}

export function config(): Config {
  cached ??= loadConfig()
  return cached
}

// Tests override pieces of the config.
export function setConfig(next: Partial<Config> | undefined) {
  cached = next ? { ...loadConfig(), ...next } : undefined
}

// Derived from masterKey(), so dev (no MASTER_KEY) gets the same token in the app
// and the runtime.
export function getControlToken(cfg: Config = config()): string {
  return controlToken(masterKey(cfg))
}

// A dev fallback so `pnpm dev` works before a MASTER_KEY exists. Never used in prod.
export const DEV_MASTER_KEY = "palui-dev-master-key-not-for-production-use-0000"

export function masterKey(cfg: Config = config()): string {
  if (cfg.masterKey) return cfg.masterKey
  if (cfg.mode === "prod") throw new Error("MASTER_KEY is not set. Add it to .env (openssl rand -base64 32).")
  return DEV_MASTER_KEY
}

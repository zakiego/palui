// The app's handle on the runtime's control API (PLAN 3.1). Every dashboard
// action that changes what's running goes through here.
import { config, masterKey } from "@palui/core/config"
import { CONTROL_HEADER, ControlError, controlClient, type ControlClient } from "@palui/core/control"
import { controlToken } from "@palui/db/crypto"

export const RUNTIME_DOWN = "The runtime isn't reachable. Check docker compose logs runtime."

let override: ControlClient | undefined
let cached: ControlClient | undefined

export function runtime(): ControlClient {
  if (override) return override
  // masterKey() falls back to the dev key when MASTER_KEY is unset outside
  // prod, so `pnpm dev` works before a .env exists (the runtime does the same).
  cached ??= controlClient(config().runtimeUrl, controlToken(masterKey()))
  return cached
}

// GET /live on the runtime. The control client doesn't expose it yet (reported
// for core), so it's fetched directly with the same header and a short timeout.
export async function runtimeLive(fetchImpl: typeof fetch = fetch): Promise<{ ok: boolean; schema: string }> {
  const res = await fetchImpl(`${config().runtimeUrl}/live`, {
    headers: { [CONTROL_HEADER]: controlToken(masterKey()) },
    signal: AbortSignal.timeout(2_000),
  })
  if (!res.ok) throw new ControlError(`runtime answered ${res.status}`, res.status)
  return (await res.json()) as { ok: boolean; schema: string }
}

// Tests inject a fake client.
export function setRuntime(client: ControlClient | undefined) {
  override = client
}

export function isRuntimeDown(err: unknown): boolean {
  return err instanceof ControlError && err.status === 503 && /isn't reachable/.test(err.message)
}

// Turns a control error into one sentence for the UI. A down runtime always
// reads the same, so the dashboard can show one Alert for it.
export function runtimeErrorMessage(err: unknown): string {
  if (isRuntimeDown(err)) return RUNTIME_DOWN
  if (err instanceof Error) return err.message
  return String(err)
}

// Runs a control call and rethrows with a clean message.
export async function callRuntime<T>(fn: (rt: ControlClient) => Promise<T>): Promise<T> {
  try {
    return await fn(runtime())
  } catch (err) {
    throw new Error(runtimeErrorMessage(err))
  }
}

// Pokes the runtime after a desired-state write. The write already happened,
// so a down runtime isn't an error: the supervisor reconciles every 30 s and
// on start anyway.
export async function pokeRuntime(): Promise<{ runtimeReachable: boolean }> {
  try {
    await runtime().reconcile()
    return { runtimeReachable: true }
  } catch {
    return { runtimeReachable: false }
  }
}

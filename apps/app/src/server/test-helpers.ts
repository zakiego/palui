// Shared fakes for server tests.
import { ControlError, type ControlClient } from "@palui/core/control"
import { vi } from "vitest"

export const TEST_MASTER_KEY = "test-master-key-0123456789-abcdefghijklmnop"

// A control client whose every call fails the way a stopped runtime does.
export function downRuntime(): ControlClient {
  const down = () => Promise.reject(new ControlError("The runtime isn't reachable (fetch failed). Check docker compose logs runtime.", 503))
  return new Proxy({} as ControlClient, { get: () => vi.fn(down) })
}

// A control client that answers ok, with spies on every method.
export function upRuntime(overrides: Partial<Record<keyof ControlClient, unknown>> = {}) {
  const rt = {
    status: vi.fn(async () => ({ ok: true, startedAt: 0, schema: "x", connectors: [], turnsPaused: { until: null, reason: null }, activeTurns: [] })),
    reconcile: vi.fn(async () => ({ ok: true as const })),
    test: vi.fn(async () => ({ ok: true, detail: "reachable" })),
    action: vi.fn(async () => ({})),
    remove: vi.fn(async () => ({ ok: true })),
    webSend: vi.fn(async () => ({ ok: true as const, chatId: "chat_1", messageId: "msg_1" })),
    approve: vi.fn(async () => ({ ok: true })),
    runJob: vi.fn(async () => ({ ok: true })),
    stop: vi.fn(async () => ({ ok: true })),
    parseReminder: vi.fn(async () => ({ ok: true, text: "x", tz: "Asia/Makassar" })),
    stream: vi.fn(async () => new Response("")),
    ...overrides,
  }
  return rt as unknown as ControlClient & { [K in keyof typeof rt]: (typeof rt)[K] }
}

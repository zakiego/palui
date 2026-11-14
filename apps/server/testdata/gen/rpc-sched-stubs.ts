// Stand-ins that rpc-sched.ts swaps in (through a module resolve hook) for
// "@tanstack/react-start" and "@/server/session", so the real handler bodies of
// apps/app/src/lib/fns run outside the web server. createServerFn keeps the
// validator step exactly as TanStack Start runs it (execValidator in
// start-client-core: a standard-schema validator's issues become an Error
// whose message is JSON.stringify(issues, undefined, 2)). The session is
// always the signed-in owner on the generator's database.

type State = { db: unknown }

function state(): State {
  const s = (globalThis as { __rpcSched?: State }).__rpcSched
  if (!s) throw new Error("rpc-sched: no database set")
  return s
}

export class UnauthorizedError extends Error {
  constructor() {
    super("UNAUTHORIZED")
  }
}

export class ForbiddenError extends Error {}

export async function requireUser() {
  return { db: state().db, viewer: { sessionId: "ses_stub", user: { id: "usr_stub", username: "owner", name: "Owner" } } }
}

export async function requireWriter() {
  return requireUser()
}

export function requireSameOrigin() {}

// Thrown when the input fails the validator, so the generator can tell a
// validation error from one the handler threw.
export class ValidationFailure extends Error {
  readonly validation = true
}

type StandardValidator = { "~standard": { validate: (v: unknown) => Promise<{ issues?: unknown[]; value?: unknown }> | { issues?: unknown[]; value?: unknown } } }

async function execValidator(validator: unknown, input: unknown): Promise<unknown> {
  if (validator == null) return {}
  if (typeof validator === "object" && "~standard" in (validator as object)) {
    const result = await (validator as StandardValidator)["~standard"].validate(input)
    if (result.issues) throw new ValidationFailure(JSON.stringify(result.issues, undefined, 2))
    return result.value
  }
  throw new Error("Invalid validator type!")
}

export type StubFn = ((opts?: { data?: unknown }) => Promise<unknown>) & { method: "GET" | "POST" }

export function createServerFn(opts: { method: "GET" | "POST" }) {
  let validator: unknown
  const builder = {
    validator(v: unknown) {
      validator = v
      return builder
    },
    handler(fn: (ctx: { data: unknown }) => unknown) {
      const call = (async (o?: { data?: unknown }) => {
        const data = await execValidator(validator, o?.data)
        return fn({ data })
      }) as StubFn
      call.method = opts.method
      return call
    },
  }
  return builder
}

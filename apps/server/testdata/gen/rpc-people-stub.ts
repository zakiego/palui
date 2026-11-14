// Stands in for `@tanstack/react-start` and `apps/app/src/server/session.ts`
// while rpc-people.ts runs the dashboard's server functions outside the web
// server. createServerFn keeps the real handler body and runs the validator
// the way TanStack Start's execValidator does; the session is always the
// owner on the generator's scratch database.

type Validator = { "~standard"?: { validate: (v: unknown) => unknown }; parse?: (v: unknown) => unknown } | ((v: unknown) => unknown)

// A validator refusal, so the generator can tell it from a handler error.
export class ValidationError extends Error {}

// start-client-core createServerFn.js execValidator, verbatim in behavior.
async function execValidator(validator: Validator | undefined, input: unknown): Promise<unknown> {
  if (validator == null) return {}
  if (typeof validator === "object" && "~standard" in validator && validator["~standard"]) {
    const result = (await validator["~standard"].validate(input)) as { issues?: unknown; value?: unknown }
    if (result.issues) throw new ValidationError(JSON.stringify(result.issues, undefined, 2))
    return result.value
  }
  if (typeof validator === "object" && validator.parse) return validator.parse(input)
  if (typeof validator === "function") return validator(input)
  throw new Error("Invalid validator type!")
}

export function createServerFn(opts: { method: "GET" | "POST" }) {
  let validator: Validator | undefined
  const builder = {
    validator(v: Validator) {
      validator = v
      return builder
    },
    handler(fn: (ctx: { data: unknown }) => unknown) {
      const call = async (input?: unknown) => fn({ data: await execValidator(validator, input) })
      return Object.assign(call, { method: opts.method })
    },
  }
  return builder
}

export const scratch: { db: unknown } = { db: undefined }

const viewer = { sessionId: "ses_generator", user: { id: "owner", username: "owner", name: "Owner" } }

export async function requireUser() {
  return { db: scratch.db, viewer }
}

export async function requireWriter() {
  return requireUser()
}

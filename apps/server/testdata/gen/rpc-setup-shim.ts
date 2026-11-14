// Stand-ins for @tanstack/react-start and @/server/session (see
// rpc-setup-loader.mjs). createServerFn keeps the method, the validator and
// the handler, and `call(input)` runs them the way TanStack Start does:
// validator.parse(input), then handler({ data }). requireUser and
// requireWriter return the scratch database and a fixed viewer; the guards
// themselves are tested in Go.
import { getDb } from "../../../app/src/server/db.ts"

export type ServerFn = { method: "GET" | "POST"; hasValidator: boolean; call: (input: unknown) => Promise<unknown> }

type Schema = { parse: (v: unknown) => unknown }

export function createServerFn(opts: { method: "GET" | "POST" }) {
  let validator: Schema | undefined
  const builder = {
    validator(s: Schema) {
      validator = s
      return builder
    },
    handler(fn: (ctx: { data: unknown }) => unknown): ServerFn {
      return {
        method: opts.method,
        hasValidator: !!validator,
        call: async (input: unknown) => fn({ data: validator ? validator.parse(input) : undefined }),
      }
    },
  }
  return builder
}

// The viewer's ids are placeholders; the Go test swaps its real session and
// user ids for the same strings.
export const VIEWER = { sessionId: "<session>", user: { id: "<user>", username: "owner", name: "Owner" } }

export async function requireUser() {
  return { db: getDb(), viewer: VIEWER }
}

export async function requireWriter() {
  return requireUser()
}

export class UnauthorizedError extends Error {}
export class ForbiddenError extends Error {}

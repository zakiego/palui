// Module resolve hook for rpc-setup.ts: the real server function files
// (apps/app/src/lib/fns) import createServerFn from @tanstack/react-start and
// requireUser from @/server/session, which need a running web server. This
// points both at rpc-setup-shim.ts so the real handler bodies and validators
// run in a plain script. tsx may rewrite the "@/" alias before this hook sees
// it, so the session module is also matched by its resolved file URL.
let shim = ""

export function initialize(data) {
  shim = data.shim
}

export async function resolve(specifier, context, next) {
  if (specifier === "@tanstack/react-start" || specifier === "@/server/session") return { url: shim, shortCircuit: true }
  const res = await next(specifier, context)
  if (/\/apps\/app\/src\/server\/session\.ts(\?.*)?$/.test(res.url)) return { url: shim, shortCircuit: true }
  return res
}

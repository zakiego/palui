// Taint tracking for flows (RFC flows 4.3). A run keeps the set of root keys whose
// values came from untrusted data: "trigger", "steps.<id>", "vars.<name>",
// "flow.<name>", "item". A value is tainted when any path it reads starts at a
// tainted root. Taint only changes what needs approval and how text is shown,
// never which branches exist.
import { pathsOf, rootKey, type Node, type PathNode } from "./expr"
import { templatePaths, templatesIn } from "./template"

export type Taint = string[]

// Root keys that can't carry outside data.
const CLEAN = new Set(["now", "run", "index"])

export function keyTainted(taint: Taint, key: string): boolean {
  if (CLEAN.has(key)) return false
  if (taint.includes(key)) return true
  // A dynamic read ("steps" with a computed first segment) is tainted when any of its kind is.
  if (key === "steps" || key === "vars" || key === "flow") return taint.some((t) => t.startsWith(`${key}.`))
  return false
}

export function pathsTainted(taint: Taint, paths: PathNode[]): boolean {
  return paths.some((p) => keyTainted(taint, rootKey(p)))
}

export function exprTainted(taint: Taint, ast: Node): boolean {
  return pathsTainted(taint, pathsOf(ast))
}

// Any template string inside `v` (a string, args object, input map) reads a tainted root.
export function valueTainted(taint: Taint, v: unknown): boolean {
  return templatesIn(v).some((t) => pathsTainted(taint, templatePaths(t)))
}

// The arg names (top level) that read a tainted root.
export function taintedArgs(taint: Taint, args: Record<string, unknown>): string[] {
  return Object.entries(args)
    .filter(([, v]) => valueTainted(taint, v))
    .map(([k]) => k)
}

export function addTaint(taint: Taint, ...keys: string[]): Taint {
  const out = new Set(taint)
  for (const k of keys) out.add(k)
  return [...out].sort()
}

export function removeTaint(taint: Taint, key: string): Taint {
  return taint.filter((t) => t !== key)
}

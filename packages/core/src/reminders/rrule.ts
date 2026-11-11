// rrule ships a UMD main and an ESM "module" build with no "exports" map. Node
// (tsx, the runtime) loads the UMD file, where the named exports live on the
// default export; Vite/vitest may load the ESM build, where they're named. This
// shim hands out the same classes in both cases.
import * as ns from "rrule"

type RRuleModule = typeof ns
const mod: RRuleModule = (ns as RRuleModule).RRule ? (ns as RRuleModule) : (ns as unknown as { default: RRuleModule }).default

export const RRule = mod.RRule
export type RRule = ns.RRule
export type RRuleOptions = ns.Options
export const Weekday = mod.Weekday
export type Weekday = ns.Weekday

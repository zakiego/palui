// Server functions only return serializable types, and `unknown` isn't one.
// Values that come from JSON columns or the runtime are JSON already, so they
// cross as `Json`.
export type Json = string | number | boolean | null | Json[] | { [key: string]: Json }
export type JsonObject = { [key: string]: Json }

export function asJson<T = JsonObject>(value: unknown): T {
  return value as T
}

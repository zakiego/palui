import { randomBytes } from "node:crypto"

const ALPHABET = "0123456789abcdefghijklmnopqrstuvwxyz"

// Time-ordered ids: "<prefix>_<ms base36, 9 chars><10 random chars>". Sorting by
// id is sorting by creation time, which keeps indexes and pagination simple.
export function newId(prefix: string): string {
  const time = Date.now().toString(36).padStart(9, "0")
  let rand = ""
  for (const b of randomBytes(10)) rand += ALPHABET[b % 36]
  return `${prefix}_${time}${rand}`
}

export function randomToken(bytes = 24): string {
  return randomBytes(bytes).toString("base64url")
}

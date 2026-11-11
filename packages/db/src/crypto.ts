// Secrets at rest (PLAN 4.5): AES-256-GCM with a key derived from MASTER_KEY.
// Format: "v1.<iv b64url>.<tag b64url>.<ciphertext b64url>".
import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes, timingSafeEqual } from "node:crypto"

export class MasterKeyError extends Error {}

export function deriveKey(masterKey: string): Buffer {
  if (!masterKey || masterKey.length < 32) throw new MasterKeyError("MASTER_KEY must be set and at least 32 characters (openssl rand -base64 32).")
  return createHash("sha256").update(`palui:secrets:v1:${masterKey}`).digest()
}

export function encrypt(plaintext: string, masterKey: string): string {
  const key = deriveKey(masterKey)
  const iv = randomBytes(12)
  const cipher = createCipheriv("aes-256-gcm", key, iv)
  const ct = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()])
  const tag = cipher.getAuthTag()
  return ["v1", iv.toString("base64url"), tag.toString("base64url"), ct.toString("base64url")].join(".")
}

export function decrypt(blob: string, masterKey: string): string {
  const [v, iv, tag, ct] = blob.split(".")
  if (v !== "v1" || !iv || !tag || ct === undefined) throw new Error("Unknown secret format")
  const decipher = createDecipheriv("aes-256-gcm", deriveKey(masterKey), Buffer.from(iv, "base64url"))
  decipher.setAuthTag(Buffer.from(tag, "base64url"))
  return Buffer.concat([decipher.update(Buffer.from(ct, "base64url")), decipher.final()]).toString("utf8")
}

export function encryptJson(value: Record<string, string>, masterKey: string): string {
  return encrypt(JSON.stringify(value), masterKey)
}

export function decryptJson(blob: string | null | undefined, masterKey: string): Record<string, string> {
  if (!blob) return {}
  return JSON.parse(decrypt(blob, masterKey)) as Record<string, string>
}

// Shown in the UI instead of a secret: a known prefix plus the last 4
// characters. Values of 12 characters or fewer get no tail, so a short secret
// (a PIN, a 4-character password) never reaches the browser whole.
export const MASK_TAIL_MIN_LENGTH = 13
export function maskSecret(value: string): string {
  if (!value) return ""
  const prefix = /^(rcl_|zkg_|sk-|pk_|ghp_)/.exec(value)?.[1] ?? ""
  const tail = value.length >= MASK_TAIL_MIN_LENGTH ? value.slice(-4) : ""
  return `${prefix}••••${tail}`
}

// A stable token for app -> runtime control calls, derived so no extra key is needed.
export function controlToken(masterKey: string): string {
  return createHmac("sha256", deriveKey(masterKey)).update("palui:control").digest("base64url")
}

export function sha256(s: string): string {
  return createHash("sha256").update(s).digest("hex")
}

export function safeEqual(a: string, b: string): boolean {
  const ab = Buffer.from(a)
  const bb = Buffer.from(b)
  return ab.length === bb.length && timingSafeEqual(ab, bb)
}

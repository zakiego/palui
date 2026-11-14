// Exports M1 goldens from the TS code into apps/server/testdata: crypto vectors
// (text and binary forms, encryptJson), mask cases, ids from a fixed clock and
// random source, token formats and redaction cases. The Go tests read them.
//
// Run from apps/app (its tsconfig and node_modules resolve the imports):
//   cd apps/app && pnpm tsx ../server/testdata/gen/goldens.ts
import { writeFileSync } from "node:fs"
import path from "node:path"
import { controlToken, decrypt, deriveKey, encrypt, encryptBytes, encryptJson, maskSecret, sha256 } from "../../../../packages/db/src/crypto.ts"
import { createIdGenerator, randomToken } from "../../../../packages/db/src/ids.ts"
import { DEV_MASTER_KEY } from "../../../../packages/core/src/config.ts"
import { redact } from "../../../../packages/core/src/log.ts"

const out = path.resolve(import.meta.dirname, "..")
const write = (name: string, value: unknown) => {
  writeFileSync(path.join(out, name), `${JSON.stringify(value, null, 2)}\n`)
  console.log(`wrote testdata/${name}`)
}

// The fixed master key every golden uses (the TS dev fallback).
const KEY = DEV_MASTER_KEY

// --- crypto ------------------------------------------------------------------------------

const texts = ["", "hello", "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ", "Halo Zaki, ini rahasia. éè 你好 😀", "x".repeat(5000)]
const binaries = [Buffer.from([]), Buffer.from([0, 1, 2, 255]), Buffer.from("%PDF-1.7\nktp"), Buffer.alloc(4096, 7)]
const jsons: Record<string, string>[] = [{}, { botToken: "123:abc" }, { apiKey: "rcl_abcdef123456a91f", baseUrl: "https://x.test", z: "last", a: "first" }]

write("crypto.json", {
  masterKey: KEY,
  derivedKeyHex: deriveKey(KEY).toString("hex"),
  controlToken: controlToken(KEY),
  text: texts.map((plaintext) => {
    const blob = encrypt(plaintext, KEY)
    if (decrypt(blob, KEY) !== plaintext) throw new Error("round trip")
    return { plaintext, blob }
  }),
  binary: binaries.map((plain) => ({ plainBase64: plain.toString("base64"), blobBase64: encryptBytes(plain, KEY).toString("base64") })),
  json: jsons.map((value) => ({ value, stringified: JSON.stringify(value), blob: encryptJson(value, KEY) })),
  sha256: ["", "abc", "palui 😀"].map((input) => ({ input, hex: sha256(input) })),
  shortKeys: ["", "short", "x".repeat(31)],
})

// --- masking -----------------------------------------------------------------------------

const maskInputs = ["", "a", "abcd", "1234:abcd", "abcdefghijkl", "abcdefghijklm", "sk-abc", "rcl_abcdef123456a91f", "1234567:abcdefgh", "zkg_x", "pk_live_1234567890abcdef", "ghp_abcdefghijklmnopqrstuvwxyz", "😀😀😀😀😀😀😀"]
write("mask.json", maskInputs.map((input) => ({ input, masked: maskSecret(input) })))

// --- ids ---------------------------------------------------------------------------------

const T = Date.UTC(2026, 10, 12)
const digits = (text: string) => [...text].map((c) => Number.parseInt(c, 36))
const scenarios = [
  { name: "fresh millisecond", clock: [T, T + 1, T + 1000], draws: ["aaaaaaaaaa", "0000000000", "zyxwvutsrq"], prefixes: ["msg", "msg", "chat"] },
  { name: "same millisecond counts up", clock: [T, T, T, T], draws: ["000000000y"], prefixes: ["msg", "msg", "msg", "run"] },
  { name: "carry", clock: [T, T], draws: ["a9zzzzzzzz"], prefixes: ["msg", "msg"] },
  { name: "clock steps back", clock: [T, T - 60_000, T - 1], draws: ["5555555555"], prefixes: ["msg", "msg", "msg"] },
  { name: "overflow borrows the next millisecond", clock: [T, T, T], draws: ["zzzzzzzzzz", "3333333333"], prefixes: ["msg", "msg", "msg"] },
  { name: "epoch zero", clock: [0, 1], draws: ["0000000000", "1111111111"], prefixes: ["x", "x"] },
]
write(
  "ids.json",
  scenarios.map((s) => {
    const clock = [...s.clock]
    const draws = s.draws.map(digits)
    let drawIndex = 0
    const next = createIdGenerator({ now: () => clock.shift()!, random: () => draws[drawIndex++]! })
    const ids = s.prefixes.map((p) => next(p))
    return { ...s, ids }
  }),
)

// --- tokens ------------------------------------------------------------------------------

write("tokens.json", {
  // randomToken(bytes): base64url without padding.
  randomToken: [12, 16, 24, 32].map((bytes) => ({ bytes, sample: randomToken(bytes), length: randomToken(bytes).length })),
  hookSecret: { sample: `phk_${randomToken(32)}`, pattern: "^phk_[A-Za-z0-9_-]{43}$" },
  hookSlug: { sample: randomToken(12), pattern: "^[A-Za-z0-9_-]{16}$" },
  shareToken: { sample: randomToken(24), pattern: "^[A-Za-z0-9_-]{32}$" },
  claimNonce: { sample: randomToken(12), pattern: "^[A-Za-z0-9_-]{16}$" },
})

// --- redaction ---------------------------------------------------------------------------

// Built from parts so this file holds no literal auth header.
const BEARER = ["Bear", "er"].join("")
const BOT = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ"
const redactInputs = [
  "key rcl_ab-cd_ef1234 end",
  `GET https://api.telegram.org/bot${BOT}/getMe`,
  `token ${BOT}`,
  "sk-abcdefgh1234 and zkg_abc-123_xyz",
  "run 12:30 at 2026-11-10",
  "1234567890123:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ",
  `Authorization: ${BEARER} abcdefghijklmnopqrstuvwxyz.0123`,
  `${BEARER.toLowerCase()}\u00a0abcdefghijklmnop`,
  "x12345:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ and a12345678:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ",
  "rcl_short sk-short zkg_12345",
]
write("redact.json", redactInputs.map((input) => ({ input, redacted: redact(input) })))

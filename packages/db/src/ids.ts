import { randomBytes } from "node:crypto"

const ALPHABET = "0123456789abcdefghijklmnopqrstuvwxyz"
const RANDOM_LENGTH = 10

export type IdSource = {
  now: () => number
  // RANDOM_LENGTH base36 digits (0 to 35), most significant first.
  random: () => number[]
}

const systemSource: IdSource = {
  // Looked up on every call, so a test that mocks Date.now is seen.
  now: () => Date.now(),
  random: () => Array.from(randomBytes(RANDOM_LENGTH), (b) => b % ALPHABET.length),
}

// Adds one in place. Returns false when every digit was already the highest.
function increment(digits: number[]): boolean {
  for (let i = digits.length - 1; i >= 0; i--) {
    const digit = digits[i]!
    if (digit < ALPHABET.length - 1) {
      digits[i] = digit + 1
      return true
    }
    digits[i] = 0
  }
  return false
}

// A generator's ids are strictly increasing in the order it made them. A new
// millisecond draws a fresh random part. Inside one millisecond, or when the
// clock steps back, the next id is the previous one plus one (ULID's monotonic
// mode). If the random part runs out, the id borrows the next millisecond.
export function createIdGenerator(source: IdSource = systemSource): (prefix: string) => string {
  let time = -1
  let digits: number[] = []
  return (prefix) => {
    const now = source.now()
    if (now > time) {
      time = now
      digits = source.random()
    } else if (!increment(digits)) {
      time += 1
      digits = source.random()
    }
    let rand = ""
    for (const d of digits) rand += ALPHABET[d]
    return `${prefix}_${time.toString(36).padStart(9, "0")}${rand}`
  }
}

// Time-ordered ids: "<prefix>_<ms base36, 9 chars><10 random chars>". Sorting by
// id is sorting by creation time, which keeps indexes and pagination simple.
// That holds inside one process, even for ids made in the same millisecond. Two
// processes (app and runtime) can still tie within a millisecond.
//
// Ids are names, not secrets: one made in the same millisecond as another can
// be guessed from it. Anything that grants access uses randomToken.
export const newId = createIdGenerator()

export function randomToken(bytes = 24): string {
  return randomBytes(bytes).toString("base64url")
}

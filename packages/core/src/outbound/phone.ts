// Phone numbers for WhatsApp contacts. Indonesian local forms become 62...; other
// countries need the leading "+" (or "00").

// Something that looks like a phone number (not a name).
export function looksLikePhone(raw: string): boolean {
  return /^\+?[\d\s().-]{8,24}$/.test(raw.trim())
}

// "0822 0000-1001", "+62 822 0000 1001", "6282200001001", "82200001001" -> "6282200001001".
// Null when it can't be a number.
export function normalizePhone(raw: string): string | null {
  const s = raw.trim()
  if (!looksLikePhone(s)) return null
  let digits = s.replace(/\D/g, "")
  if (s.startsWith("+")) {
    // Already international.
  } else if (digits.startsWith("00")) {
    digits = digits.slice(2)
  } else if (digits.startsWith("0")) {
    digits = `62${digits.slice(1)}`
  } else if (digits.startsWith("8")) {
    digits = `62${digits}`
  } else if (!digits.startsWith("62")) {
    return null
  }
  if (digits.startsWith("620")) digits = `62${digits.slice(3)}`
  if (digits.length < 10 || digits.length > 15) return null
  return digits
}

export function waJid(digits: string): string {
  return `${digits}@s.whatsapp.net`
}

// "+62 822-0000-1001" for 62 numbers, "+<digits>" otherwise.
export function formatPhone(digits: string): string {
  if (digits.startsWith("62") && digits.length >= 11) {
    const rest = digits.slice(2)
    return `+62 ${rest.slice(0, 3)}-${rest.slice(3, 7)}-${rest.slice(7)}`
  }
  return `+${digits}`
}

// Every phone number written in a text, normalized.
export function phonesIn(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/\+?\d[\d\s().-]{7,22}\d/g)) {
    const n = normalizePhone(m[0])
    if (n) out.push(n)
  }
  return out
}

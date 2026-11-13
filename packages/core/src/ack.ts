// The reaction a channel puts on the message being answered while the agent
// thinks (PLAN 5, WhatsApp only). Each chat can turn it off or pick its emoji.
// No database imports: the dashboard's client bundle reads this file.

export const DEFAULT_ACK_EMOJI = "⏳"

// What the dashboard offers. /ack takes any single emoji.
export const ACK_EMOJI_CHOICES = ["⏳", "👀", "🤔", "✍️", "🧠", "🙏", "👍", "🔥"] as const

const segmenter = new Intl.Segmenter(undefined, { granularity: "grapheme" })

// One emoji (a single grapheme, so skin tones and ZWJ sequences count), or null.
// WhatsApp rejects a reaction that isn't an emoji.
export function parseAckEmoji(input: string): string | null {
  const s = input.trim()
  if (!s || s.length > 32) return null
  if ([...segmenter.segment(s)].length !== 1) return null
  return /\p{Extended_Pictographic}|\p{Regional_Indicator}/u.test(s) ? s : null
}

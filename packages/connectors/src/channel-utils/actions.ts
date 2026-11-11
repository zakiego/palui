// Channels without buttons (WhatsApp) show outbox actions as a reply hint instead:
// "Balas: done / tunda 1j / tunda besok". The words come from the action id the
// router understands (AGENTS.md "Outbox actions"), falling back to the label.

export type OutboxAction = { id: string; label: string }

const SNOOZE_WORDS: Record<string, string> = {
  "10m": "tunda 10m",
  "1h": "tunda 1j",
  tomorrow: "tunda besok",
}

// The word the owner types to trigger this action. The router maps a reply back
// with the same function, so both sides agree on the vocabulary.
export function actionReplyWord(action: OutboxAction): string {
  const parts = action.id.split(":")
  const [head, second, , fourth] = parts
  if (head === "approve") return "ya"
  if (head === "deny") return "batal"
  if (head === "rem" && second === "done") return "done"
  if (head === "rem" && second === "snooze" && fourth && SNOOZE_WORDS[fourth]) return SNOOZE_WORDS[fourth]
  if (head === "connector" && (second === "on" || second === "off") && parts[2]) return `/connectors ${second} ${parts.slice(2).join(":")}`
  return action.label.trim().toLowerCase()
}

export function actionsHint(actions: OutboxAction[] | undefined): string {
  if (!actions?.length) return ""
  const words = [...new Set(actions.map(actionReplyWord).filter(Boolean))]
  return words.length ? `Balas: ${words.join(" / ")}` : ""
}

// The action a free-text reply picks, when it matches one word exactly
// (case-insensitive, surrounding whitespace ignored).
export function matchActionReply(text: string, actions: OutboxAction[]): OutboxAction | undefined {
  const t = text.trim().toLowerCase()
  if (!t) return undefined
  return actions.find((a) => actionReplyWord(a).toLowerCase() === t)
}

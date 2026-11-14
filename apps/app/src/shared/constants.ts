// Values the dashboard shares with the Go server (apps/server). The server
// validates against its own copy, named next to each one here, so a change
// goes in both places.

// internal/ack (EmojiChoices): the reactions WhatsApp shows while a reply is written.
export const ACK_EMOJI_CHOICES = ["⏳", "👀", "🤔", "✍️", "🧠", "🙏", "👍", "🔥"] as const

// internal/agents: avatar icons and colors.
export const AGENT_ICONS = [
  "bot",
  "sparkles",
  "smile",
  "heart",
  "star",
  "chef-hat",
  "coffee",
  "briefcase",
  "headset",
  "store",
  "home",
  "book-open",
  "graduation-cap",
  "code",
  "wrench",
  "calculator",
  "scale",
  "stethoscope",
  "dumbbell",
  "leaf",
  "music",
  "camera",
  "plane",
  "moon",
] as const
export type AgentIcon = (typeof AGENT_ICONS)[number]
export const AGENT_COLORS = ["zinc", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"] as const
export type AgentColor = (typeof AGENT_COLORS)[number]

// internal/settings: reasoning effort per model chain link.
export const REASONING_EFFORTS = ["none", "minimal", "low", "medium", "high", "xhigh", "max"] as const
export type ReasoningEffort = (typeof REASONING_EFFORTS)[number]

// internal/settings (ModelRoles): one link of a role's model chain.
export type ModelChainLink = { connectorId: string; model: string; effort?: ReasoningEffort }

// internal/settings: the outbound signature's length cap.
export const MAX_SIGNATURE = 150

// The built-in chat commands (PLAN 8.2). One list for the router and for flow
// validation, so a flow can never claim a name the router takes first.
export const COMMANDS = {
  "/new": "new",
  "/stop": "stop",
  "/status": "status",
  "/connectors": "connectors",
  "/agent": "agent",
  "/reply": "reply",
  "/help": "help",
  "/models": "models",
  "/model": "model",
  "/usage": "usage",
  "/btw": "btw",
  "/compact": "compact",
  "/tools": "tools",
  "/id": "id",
  "/report": "report",
} as const

export type CommandName = (typeof COMMANDS)[keyof typeof COMMANDS]

// Names a flow's chat trigger can't use. `/start` is Telegram's handshake.
export const BUILTIN_COMMANDS: string[] = [...Object.keys(COMMANDS), "/start"]

package chat

// The built-in chat commands (command-names.ts, PLAN 8.2). One list for the
// router and for flow validation, so a flow can never claim a name the router
// takes first.

// Commands maps the typed command to its name.
var Commands = map[string]string{
	"/new":        "new",
	"/stop":       "stop",
	"/status":     "status",
	"/connectors": "connectors",
	"/agent":      "agent",
	"/reply":      "reply",
	"/ack":        "ack",
	"/help":       "help",
	"/models":     "models",
	"/model":      "model",
	"/usage":      "usage",
	"/btw":        "btw",
	"/compact":    "compact",
	"/tools":      "tools",
	"/id":         "id",
	"/report":     "report",
}

// BuiltinCommands is BUILTIN_COMMANDS: names a flow's chat trigger can't use,
// in the TS order. `/start` is Telegram's handshake.
var BuiltinCommands = []string{"/new", "/stop", "/status", "/connectors", "/agent", "/reply", "/ack", "/help", "/models", "/model", "/usage", "/btw", "/compact", "/tools", "/id", "/report", "/start"}

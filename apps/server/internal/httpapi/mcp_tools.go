package httpapi

// MCPTool maps one dashboard server function to an MCP tool. The input schema
// is the function's own zod schema; reads are the GET functions.
type MCPTool struct {
	Name        string
	RPC         string
	Title       string
	Description string
	// Destructive: deletes or cuts something off that can't be undone in one step.
	Destructive bool
	// Idempotent: calling it twice with the same input changes nothing more.
	Idempotent bool
}

// MCPExcluded are the server functions /mcp doesn't offer, and why. A test
// fails when a new server function is in neither list, so each one is a
// decision.
var MCPExcluded = map[string]string{
	"getSession":         "dashboard sign-in state",
	"getInvite":          "owner invite links are for the browser",
	"acceptInvite":       "signs a browser in",
	"listPasskeys":       "passkeys are managed in the browser",
	"removePasskey":      "passkeys are managed in the browser",
	"getShell":           "dashboard chrome",
	"dismissChecklist":   "dashboard chrome",
	"saveConnectorDraft": "carries connector secrets; add connectors in the dashboard",
	"connectConnectorFn": "carries connector secrets; add connectors in the dashboard",
	"getFixtureState":    "dev fixtures",
	"setFixture":         "dev fixtures",
	"getMcpAccess":       "a token can't manage MCP access",
	"createMcpToken":     "a token can't mint more tokens",
	"revokeMcpAccess":    "a token can't manage MCP access",
	"getOAuthRequest":    "OAuth consent is the owner's, in the browser",
	"decideOAuthRequest": "OAuth consent is the owner's, in the browser",
	"setConnectorDataFn": "decides what strangers in a shared chat can reach; the owner's call, in the dashboard",
}

// MCPTools is the catalog, grouped as the dashboard is.
func MCPTools() []MCPTool {
	return []MCPTool{
		// Overview and activity
		{Name: "get_status", RPC: "getStatusSummary", Title: "Status summary", Description: "What needs attention right now: connector problems, pending approvals and the counts the dashboard header shows."},
		{Name: "get_home", RPC: "getHome", Title: "Home overview", Description: "The dashboard home: connector map, the next reminders, recent runs and token use today."},
		{Name: "list_events", RPC: "getEvents", Title: "List events", Description: "Recent log events, newest first. Filter by connectorId and levels (debug, info, warn, error)."},
		{Name: "list_runs", RPC: "getRuns", Title: "List runs", Description: "Recent agent runs (turns, jobs, flows) with status, model and token use."},
		{Name: "get_run", RPC: "getRun", Title: "Get run", Description: "One run with its tool calls and result. id comes from list_runs."},
		{Name: "get_usage", RPC: "getUsage", Title: "Token usage", Description: "Token use per day and per model."},

		// Settings
		{Name: "get_settings", RPC: "getSettingsPage", Title: "Get settings", Description: "All settings: persona, timezone, languages, model roles and fallbacks, budgets, the fallback channel, webhooks and backups."},
		{Name: "update_settings", RPC: "saveSettings", Title: "Update settings", Description: "Change settings. Send only the keys to change (timezone, language, uiLanguage, modelRoles, budgets, fallbackChannel, ...). Read get_settings first for the current values and shapes.", Idempotent: true},
		{Name: "list_connector_models", RPC: "listConnectorModels", Title: "List models of a connector", Description: "The models a model connector (CLIProxyAPI, AI Gateway) offers, for update_settings modelRoles. id is the connector id."},
		{Name: "create_webhook", RPC: "createHookFn", Title: "Create webhook", Description: "Create an inbound webhook that posts into a chat. mode forward sends the text as is; summarize runs a tool-less turn. Returns the URL and signing secret once."},

		// Agents
		{Name: "list_agents", RPC: "getAgentsPage", Title: "List agents", Description: "Every agent with its id, slug, name, persona summary, guest tools setting and the chats it serves."},
		{Name: "get_agent", RPC: "getAgentPage", Title: "Get agent", Description: "One agent in full, by slug (from list_agents): persona, guest tools, token cap, recall settings, chats."},
		{Name: "get_agent_tools", RPC: "getAgentToolsFn", Title: "Get agent tools", Description: "The tools an agent can call and each one's permission (allow, ask, deny). id is the agent id."},
		{Name: "get_agent_memory", RPC: "getAgentMemoryFn", Title: "Get agent memory", Description: "What an agent remembers in its recall memory. id is the agent id."},
		{Name: "save_agent", RPC: "saveAgentFn", Title: "Create or update agent", Description: "Create an agent, or update one when id is given. Only the fields sent change. persona is the system prompt; guestTools none or safe."},
		{Name: "list_agent_members", RPC: "getAgentMembersFn", Title: "List agent members", Description: "The people an AI agent is shared with (members), each with their WhatsApp or Telegram accounts and every agent they're in. Also lists members of other agents and people who wrote in a chat but aren't members yet. id is the agent id."},
		{Name: "save_member", RPC: "saveMemberFn", Title: "Create or update member", Description: "Add a member to an agent (no id) or rename one and replace their accounts (id from list_agent_members). accounts is 1 to 5 of channel whatsapp or telegram with an address: a phone number, a WhatsApp JID, or a numeric Telegram user id. The owner's own account and accounts another member holds are refused."},
		{Name: "set_agent_member", RPC: "setAgentMemberFn", Title: "Add or remove agent member", Description: "Add an existing member to an agent (on true) or take them out (on false). Taking a member out of their last agent deletes them. The default agent has no members.", Destructive: true, Idempotent: true},
		{Name: "set_member_rules", RPC: "setMemberRulesFn", Title: "Let a member set chat rules", Description: "Let a member of an AI agent add and remove rules for the shared chats they're in (on true), or stop that (on false). Off by default. agentId is the agent id, memberId from list_agent_members.", Idempotent: true},
		{Name: "get_chat_rules", RPC: "getChatRulesFn", Title: "Get chat rules", Description: "The rules people set from an AI agent's shared chats, each with its chat and who added it, plus the latest adds and removes. id is the agent id."},
		{Name: "delete_chat_rule", RPC: "deleteChatRuleFn", Title: "Delete chat rule", Description: "Remove one chat rule (id from get_chat_rules). agentId is the agent id.", Destructive: true},
		{Name: "undo_chat_rule_change", RPC: "undoChatRuleChangeFn", Title: "Undo chat rule change", Description: "Undo an add or remove from get_chat_rules changes: an add takes the rule out, a remove puts it back. agentId is the agent id, id the change id."},
		{Name: "delete_agent", RPC: "deleteAgentFn", Title: "Delete agent", Description: "Delete an agent. Its chats go back to the default agent. The default agent can't be deleted.", Destructive: true},
		{Name: "set_agent_tool_permissions", RPC: "setAgentToolPermissionsFn", Title: "Set agent tool permissions", Description: "Set allowAll, or per-tool allow, ask or deny (null clears one) for an agent. Tool names come from get_agent_tools.", Idempotent: true},

		// Chats
		{Name: "list_chats", RPC: "getChats", Title: "List chats", Description: "Every chat on every channel with its id, title, agent, reply mode and whether it's allowed."},
		{Name: "get_transcript", RPC: "getTranscript", Title: "Get transcript", Description: "Recent messages of a chat, with the agent that answers it and the agents it can switch to."},
		{Name: "set_chat_agent", RPC: "setChatAgentFn", Title: "Set chat agent", Description: "Pick the agent that answers a chat. agentId null means the default agent.", Idempotent: true},
		{Name: "set_chat_reply_mode", RPC: "setChatReplyModeFn", Title: "Set chat reply mode", Description: "In a group chat, answer only when mentioned (mention) or every message (all).", Idempotent: true},
		{Name: "set_chat_allowed", RPC: "setChatAllowedFn", Title: "Allow or block chat", Description: "Allow a chat to talk to Palui, or block it.", Idempotent: true},
		{Name: "set_chat_ack", RPC: "setChatAckFn", Title: "Set chat acknowledgement", Description: "Turn the reaction Palui puts on a message it's working on on or off, and pick its emoji.", Idempotent: true},
		{Name: "set_chat_deploy_notice", RPC: "setChatDeployNoticeFn", Title: "Set deploy notice", Description: "Whether this chat hears about new Palui versions after a deploy.", Idempotent: true},
		{Name: "send_web_message", RPC: "sendWebMessage", Title: "Send web chat message", Description: "Send a message to Palui in the dashboard's web chat, as the owner. The reply arrives in the web chat; read it with get_transcript."},
		{Name: "decide_approval", RPC: "decideApproval", Title: "Decide approval", Description: "Approve or deny a tool call that waits for the owner. id comes from get_status or a transcript."},
		{Name: "stop_turn", RPC: "stopTurn", Title: "Stop turn", Description: "Stop the turn running in a chat."},

		// Connectors
		{Name: "list_connectors", RPC: "getConnectorsPage", Title: "List connectors", Description: "Every connector (channels, models, memory, tools) with its id, type, status and whether it's on. Secrets are masked."},
		{Name: "get_connector", RPC: "getConnectorDetail", Title: "Get connector", Description: "One connector: config with masked secrets, status, recent events, and its MCP tools when it has them."},
		{Name: "pause_connector", RPC: "pauseConnectorFn", Title: "Pause connector", Description: "Turn a connector off. Its config is kept.", Idempotent: true},
		{Name: "resume_connector", RPC: "resumeConnectorFn", Title: "Resume connector", Description: "Turn a paused connector back on.", Idempotent: true},
		{Name: "test_connector", RPC: "testConnectorFn", Title: "Test connector", Description: "Run a connector's connection test and return the result."},
		{Name: "remove_connector", RPC: "removeConnectorFn", Title: "Remove connector", Description: "Delete a connector with its config and secrets.", Destructive: true},
		{Name: "run_connector_action", RPC: "connectorActionFn", Title: "Run connector action", Description: "Run a named action a connector's runtime offers (for example authUrl on Google Calendar). input is the action's arguments. Use only an action the user or the connector's docs named."},
		{Name: "set_connector_owner", RPC: "setConnectorOwnerFn", Title: "Set connector owner agent", Description: "Bind a channel connector (an agent bot) to an agent, or null to unbind.", Idempotent: true},
		{Name: "set_whatsapp_owner", RPC: "setWhatsAppOwnerFn", Title: "Set WhatsApp owner", Description: "Set the owner's own WhatsApp number, so Palui knows which chat is the owner's.", Idempotent: true},

		// Notes
		{Name: "get_notes", RPC: "getNotes", Title: "Get notes", Description: "Standing instructions and preferences, for one agent (agentId) or the default."},
		{Name: "save_instruction", RPC: "saveInstructionFn", Title: "Save instruction", Description: "Create a standing instruction, or update one when id is given."},
		{Name: "set_instruction_enabled", RPC: "setInstructionEnabledFn", Title: "Enable or disable instruction", Description: "Turn a standing instruction on or off.", Idempotent: true},
		{Name: "delete_instruction", RPC: "deleteInstructionFn", Title: "Delete instruction", Description: "Delete a standing instruction.", Destructive: true},
		{Name: "set_preference", RPC: "setPreferenceFn", Title: "Set preference", Description: "Set a preference (key and value) the agent keeps in mind.", Idempotent: true},
		{Name: "delete_preference", RPC: "deletePreferenceFn", Title: "Delete preference", Description: "Delete a preference by key.", Destructive: true},

		// Custom chat commands
		{Name: "list_commands", RPC: "getCommandsPage", Title: "List custom commands", Description: "The owner's custom chat commands (a saved prompt under a /name, for every agent or one agent), the agents, and the built-in names they can't use."},
		{Name: "save_command", RPC: "saveCommandFn", Title: "Save custom command", Description: "Create a custom chat command, or update one when id is given. agentId null is every agent. {{args}} in prompt is the text typed after the command. attachReport adds a /report debug snapshot (private chats only)."},
		{Name: "set_command_enabled", RPC: "setCommandEnabledFn", Title: "Enable or disable custom command", Description: "Turn a custom chat command on or off.", Idempotent: true},
		{Name: "delete_command", RPC: "deleteCommandFn", Title: "Delete custom command", Description: "Delete a custom chat command.", Destructive: true},

		// Jobs, flows, reminders
		{Name: "list_jobs", RPC: "getJobs", Title: "List jobs", Description: "Scheduled jobs (including the daily briefing and heartbeat) with their schedule, chat, next run and last result."},
		{Name: "get_job_preset", RPC: "getJobPreset", Title: "Get job preset", Description: "A ready-made job (dailyBriefing or heartbeat) for a chat, to pass to save_job."},
		{Name: "save_job", RPC: "saveJob", Title: "Create or update job", Description: "Create a scheduled job, or update one when id is given. rrule is an RFC 5545 rule starting with FREQ=, in tz."},
		{Name: "set_job_enabled", RPC: "setJobEnabledFn", Title: "Enable or disable job", Description: "Turn a job on or off.", Idempotent: true},
		{Name: "run_job_now", RPC: "runJobNow", Title: "Run job now", Description: "Run a job once right away, outside its schedule."},
		{Name: "delete_job", RPC: "deleteJobFn", Title: "Delete job", Description: "Delete a scheduled job.", Destructive: true},
		{Name: "list_flows", RPC: "getFlows", Title: "List flows", Description: "Flows with their triggers, current version and recent runs."},
		{Name: "get_flow", RPC: "getFlowFn", Title: "Get flow", Description: "One flow: its steps, versions, approvals and runs."},
		{Name: "list_reminders", RPC: "getReminders", Title: "List reminders", Description: "Upcoming and recent reminders with their chat, time and repeat rule."},
		{Name: "parse_reminder", RPC: "parseReminder", Title: "Parse reminder", Description: "Turn natural text into a reminder time and repeat rule without saving it. Use the result in create_reminder.", Idempotent: true},
		{Name: "create_reminder", RPC: "createReminderFn", Title: "Create reminder", Description: "Create a reminder in a chat at fireAtIso (in tz), optionally repeating with an rrule."},
		{Name: "cancel_reminder", RPC: "cancelReminderFn", Title: "Cancel reminder", Description: "Cancel a reminder.", Destructive: true},
		{Name: "snooze_reminder", RPC: "snoozeReminderFn", Title: "Snooze reminder", Description: "Snooze a reminder by 10m, 1h or to tomorrow."},

		// Outbound
		{Name: "get_outbound", RPC: "getOutbound", Title: "Get outbound", Description: "Outbound messaging: whether it's on, the limits, contacts with their policy, and scheduled messages."},
		{Name: "set_outbound_enabled", RPC: "setOutboundSwitch", Title: "Turn outbound on or off", Description: "Allow or stop Palui sending messages to contacts.", Idempotent: true},
		{Name: "save_outbound_limits", RPC: "saveOutboundLimits", Title: "Save outbound limits", Description: "Set the daily limits, the hold before sending, and the signature.", Idempotent: true},
		{Name: "add_contact", RPC: "addContactFn", Title: "Add contact", Description: "Add a contact an agent may message, by phone or chat id. policy ask needs the owner's yes per message; allow doesn't."},
		{Name: "set_contact_policy", RPC: "setContactPolicy", Title: "Set contact policy", Description: "Set a contact to ask or allow.", Idempotent: true},
		{Name: "remove_contact", RPC: "removeContactFn", Title: "Remove contact", Description: "Remove a contact.", Destructive: true},
		{Name: "cancel_outbound", RPC: "cancelOutboundFn", Title: "Cancel outbound message", Description: "Cancel a scheduled outbound message.", Destructive: true},

		// Attachments
		{Name: "list_attachments", RPC: "getAttachments", Title: "List attachments", Description: "Saved attachments with their chat, type and size."},
		{Name: "get_attachment_shares", RPC: "getAttachmentShares", Title: "Get attachment shares", Description: "The share links of one attachment."},
		{Name: "create_attachment_share", RPC: "createAttachmentShareFn", Title: "Share attachment", Description: "Make a share link for an attachment: public or private (owner only), for 1h, 24h or 7d."},
		{Name: "revoke_attachment_share", RPC: "revokeAttachmentShareFn", Title: "Revoke attachment share", Description: "Stop a share link working.", Destructive: true},
		{Name: "delete_attachment", RPC: "deleteAttachmentFn", Title: "Delete attachment", Description: "Delete a saved attachment and its share links.", Destructive: true},
	}
}

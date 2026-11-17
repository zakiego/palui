package chat

// The router (router.ts, PLAN 8.2): decides what an inbound message is before
// any model sees it. Order: sender checks, hook events, approval answers,
// commands, reminder actions, then a turn.

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Route is router.ts's Route. Kind is ignore, command, approval, reminder,
// hook, handled or turn.
type Route struct {
	Kind string
	// ignore.
	Reason string
	// command.
	Name string
	Args []string
	// approval. Batch: a bare "ya"/"batal" with several requests waiting
	// answers every pending request of the newest run.
	ApprovalID string
	Approved   bool
	Batch      []string
	// reminder: a reminder or flow check-in action, answered without the model.
	Reply string
	// hook.
	Hook *StoredHook
	Text string
}

// ChatScope is chatScope: an agent's own Telegram bot keeps its chats apart
// from the main bot's (PLAN 19.6). "" is the channel's main connector.
func ChatScope(channel, connectorID string) string {
	if channel == "telegram" && connectorID != "" && connectorID != "telegram" {
		return connectorID
	}
	return ""
}

// ScopeAgentID is scopeAgentId: the agent a new chat in this scope starts with.
func ScopeAgentID(q db.Q, scope string) (*string, error) {
	if scope == "" {
		return nil, nil
	}
	a, ok, err := agents.ForTelegramConnector(q, scope)
	if err != nil || !ok {
		return nil, err
	}
	return &a.ID, nil
}

// SenderVerdict is senderCheck's result. Quiet: a group message nobody
// addressed to us, dropped without an event.
type SenderVerdict struct {
	OK        bool
	FromOwner bool
	Reason    string
	Quiet     bool
	// Context: a group message nobody addressed to the agent, in a chat set
	// up with one. It's stored as context for later turns and never answered.
	Context bool
}

// SenderCheck is senderCheck: checks that need only the inbound message and
// the chat row, if any (PLAN 19.2). A stranger's message in a chat we've never
// seen creates the chat row (not allowed), so the owner can set it up.
func SenderCheck(q db.Q, msg connectors.InboundMessage, now int64) (SenderVerdict, error) {
	if msg.FromMe {
		return SenderVerdict{Reason: fmt.Sprintf("Dropped a %s message sent from the bot's own account (fromMe).", msg.Channel)}, nil
	}
	isOwner, err := owner.IsOwner(q, owner.Sender{Channel: msg.Channel, ExternalID: msg.SenderExternalID, AltID: msg.SenderAltID})
	if err != nil {
		return SenderVerdict{}, err
	}
	scope := ChatScope(msg.Channel, msg.ConnectorID)
	chat, found, err := chats.Find(q, msg.Channel, msg.ChatExternalID, scope)
	if err != nil {
		return SenderVerdict{}, err
	}
	group := msg.ChatKind == "group"
	addressed := !group || msg.Addressed || (found && chat.Allowed && chat.ReplyMode == "all")
	// In a set-up group the agent reads everything and answers only what's
	// addressed to it, so the rest is kept as context.
	asContext := found && chat.Allowed && chat.AgentID != nil
	if isOwner {
		// Commands and buttons always go through, even before the chat is set up.
		if !addressed && msg.Callback == nil && !strings.HasPrefix(strx.Trim(msg.Text), "/") {
			if asContext {
				return SenderVerdict{OK: true, FromOwner: true, Context: true}, nil
			}
			return SenderVerdict{Reason: "Group message not addressed to the agent.", Quiet: true}, nil
		}
		return SenderVerdict{OK: true, FromOwner: true}, nil
	}
	if !found {
		agentID, err := ScopeAgentID(q, scope)
		if err != nil {
			return SenderVerdict{}, err
		}
		no := false
		if chat, err = chats.Ensure(q, chats.EnsureInput{Channel: msg.Channel, ExternalID: msg.ChatExternalID, Title: msg.ChatTitle, Kind: msg.ChatKind, Allowed: &no, ConnectorID: scope, AgentID: agentID}, now); err != nil {
			return SenderVerdict{}, err
		}
	}
	if !addressed {
		if asContext {
			return SenderVerdict{OK: true, Context: true}, nil
		}
		return SenderVerdict{Reason: "Group message not addressed to the agent.", Quiet: true}, nil
	}
	if !chat.Allowed || chat.AgentID == nil {
		// Chatter in an "all" group nobody set up for guests isn't worth an event.
		if group && !msg.Addressed {
			return SenderVerdict{Reason: "Group message in a chat that isn't set up for guests.", Quiet: true}, nil
		}
		who := ""
		if msg.SenderName != "" {
			who = msg.SenderName + " "
		}
		alt := ""
		if msg.SenderAltID != "" {
			alt = ", alt " + msg.SenderAltID
		}
		who += fmt.Sprintf("(%s%s)", msg.SenderExternalID, alt)
		return SenderVerdict{Reason: fmt.Sprintf("Dropped a %s message from %s: not the owner, and this chat isn't set up for guests (allow it and give it an agent on the Chats page).", msg.Channel, who)}, nil
	}
	return SenderVerdict{OK: true}, nil
}

// ChatCheck is chatCheck: checks that need the chat row ("" when it passes).
func ChatCheck(chat db.Chat) string {
	if chat.Channel == "whatsapp" && !chat.Allowed {
		return fmt.Sprintf("Dropped a WhatsApp message in %s: the chat isn't in Palui's allowlist.", chat.ExternalID)
	}
	return ""
}

var (
	spaceSplit = regexp.MustCompile(`[` + strx.SpaceClass + `]+`)
	botSuffix  = regexp.MustCompile(`@[^` + strx.SpaceClass + `]+$`)
)

// ParsedCommand is parseCommand's result.
type ParsedCommand struct {
	Name string
	Args []string
}

// ParseCommand is parseCommand: a built-in command, with a Telegram @bot
// suffix and in any case. ok false when the text isn't one.
func ParseCommand(text string) (ParsedCommand, bool) {
	t := strx.Trim(text)
	if !strings.HasPrefix(t, "/") {
		return ParsedCommand{}, false
	}
	fields := spaceSplit.Split(t, -1)
	head := botSuffix.ReplaceAllString(strings.ToLower(fields[0]), "")
	name, ok := Commands[head]
	if !ok {
		return ParsedCommand{}, false
	}
	return ParsedCommand{Name: name, Args: append([]string{}, fields[1:]...)}, true
}

var (
	yesWords  = map[string]bool{"ya": true, "y": true, "iya": true, "yes": true}
	noWords   = map[string]bool{"batal": true, "no": true, "tidak": true, "nggak": true, "gak": true}
	trailBang = regexp.MustCompile(`[.!]+$`)
)

// ParseYesNo is parseYesNo: true for ya, false for batal, ok false otherwise.
func ParseYesNo(text string) (yes bool, ok bool) {
	t := trailBang.ReplaceAllString(strings.ToLower(strx.Trim(text)), "")
	if yesWords[t] {
		return true, true
	}
	if noWords[t] {
		return false, true
	}
	return false, false
}

// quotedApproval: the approval whose prompt this message quotes, by the
// prompt's channel id or through the outbox row that sent it.
func quotedApproval(q db.Q, chatID, channelMsgID string) (string, error) {
	var id string
	err := q.QueryRow("SELECT id FROM pending_approvals WHERE (ask_chat_id = ? OR (ask_chat_id IS NULL AND chat_id = ?)) AND prompt_msg_id = ?", chatID, chatID, channelMsgID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !isNoRows(err) {
		return "", err
	}
	var source string
	err = q.QueryRow("SELECT source FROM outbox WHERE chat_id = ? AND channel_msg_id = ? AND source LIKE 'approval:%'", chatID, channelMsgID).Scan(&source)
	if isNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(source, "approval:"), nil
}

// RouteInput is RouteInput.
type RouteInput struct {
	DB                  db.Q
	Chat                db.Chat
	Content             StoredContent
	Text                string
	ReplyToChannelMsgID string
	// The stored message id (chat triggers key their run on it).
	MessageID       string
	ReminderActions ReminderActions
	FlowActions     FlowActions
	Outbound        Outbound
	// false for a guest (PLAN 19.3): approvals, buttons, commands and
	// reminder actions are the owner's.
	FromOwner bool
	Now       int64
}

// The commands that work in a chat that's off (PLAN 19.2).
var setupCommands = map[string]bool{"agent": true, "reply": true, "ack": true, "help": true, "id": true}

var (
	approveData   = regexp.MustCompile(`^(approve|deny):(.+)$`)
	connectorData = regexp.MustCompile(`^connector:(on|off):(.+)$`)
	cancelData    = regexp.MustCompile(`^ob:cancel:(.+)$`)
)

// RouteMessage is route.
func RouteMessage(i RouteInput) (Route, error) {
	if blocked := ChatCheck(i.Chat); blocked != "" {
		// The owner sets a chat up from inside it: the setup commands still
		// run, and anything else addressed to the agent gets the setup prompt.
		if !i.FromOwner || i.Content.Hook != nil || i.Content.Callback != nil {
			return Route{Kind: "ignore", Reason: blocked}, nil
		}
		text := strx.Trim(i.Text)
		cmd, isCmd := ParseCommand(text)
		if isCmd && setupCommands[cmd.Name] {
			return Route{Kind: "command", Name: cmd.Name, Args: cmd.Args}, nil
		}
		if !isCmd && i.Chat.Kind == "group" && text != "" && !strings.HasPrefix(text, "/") {
			return Route{Kind: "command", Name: "agent", Args: []string{}}, nil
		}
		return Route{Kind: "ignore", Reason: blocked}, nil
	}
	if i.Content.Hook != nil {
		return Route{Kind: "hook", Hook: i.Content.Hook, Text: i.Text}, nil
	}
	if !i.FromOwner {
		if i.Content.Callback != nil {
			return Route{Kind: "ignore", Reason: "A guest pressed a button; buttons are the owner's."}, nil
		}
		if strx.Trim(i.Text) == "" {
			return Route{Kind: "ignore", Reason: "Empty message."}, nil
		}
		return Route{Kind: "turn"}, nil
	}

	if i.Content.Callback != nil && i.Content.Callback.Data != "" {
		cb := i.Content.Callback.Data
		if m := approveData.FindStringSubmatch(cb); m != nil {
			return Route{Kind: "approval", ApprovalID: m[2], Approved: m[1] == "approve"}, nil
		}
		if m := connectorData.FindStringSubmatch(cb); m != nil {
			return Route{Kind: "command", Name: "connectors", Args: []string{m[1], m[2]}}, nil
		}
		if m := cancelData.FindStringSubmatch(cb); m != nil {
			reply, err := cancelHeld(i, m[1])
			return Route{Kind: "reminder", Reply: reply}, err
		}
		if strings.HasPrefix(cb, "rem:") && i.ReminderActions != nil {
			reply, ok, err := i.ReminderActions.FromCallback(i.DB, cb, i.Chat.ID, i.Now)
			if err != nil {
				return Route{}, err
			}
			if ok {
				return Route{Kind: "reminder", Reply: reply}, nil
			}
		}
		if (strings.HasPrefix(cb, "fl:") || strings.HasPrefix(cb, "flowver:")) && i.FlowActions != nil {
			reply, ok, err := i.FlowActions.FromCallback(i.DB, cb, i.Chat.ID, i.Now)
			if err != nil {
				return Route{}, err
			}
			if ok {
				return Route{Kind: "reminder", Reply: reply}, nil
			}
		}
		return Route{Kind: "ignore", Reason: "Unknown button \"" + cb + "\"."}, nil
	}

	if yes, ok := ParseYesNo(i.Text); ok {
		// Quoting a prompt answers that one, even when it was already answered.
		if i.ReplyToChannelMsgID != "" {
			quoted, err := quotedApproval(i.DB, i.Chat.ID, i.ReplyToChannelMsgID)
			if err != nil {
				return Route{}, err
			}
			if quoted != "" {
				return Route{Kind: "approval", ApprovalID: quoted, Approved: yes}, nil
			}
		}
		// Quoting some other message ("ya" to a reminder, say) is not an approval answer.
		var pending []db.Approval
		if i.ReplyToChannelMsgID == "" {
			var err error
			if pending, err = approvals.PendingForChat(i.DB, i.Chat.ID); err != nil {
				return Route{}, err
			}
		}
		if len(pending) == 1 {
			return Route{Kind: "approval", ApprovalID: pending[0].ID, Approved: yes}, nil
		}
		if len(pending) > 1 {
			newest := pending[len(pending)-1].RunID
			var batch []string
			for _, p := range pending {
				if p.RunID == newest {
					batch = append(batch, p.ID)
				}
			}
			return Route{Kind: "approval", ApprovalID: batch[0], Approved: yes, Batch: batch}, nil
		}
		// "batal" with no approval waiting: a message to a contact still in its undo window.
		if !yes && i.Outbound != nil {
			var held string
			var err error
			if i.ReplyToChannelMsgID != "" {
				held, err = i.Outbound.HeldFromNotice(i.DB, i.Chat.ID, i.ReplyToChannelMsgID)
			} else {
				held, err = i.Outbound.NewestHeld(i.DB, i.Chat.ID, i.Now)
			}
			if err != nil {
				return Route{}, err
			}
			if held != "" {
				reply, err := cancelHeld(i, held)
				return Route{Kind: "reminder", Reply: reply}, err
			}
		}
	}

	if cmd, ok := ParseCommand(i.Text); ok {
		return Route{Kind: "command", Name: cmd.Name, Args: cmd.Args}, nil
	}

	hasText := strx.Trim(i.Text) != ""
	if i.FlowActions != nil && hasText && i.MessageID != "" {
		took, err := i.FlowActions.FromChat(i.DB, ChatTrigger{Text: i.Text, ChatID: i.Chat.ID, MessageID: i.MessageID, FromOwner: true}, i.Now)
		if err != nil {
			return Route{}, err
		}
		if took {
			return Route{Kind: "handled"}, nil
		}
	}
	if i.ReminderActions != nil && hasText {
		reply, ok, err := i.ReminderActions.FromText(i.DB, i.Text, i.ReplyToChannelMsgID, i.Chat.ID, i.Now)
		if err != nil {
			return Route{}, err
		}
		if ok {
			return Route{Kind: "reminder", Reply: reply}, nil
		}
	}
	if i.FlowActions != nil && hasText {
		reply, ok, err := i.FlowActions.FromText(i.DB, i.Text, i.ReplyToChannelMsgID, i.Chat.ID, i.Now)
		if err != nil {
			return Route{}, err
		}
		if ok {
			return Route{Kind: "reminder", Reply: reply}, nil
		}
	}
	if !hasText && len(i.Content.Media) == 0 {
		return Route{Kind: "ignore", Reason: "Empty message."}, nil
	}
	return Route{Kind: "turn"}, nil
}

// cancelHeld: only the chat that asked for a message to a contact can cancel it.
func cancelHeld(i RouteInput, outboundID string) (string, error) {
	if i.Outbound == nil {
		return "Pesan itu nggak ketemu di chat ini.", nil
	}
	ownerChatID, ok, err := i.Outbound.OwnerChatID(i.DB, outboundID)
	if err != nil {
		return "", err
	}
	if !ok || ownerChatID != i.Chat.ID {
		return "Pesan itu nggak ketemu di chat ini.", nil
	}
	return i.Outbound.Cancel(i.DB, outboundID, i.Now)
}

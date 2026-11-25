// Package wainbox is the WhatsApp inbox connector: the owner's personal
// number linked as a companion device (WhatsApp multi-device, on whatsmeow,
// the same runtime as the WhatsApp channel) that never answers. It keeps, per
// chat, only what "waiting for a reply" needs and offers it as read tools, so
// a proactive prompt can mention @<id>__waiting_chats
// (docs/rfc/proactive-prompt.md 10). It's its own connector type, not a mode
// of the channel, so no code path can make it reply in the owner's chats.
package wainbox

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

func phone() *zod.StringS {
	return zod.String().Regex(regexp.MustCompile(`^\+?[\d\s().-]{8,24}$`), `^\+?[\d\s().-]{8,24}$`, "Enter a phone number like 0812 3456 789 or +62 812 3456 789")
}

// ConfigZod: pairPhone is the number being linked (pairing codes are
// requested for it); linkedNumber is learned on connect.
var ConfigZod = zod.Object(
	zod.F("pairPhone", phone().Optional()),
	zod.F("linkedNumber", zod.String().Optional()),
)

var restartOnError = false

var Manifest = connectors.Manifest{
	ID:       "wa-inbox",
	Kind:     "tools",
	Name:     "WhatsApp inbox",
	Icon:     "whatsapp",
	Summary:  "See which of your WhatsApp chats wait for a reply. It never sends anything.",
	Multiple: true,
	Trust:    "external",
	Setup: []connectors.SetupStep{
		{Action: "pairWhatsApp", Label: "Link your WhatsApp number"},
	},
	PauseNote:      "While paused, nothing new is recorded, and the chats waiting stay as they were.",
	RemoveWarning:  "This unlinks the device from your WhatsApp and forgets the chats it recorded. Your chats aren't touched.",
	UnlinkOnRemove: true,
	RestartOnError: &restartOnError,
	Docs: &connectors.Docs{Notes: []string{
		"It links as one more device on your own number and never sends, reacts or marks anything read.",
		"Per chat it keeps the name, when the last message came in, when you last replied and a 200-character preview. Message bodies are never stored.",
		"It starts empty: only messages that arrive after linking count.",
	}},
	ConfigZod:   ConfigZod,
	ParseConfig: kit.ParseConfig(ConfigZod),
}

const (
	// PreviewChars caps a stored preview.
	PreviewChars = 200
	// RetentionMs: chats quiet for longer are forgotten.
	RetentionMs = 30 * 24 * 3600_000
	// groupNameTTL is how long a group's name is reused.
	groupNameTTL = time.Hour
)

// Inbox records one connector's chats.
type Inbox struct {
	DB          db.Q
	ConnectorID string
	Log         *logx.Logger
	Now         func() int64

	mu        sync.Mutex
	groups    map[string]groupName
	lastPrune int64
}

type groupName struct {
	name string
	at   time.Time
}

// self: the linked account's own chat (notes to self), by number or LID.
func self(s whatsapp.Socket, jid string) bool {
	if s == nil {
		return false
	}
	u := s.User()
	if u == nil {
		return false
	}
	bare := whatsapp.BareJID(jid)
	return bare == whatsapp.BareJID(u.ID) || (u.LID != "" && bare == whatsapp.BareJID(u.LID))
}

// preview is what a message shows in the list: its text, or a label for
// media, a contact card, a location or a poll. "" for messages that aren't
// something to reply to (reactions, edits of nothing, protocol messages).
func preview(m *whatsapp.Message) string {
	c := whatsapp.Unwrap(m.Message)
	if c == nil {
		return ""
	}
	t := strx.Trim(whatsapp.TextOf(c))
	if t == "" {
		t = whatsapp.ContentLabel(c)
	}
	if t == "" {
		t = whatsapp.MediaLabel(c)
	}
	t = strings.Join(strings.Fields(t), " ")
	if strx.Len16(t) > PreviewChars {
		t = strx.Slice16(t, PreviewChars-1) + "…"
	}
	return t
}

func (in *Inbox) groupName(ctx context.Context, s whatsapp.Socket, jid string) string {
	in.mu.Lock()
	if in.groups == nil {
		in.groups = map[string]groupName{}
	}
	g, ok := in.groups[jid]
	in.mu.Unlock()
	if ok && time.Since(g.at) < groupNameTTL {
		return g.name
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	name, err := s.GroupSubject(ctx, jid)
	if err != nil {
		return g.name
	}
	in.mu.Lock()
	in.groups[jid] = groupName{name: name, at: time.Now()}
	in.mu.Unlock()
	return name
}

// Observe records one message: a message from someone else starts or
// extends a wait; one from the owner (from any device) ends it.
func (in *Inbox) Observe(s whatsapp.Socket, m *whatsapp.Message) {
	if m == nil {
		return
	}
	jid := whatsapp.BareJID(m.Key.RemoteJID)
	kind := ""
	switch {
	case whatsapp.IsDirectChat(jid):
		kind = "dm"
	case whatsapp.IsGroupChat(jid):
		kind = "group"
	default:
		return // status updates, broadcast lists, newsletters
	}
	if self(s, jid) {
		return
	}
	now := in.Now()
	at := now
	if m.Timestamp > 0 {
		at = m.Timestamp * 1000
	}
	text := preview(m)
	reaction := whatsapp.Unwrap(m.Message).GetReactionMessage() != nil
	if err := in.record(jid, kind, m, text, reaction, at, now, s); err != nil && in.Log != nil {
		in.Log.Warn("wa inbox: could not record a message", logx.Fields{"connector_id": in.ConnectorID, "err": err})
	}
	if now-in.lastPrune > 6*3600_000 {
		in.lastPrune = now
		_, _ = in.DB.Exec("DELETE FROM wa_inbox_chats WHERE connector_id = ? AND updated_at < ?", in.ConnectorID, now-RetentionMs)
	}
}

func (in *Inbox) record(jid, kind string, m *whatsapp.Message, text string, reaction bool, at, now int64, s whatsapp.Socket) error {
	if m.Key.FromMe {
		// Any message or reaction from the owner answers the chat.
		if text == "" && !reaction {
			return nil
		}
		_, err := in.DB.Exec(`INSERT INTO wa_inbox_chats (connector_id, chat_jid, kind, last_out_at, waiting_since, unanswered, updated_at) VALUES (?, ?, ?, ?, NULL, 0, ?)
			ON CONFLICT (connector_id, chat_jid) DO UPDATE SET last_out_at = max(coalesce(last_out_at, 0), excluded.last_out_at), waiting_since = CASE WHEN coalesce(last_in_at, 0) > excluded.last_out_at THEN waiting_since ELSE NULL END, unanswered = CASE WHEN coalesce(last_in_at, 0) > excluded.last_out_at THEN unanswered ELSE 0 END, updated_at = excluded.updated_at`,
			in.ConnectorID, jid, kind, at, now)
		return err
	}
	if text == "" {
		return nil // a reaction or a protocol message isn't something to reply to
	}
	name := strx.Trim(m.PushName)
	if kind == "group" && s != nil {
		name = in.groupName(context.Background(), s, jid)
	}
	_, err := in.DB.Exec(`INSERT INTO wa_inbox_chats (connector_id, chat_jid, kind, name, last_in_at, waiting_since, unanswered, preview, updated_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT (connector_id, chat_jid) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
			waiting_since = CASE WHEN waiting_since IS NULL AND excluded.last_in_at > coalesce(last_out_at, 0) THEN excluded.last_in_at ELSE waiting_since END,
			unanswered = CASE WHEN excluded.last_in_at > coalesce(last_out_at, 0) THEN unanswered + 1 ELSE unanswered END,
			last_in_at = max(coalesce(last_in_at, 0), excluded.last_in_at),
			preview = CASE WHEN excluded.last_in_at >= coalesce(last_in_at, 0) THEN excluded.preview ELSE preview END,
			updated_at = excluded.updated_at`,
		in.ConnectorID, jid, kind, name, at, at, text, now)
	return err
}

// Waiting lists the chats waiting for the owner's reply, longest first.
func Waiting(q db.Q, connectorID string, groups bool, minWaitMs, now int64, limit int) ([]db.WaInboxChat, error) {
	query := "SELECT " + db.WaInboxChatCols + " FROM wa_inbox_chats WHERE connector_id = ? AND waiting_since IS NOT NULL AND waiting_since <= ?"
	if !groups {
		query += " AND kind = 'dm'"
	}
	rows, err := q.Query(query+" ORDER BY waiting_since, chat_jid LIMIT ?", connectorID, now-minWaitMs, limit)
	return db.All(rows, err, db.ScanWaInboxChat)
}

// Recent lists chats by their last activity, newest first.
func Recent(q db.Q, connectorID string, groups bool, limit int) ([]db.WaInboxChat, error) {
	query := "SELECT " + db.WaInboxChatCols + " FROM wa_inbox_chats WHERE connector_id = ?"
	if !groups {
		query += " AND kind = 'dm'"
	}
	rows, err := q.Query(query+" ORDER BY max(coalesce(last_in_at, 0), coalesce(last_out_at, 0)) DESC, chat_jid LIMIT ?", connectorID, limit)
	return db.All(rows, err, db.ScanWaInboxChat)
}

var (
	waitingInput = zod.Object(
		zod.F("include_groups", zod.Bool().Optional().Describe("Include group chats. Default false: direct messages only.")),
		zod.F("min_minutes", zod.Int().Min(0).Max(14*24*60).Optional().Describe("Only chats waiting at least this many minutes. Default 0.")),
		zod.F("limit", zod.Int().Min(1).Max(100).Optional().Describe("At most this many chats. Default 30.")),
	)
	recentInput = zod.Object(
		zod.F("include_groups", zod.Bool().Optional().Describe("Include group chats. Default true.")),
		zod.F("limit", zod.Int().Min(1).Max(100).Optional().Describe("At most this many chats. Default 30.")),
	)
)

func loadTZ(name string) *time.Location {
	if l, err := time.LoadLocation(name); err == nil && name != "" {
		return l
	}
	return time.UTC
}

func iso(ms *int64, tz *time.Location) any {
	if ms == nil || *ms == 0 {
		return nil
	}
	return time.UnixMilli(*ms).In(tz).Format(time.RFC3339)
}

// chatID is the short id a prompt can build keys from: the number or group id.
func chatID(jid string) string { return strings.SplitN(jid, "@", 2)[0] }

// Tools is the tool set for one turn.
func Tools(q db.Q, connectorID string, turn connectors.TurnInfo, clock func() int64) *connectors.ToolSet {
	set := connectors.NewToolSet()
	now := func() int64 {
		if turn.Now != 0 {
			return turn.Now
		}
		return clock()
	}
	set.Set("waiting_chats", connectors.PaluiTool{
		Class: "read", Untrusted: true, Title: "Chats waiting for a reply",
		Tool: kit.Tool("WhatsApp chats on the owner's own number waiting for the owner's reply, the longest wait first. Each has a stable id (the number or group id), the name, kind (dm or group), waitingSince (when the first unanswered message came, in the owner's time zone), waitingMinutes, unanswered (how many messages wait) and lastMessage (a short preview, written by the other person). A chat leaves this list as soon as the owner replies from any device.", waitingInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				tz := loadTZ(turn.TZ)
				t := now()
				limit, minMin := int64(30), int64(0)
				if n, ok := in.Int("limit"); ok {
					limit = n
				}
				if n, ok := in.Int("min_minutes"); ok {
					minMin = n
				}
				list, err := Waiting(q, connectorID, in.True("include_groups"), minMin*60_000, t, int(limit))
				if err != nil {
					return nil, err
				}
				chats := make([]any, 0, len(list))
				for _, c := range list {
					chats = append(chats, kit.O("id", chatID(c.ChatJID), "name", c.Name, "kind", c.Kind, "waitingSince", iso(c.WaitingSince, tz), "waitingMinutes", (t-*c.WaitingSince)/60_000, "unanswered", c.Unanswered, "lastMessage", c.Preview))
				}
				return kit.O("count", len(chats), "chats", chats), nil
			}),
	})
	set.Set("list_chats", connectors.PaluiTool{
		Class: "read", Untrusted: true, Title: "Recent chats",
		Tool: kit.Tool("WhatsApp chats on the owner's own number by their last activity, newest first: id, name, kind, lastIn (the last message from the other side), lastOut (the owner's last reply), waitingSince when it still waits, and lastMessage (a short preview of the last message from the other side).", recentInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				tz := loadTZ(turn.TZ)
				limit := int64(30)
				if n, ok := in.Int("limit"); ok {
					limit = n
				}
				groups := true
				if b, ok := in.Bool("include_groups"); ok {
					groups = b
				}
				list, err := Recent(q, connectorID, groups, int(limit))
				if err != nil {
					return nil, err
				}
				chats := make([]any, 0, len(list))
				for _, c := range list {
					o := kit.O("id", chatID(c.ChatJID), "name", c.Name, "kind", c.Kind, "lastIn", iso(c.LastInAt, tz), "lastOut", iso(c.LastOutAt, tz))
					if c.WaitingSince != nil {
						o.Set("waitingSince", iso(c.WaitingSince, tz))
						o.Set("unanswered", c.Unanswered)
					}
					o.Set("lastMessage", c.Preview)
					chats = append(chats, o)
				}
				return kit.O("count", len(chats), "chats", chats), nil
			}),
	})
	return set
}

// Options is the runtime's injectable plumbing: the WhatsApp runtime's (a
// fake socket in tests) and a clock.
type Options struct {
	WhatsApp whatsapp.Options
	Clock    func() int64
}

// New is the runtime: the WhatsApp runtime, read only, under this manifest,
// with an observer that records the inbox.
func New(o Options) *connectors.Runtime {
	clock := o.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	base := func(observe func(whatsapp.Socket, *whatsapp.Message)) *connectors.Runtime {
		wo := o.WhatsApp
		m := Manifest
		wo.Manifest = &m
		wo.ReadOnly = true
		wo.Observe = observe
		return whatsapp.New(wo)
	}
	rt := base(nil)
	return &connectors.Runtime{
		Manifest: Manifest,
		Test:     rt.Test,
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			inbox := &Inbox{DB: c.DB, ConnectorID: c.ID, Log: c.Log, Now: clock}
			running, err := base(inbox.Observe).Start(c)
			if err != nil {
				return nil, err
			}
			running.Tools = func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				return Tools(c.DB, c.ID, turn, clock), nil
			}
			return running, nil
		},
	}
}

// Runtime is the WhatsApp inbox connector runtime.
var Runtime = New(Options{})

// RemoveChats forgets a removed inbox's chats (composed into the supervisor's
// OnRemoved next to the session wipe).
func RemoveChats(q db.Q) func(row db.Connector, unlinked bool) error {
	return func(row db.Connector, _ bool) error {
		if row.Type != Manifest.ID {
			return nil
		}
		_, err := q.Exec("DELETE FROM wa_inbox_chats WHERE connector_id = ?", row.ID)
		return err
	}
}

package agents

// The rest of agents.ts (PLAN 19): lookups, memory containers, validation,
// create, update, tool permissions, delete and chat assignment.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

var (
	AgentSlug       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	RecallContainer = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	AvatarImage     = regexp.MustCompile(`^data:image/(png|jpeg|webp);base64,[A-Za-z0-9+/]+=*$`)
)

const (
	MaxAgentName      = 40
	MaxPersona        = 8000
	MaxAgents         = 20
	MaxReadContainers = 5
	// A browser-resized photo is a few KB; this keeps a pasted original out.
	MaxAvatarImage = 96 * 1024
)

// AgentIcons and AgentColors are the avatar choices (names only).
var AgentIcons = []string{"bot", "sparkles", "smile", "heart", "star", "chef-hat", "coffee", "briefcase", "headset", "store", "home", "book-open", "graduation-cap", "code", "wrench", "calculator", "scale", "stethoscope", "dumbbell", "leaf", "music", "camera", "plane", "moon"}
var AgentColors = []string{"zinc", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"}

// Error carries a message for the owner (AgentError).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// List is listAgents: the default first, then by creation.
func List(q db.Q, now int64) ([]db.Agent, error) {
	if _, err := GetDefault(q, now); err != nil {
		return nil, err
	}
	rows, err := q.Query("SELECT " + db.AgentCols + " FROM agents ORDER BY is_default DESC, created_at ASC, id ASC")
	return db.All(rows, err, db.ScanAgent)
}

func GetBySlug(q db.Q, slug string) (db.Agent, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.AgentCols+" FROM agents WHERE slug = ?", strings.ToLower(strx.Trim(slug))), db.ScanAgent)
}

func ForTelegramConnector(q db.Q, connectorID string) (db.Agent, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.AgentCols+" FROM agents WHERE telegram_connector_id = ?", connectorID), db.ScanAgent)
}

// ForChat is the agent that answers in this chat; a chat whose agent was
// deleted falls back to the default.
func ForChat(q db.Q, chatAgentID *string, now int64) (db.Agent, error) {
	if chatAgentID != nil && *chatAgentID != "" {
		a, ok, err := Get(q, *chatAgentID)
		if err != nil || ok {
			return a, err
		}
	}
	return GetDefault(q, now)
}

// ServesChat is ForChat(...).ID == agentID.
func ServesChat(q db.Q, agentID string, chatAgentID *string, now int64) (bool, error) {
	a, err := ForChat(q, chatAgentID, now)
	return err == nil && a.ID == agentID, err
}

// ChatsServedBy is the SQL filter (on table `chats`) for the chats this agent
// answers in, matching ServesChat.
func ChatsServedBy(q db.Q, agentID string) (string, []any, error) {
	a, ok, err := Get(q, agentID)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "0", nil, nil
	}
	if !a.IsDefault {
		return `"chats"."agent_id" = ?`, []any{a.ID}, nil
	}
	return `("chats"."agent_id" is null or "chats"."agent_id" = ? or "chats"."agent_id" not in (select "id" from "agents"))`, []any{a.ID}, nil
}

// Container is agentContainer: where the agent's memory lives; nil means the
// recall connector's own. With recallPerChat and a chat, each chat gets its own.
func Container(a db.Agent, chatID string) *string {
	var base *string
	if a.IsDefault {
		base = a.RecallContainer
	} else if a.RecallContainer != nil {
		base = a.RecallContainer
	} else {
		s := "agent-" + a.Slug
		base = &s
	}
	if !a.RecallPerChat || chatID == "" || base == nil || *base == "" {
		return base
	}
	c := ChatContainer(*base, chatID)
	return &c
}

// ChatContainer is <base>-c<10 hex>, at most 64 characters.
func ChatContainer(base, chatID string) string {
	sum := sha256.Sum256([]byte(chatID))
	return ChatContainerPrefix(base) + hex.EncodeToString(sum[:])[:10]
}

var trailingDash = regexp.MustCompile(`[-_]+$`)

// ChatContainerPrefix is what every per-chat container of this base starts with.
func ChatContainerPrefix(base string) string {
	return trailingDash.ReplaceAllString(strx.Slice16(base, 64-12), "") + "-c"
}

// ReadContainers are the containers it reads on top of its own.
func ReadContainers(a db.Agent) []string {
	out := []string{}
	if !a.RecallReadContainers.Valid {
		return out
	}
	for _, c := range a.RecallReadContainers.V {
		if RecallContainer.MatchString(c) {
			out = append(out, c)
		}
	}
	return out
}

// MemoryScope is what a turn of this agent may touch in recall (PLAN 19.5).
type MemoryScope struct {
	Container *string  `json:"container"`
	Read      []string `json:"read"`
	Locked    bool     `json:"locked"`
}

func Scope(a db.Agent, chatID string) MemoryScope {
	return MemoryScope{Container: Container(a, chatID), Read: ReadContainers(a), Locked: true}
}

// OwnerContainers is the owner's own memory: the memory connector's container
// (or recall's default and the learned default name) and the default agent's
// override.
func OwnerContainers(q db.Q) ([]string, error) {
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	rows, err := q.Query("SELECT config FROM connectors WHERE kind = 'memory'")
	if err != nil {
		return nil, err
	}
	var configs []string
	for rows.Next() {
		var c *string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		if c != nil {
			configs = append(configs, *c)
		} else {
			configs = append(configs, "{}")
		}
	}
	rows.Close()
	for _, raw := range configs {
		cfg, _ := jsonx.ParseObject([]byte(raw))
		if cfg == nil {
			cfg = jsonx.NewMap[any]()
		}
		if v, _ := cfg.Get("container"); v != nil {
			if s, ok := v.(string); ok && s != "" {
				add(s)
				continue
			}
		}
		add("default")
		if v, _ := cfg.Get("defaultContainer"); v != nil {
			if s, ok := v.(string); ok && s != "" {
				add(s)
			}
		}
	}
	var def *string
	err = q.QueryRow("SELECT recall_container FROM agents WHERE is_default = 1 LIMIT 1").Scan(&def)
	if err == nil && def != nil && *def != "" {
		add(*def)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// IsSharedChat: a group, or a DM with someone who isn't the owner (PLAN 19.2).
func IsSharedChat(q db.Q, chat db.Chat) (bool, error) {
	if chat.Channel == db.ChannelWeb {
		return false, nil
	}
	if chat.Kind == "group" {
		return true, nil
	}
	isOwner, err := owner.IsOwner(q, owner.Sender{Channel: chat.Channel, ExternalID: chat.ExternalID})
	return !isOwner, err
}

// Input is AgentInput. For the optional *string fields "" means null (cleared).
// RecallReadContainers: nil leaves it, a pointer to an empty or nil slice clears it.
type Input struct {
	Slug                 string
	Name                 *string
	Persona              *string
	GuestTools           *string
	DailyTokenCap        *int64
	RecallContainer      *string
	RecallReadContainers *[]string
	RecallPerChat        *bool
	TelegramConnectorID  *string
	AvatarImage          *string
	AvatarIcon           *string
	AvatarColor          *string
}

func readList(list []string) []string {
	var out []string
	for _, c := range list {
		c = strings.ToLower(strx.Trim(c))
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

var crlf = regexp.MustCompile(`\r\n?`)
var manyNewlines = regexp.MustCompile(`\n{3,}`)
var newlines = regexp.MustCompile(`\n+`)

func clean(text string) string {
	return strx.Trim(manyNewlines.ReplaceAllString(crlf.ReplaceAllString(text, "\n"), "\n\n"))
}

func nonEmpty(p *string) bool { return p != nil && *p != "" }

func checkFields(q db.Q, i Input, selfID string, isDefault bool) error {
	if i.Name != nil {
		if *i.Name == "" {
			return errf("Name is empty.")
		}
		if strx.Len16(*i.Name) > MaxAgentName {
			return errf("Name is at most %d characters.", MaxAgentName)
		}
	}
	if i.Persona != nil {
		if *i.Persona == "" {
			return errf("Persona is empty.")
		}
		if strx.Len16(*i.Persona) > MaxPersona {
			return errf("Persona is at most %d characters.", MaxPersona)
		}
	}
	if i.GuestTools != nil && *i.GuestTools != "none" && *i.GuestTools != "safe" {
		return errf("Guest tools must be none or safe.")
	}
	if i.DailyTokenCap != nil && *i.DailyTokenCap < 0 {
		return errf("Daily cap must be a whole number, 0 or more.")
	}
	if nonEmpty(i.RecallContainer) && !RecallContainer.MatchString(*i.RecallContainer) {
		return errf("Recall container uses lowercase letters, digits, - and _.")
	}
	if i.RecallReadContainers != nil && *i.RecallReadContainers != nil {
		list := *i.RecallReadContainers
		if len(list) > MaxReadContainers {
			return errf("At most %d shared containers.", MaxReadContainers)
		}
		for _, c := range list {
			if !RecallContainer.MatchString(c) {
				return errf("%s isn't a recall container name: lowercase letters, digits, - and _.", strx.Slice16(c, 64))
			}
		}
	}
	// The owner's memory stays Palui's (PLAN 19.1).
	if !isDefault {
		owned, err := OwnerContainers(q)
		if err != nil {
			return err
		}
		if nonEmpty(i.RecallContainer) && slices.Contains(owned, *i.RecallContainer) {
			return errf("%s is the owner's memory. Only Palui uses it.", *i.RecallContainer)
		}
		if i.RecallReadContainers != nil {
			for _, c := range *i.RecallReadContainers {
				if slices.Contains(owned, c) {
					return errf("%s is the owner's memory. Only Palui reads it.", c)
				}
			}
		}
	}
	if nonEmpty(i.AvatarImage) {
		if strx.Len16(*i.AvatarImage) > MaxAvatarImage {
			return errf("That photo is too big. Pick a smaller one.")
		}
		if !AvatarImage.MatchString(*i.AvatarImage) {
			return errf("The photo must be a PNG, JPEG or WebP image.")
		}
	}
	if nonEmpty(i.AvatarIcon) && !slices.Contains(AgentIcons, *i.AvatarIcon) {
		return errf("Unknown avatar icon.")
	}
	if nonEmpty(i.AvatarColor) && !slices.Contains(AgentColors, *i.AvatarColor) {
		return errf("Unknown avatar color.")
	}
	if nonEmpty(i.TelegramConnectorID) {
		taken, ok, err := ForTelegramConnector(q, *i.TelegramConnectorID)
		if err != nil {
			return err
		}
		if ok && taken.ID != selfID {
			return errf("That Telegram bot already belongs to %s.", taken.Name)
		}
	}
	return nil
}

func orNull(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}

func readListPtr(p *[]string) *[]string {
	if p == nil {
		return nil
	}
	l := readList(*p)
	return &l
}

func listValue(p *[]string) db.NullJSON[[]string] {
	if p == nil || len(*p) == 0 {
		return db.NullJSON[[]string]{}
	}
	return db.SomeJSON(*p)
}

func str(s string) *string { return &s }

// Create is createAgent.
func Create(q db.Q, in Input, now int64) (db.Agent, error) {
	slug := strings.ToLower(strx.Trim(in.Slug))
	if !AgentSlug.MatchString(slug) {
		return db.Agent{}, errf("Slug is 2 to 24 characters: lowercase letters, digits and -, starting with a letter.")
	}
	if _, ok, err := GetBySlug(q, slug); err != nil || ok {
		if err != nil {
			return db.Agent{}, err
		}
		return db.Agent{}, errf("There's already an agent called %s.", slug)
	}
	f := in
	name, persona := "", ""
	if in.Name != nil {
		name = newlines.ReplaceAllString(clean(*in.Name), " ")
	}
	if in.Persona != nil {
		persona = clean(*in.Persona)
	}
	f.Name, f.Persona = &name, &persona
	f.RecallReadContainers = readListPtr(in.RecallReadContainers)
	if err := checkFields(q, f, "", false); err != nil {
		return db.Agent{}, err
	}
	all, err := List(q, now)
	if err != nil {
		return db.Agent{}, err
	}
	if len(all) >= MaxAgents {
		return db.Agent{}, errf("At most %d agents.", MaxAgents)
	}
	guestTools := "safe"
	if f.GuestTools != nil {
		guestTools = *f.GuestTools
	}
	var cap int64
	if f.DailyTokenCap != nil {
		cap = *f.DailyTokenCap
	}
	perChat := f.RecallPerChat != nil && *f.RecallPerChat
	return db.ScanAgent(q.WQueryRow("INSERT INTO agents (id, slug, name, persona, guest_tools, daily_token_cap, recall_container, recall_read_containers, recall_per_chat, telegram_connector_id, avatar_image, avatar_icon, avatar_color, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.AgentCols,
		secret.NewID("agt"), slug, name, persona, guestTools, cap, orNull(f.RecallContainer), listValue(f.RecallReadContainers), db.Bool(perChat), orNull(f.TelegramConnectorID), orNull(f.AvatarImage), orNull(f.AvatarIcon), orNull(f.AvatarColor), now, now))
}

// Update is updateAgent (Slug is ignored).
func Update(q db.Q, id string, patch Input, now int64) (db.Agent, error) {
	row, ok, err := Get(q, id)
	if err != nil {
		return db.Agent{}, err
	}
	if !ok {
		return db.Agent{}, errf("No agent %s.", id)
	}
	f := patch
	if patch.Name != nil {
		f.Name = str(newlines.ReplaceAllString(clean(*patch.Name), " "))
	}
	if patch.Persona != nil {
		f.Persona = str(clean(*patch.Persona))
	}
	f.RecallReadContainers = readListPtr(patch.RecallReadContainers)
	// The default agent never runs in shared chats: no shared-chat rules or bot of its own.
	if row.IsDefault {
		f.GuestTools, f.TelegramConnectorID = nil, nil
	}
	if err := checkFields(q, f, id, row.IsDefault); err != nil {
		return db.Agent{}, err
	}
	sets := []string{"updated_at = ?"}
	args := []any{now}
	set := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if f.Name != nil {
		set("name", *f.Name)
	}
	if f.Persona != nil {
		set("persona", *f.Persona)
	}
	if f.GuestTools != nil {
		set("guest_tools", *f.GuestTools)
	}
	if f.DailyTokenCap != nil {
		set("daily_token_cap", *f.DailyTokenCap)
	}
	if f.RecallContainer != nil {
		set("recall_container", orNull(f.RecallContainer))
	}
	if f.RecallReadContainers != nil {
		set("recall_read_containers", listValue(f.RecallReadContainers))
	}
	if f.RecallPerChat != nil {
		set("recall_per_chat", db.Bool(*f.RecallPerChat))
	}
	if f.TelegramConnectorID != nil {
		set("telegram_connector_id", orNull(f.TelegramConnectorID))
	}
	if f.AvatarImage != nil {
		set("avatar_image", orNull(f.AvatarImage))
	}
	if f.AvatarIcon != nil {
		set("avatar_icon", orNull(f.AvatarIcon))
	}
	if f.AvatarColor != nil {
		set("avatar_color", orNull(f.AvatarColor))
	}
	args = append(args, id)
	return db.ScanAgent(q.WQueryRow("UPDATE agents SET "+strings.Join(sets, ", ")+" WHERE id = ? RETURNING "+db.AgentCols, args...))
}

// PermissionsPatch is ToolPermissionsPatch: AllowAll changes the default;
// Tools sets one tool each, a nil value putting it back to the default.
type PermissionsPatch struct {
	AllowAll *bool
	Tools    *jsonx.Map[*string]
}

// SetToolPermissions is setToolPermissions.
func SetToolPermissions(q db.Q, id string, patch PermissionsPatch, now int64) (db.Agent, error) {
	row, ok, err := Get(q, id)
	if err != nil {
		return db.Agent{}, err
	}
	if !ok {
		return db.Agent{}, errf("No agent %s.", id)
	}
	next := tools.NoPermissions()
	if row.ToolPermissions.Valid {
		next = tools.ReadPermissions([]byte(jsonx.Stringify(row.ToolPermissions.V)))
	}
	ts := next.Tools.Clone()
	if patch.Tools != nil {
		for _, name := range patch.Tools.Keys() {
			p, _ := patch.Tools.Get(name)
			if !tools.ToolName.MatchString(name) {
				return db.Agent{}, errf("Unknown tool name %s.", name)
			}
			switch {
			case p == nil:
				ts.Delete(name)
			case slices.Contains(tools.Permissions, *p):
				ts.Set(name, *p)
			default:
				return db.Agent{}, errf("A tool permission is allow, ask or deny.")
			}
		}
	}
	if ts.Len() > tools.MaxToolPermissions {
		return db.Agent{}, errf("At most %d tool permissions.", tools.MaxToolPermissions)
	}
	allowAll := next.AllowAll
	if patch.AllowAll != nil {
		allowAll = *patch.AllowAll
	}
	value := db.NullJSON[db.ToolPermissions]{}
	if allowAll || ts.Len() > 0 {
		value = db.SomeJSON(db.ToolPermissions{AllowAll: allowAll, Tools: ts})
	}
	return db.ScanAgent(q.WQueryRow("UPDATE agents SET tool_permissions = ?, updated_at = ? WHERE id = ? RETURNING "+db.AgentCols, value, now, id))
}

// Delete is deleteAgent: its chats go back to the default agent, connectors it
// owned pause (keeping the dead owner, so they serve nobody), its instructions
// go, its contacts are removed, nothing waiting is sent on its behalf, and
// members who were only in this agent are deleted.
func Delete(q db.Q, id string, now int64) (bool, error) {
	row, ok, err := Get(q, id)
	if err != nil || !ok {
		return false, err
	}
	if row.IsDefault {
		return false, errf("Palui is the default agent and can't be deleted.")
	}
	err = q.Tx(func(tx db.Q) error {
		stmts := []struct {
			sql  string
			args []any
		}{
			{"UPDATE chats SET agent_id = NULL WHERE agent_id = ?", []any{id}},
			{"UPDATE connectors SET enabled = 0 WHERE agent_id = ?", []any{id}},
			{"DELETE FROM instructions WHERE agent_id = ?", []any{id}},
			{"UPDATE contacts SET removed_at = ? WHERE agent_id = ? AND removed_at IS NULL", []any{now, id}},
			// Members who were only in this agent go with it (PLAN 19.4). The
			// first two read agent_members before the third clears it.
			{"DELETE FROM member_accounts WHERE member_id IN (SELECT member_id FROM agent_members WHERE agent_id = ?) AND member_id NOT IN (SELECT member_id FROM agent_members WHERE agent_id != ?)", []any{id, id}},
			{"DELETE FROM members WHERE id IN (SELECT member_id FROM agent_members WHERE agent_id = ?) AND id NOT IN (SELECT member_id FROM agent_members WHERE agent_id != ?)", []any{id, id}},
			{"DELETE FROM agent_members WHERE agent_id = ?", []any{id}},
		}
		for _, s := range stmts {
			if _, err := tx.Exec(s.sql, s.args...); err != nil {
				return err
			}
		}
		rows, err := tx.WQuery("SELECT id FROM outbound_messages WHERE agent_id = ?", id)
		if err != nil {
			return err
		}
		var waiting []any
		for rows.Next() {
			var oid string
			if err := rows.Scan(&oid); err != nil {
				rows.Close()
				return err
			}
			waiting = append(waiting, "outbound:"+oid)
		}
		rows.Close()
		if _, err := tx.Exec("UPDATE outbound_messages SET status = 'cancelled', updated_at = ? WHERE agent_id = ? AND status IN ('scheduled', 'firing')", now, id); err != nil {
			return err
		}
		if len(waiting) > 0 {
			marks := strings.TrimSuffix(strings.Repeat("?, ", len(waiting)), ", ")
			if _, err := tx.Exec("UPDATE outbox SET status = 'cancelled', error = 'agent deleted', claimed_at = NULL WHERE source IN ("+marks+") AND status = 'pending'", waiting...); err != nil {
				return err
			}
		}
		_, err = tx.Exec("DELETE FROM agents WHERE id = ?", id)
		return err
	})
	return err == nil, err
}

// Assign is assignAgent: give a chat an agent (nil: the default) and turn the
// chat on. A chat that came in on an agent's own bot keeps that agent.
func Assign(q db.Q, chatID string, agentID *string, now int64) (db.Chat, db.Agent, error) {
	chat, ok, err := db.One(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE id = ?", chatID), db.ScanChat)
	if err != nil {
		return db.Chat{}, db.Agent{}, err
	}
	if !ok {
		return db.Chat{}, db.Agent{}, errf("No chat %s.", chatID)
	}
	var agent db.Agent
	if agentID != nil {
		a, found, err := Get(q, *agentID)
		if err != nil {
			return db.Chat{}, db.Agent{}, err
		}
		if !found {
			return db.Chat{}, db.Agent{}, errf("No agent %s.", *agentID)
		}
		agent = a
	} else if agent, err = GetDefault(q, now); err != nil {
		return db.Chat{}, db.Agent{}, err
	}
	if chat.ConnectorID != "" && (agent.TelegramConnectorID == nil || *agent.TelegramConnectorID != chat.ConnectorID) {
		return db.Chat{}, db.Agent{}, errf("This chat came in on another agent's bot, so its agent is fixed.")
	}
	var newAgent *string
	if !agent.IsDefault {
		newAgent = &agent.ID
	}
	updated, err := db.ScanChat(q.WQueryRow("UPDATE chats SET agent_id = ?, allowed = 1 WHERE id = ? RETURNING "+db.ChatCols, newAgent, chatID))
	return updated, agent, err
}

// Package members stores the people the owner shares an agent with (PLAN
// 19.4). A member has one or more channel accounts (member_accounts) and is
// linked to the agents they share (agent_members). Member accounts never live
// in identities: every query there assumes the row is the owner.
package members

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	// MaxName is in UTF-16 units.
	MaxName     = 60
	MaxAccounts = 5
	MaxPerAgent = 30
)

// Sender is one account as a channel reports it. AltID is the other form on
// WhatsApp (LID or phone JID), "" when unknown.
type Sender struct{ Channel, ExternalID, AltID string }

// AccountInput is an account as the owner typed or picked it.
type AccountInput struct{ Channel, Address, DisplayName string }

// View is a member with their accounts and the agents they are in.
type View struct {
	db.Member
	Accounts []db.MemberAccount
	AgentIDs []string
}

// Error carries a message for the owner, like agents.Error.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

var (
	waJID      = regexp.MustCompile(`^\d{5,20}@(s\.whatsapp\.net|lid)$`)
	telegramID = regexp.MustCompile(`^\d{1,20}$`)
)

// NormalizeAccount turns what the owner typed or picked into the external id
// the channel reports: a bare WhatsApp JID (phone or LID) or a Telegram user id.
func NormalizeAccount(channel, address string) (string, error) {
	address = strx.Trim(address)
	switch channel {
	case db.ChannelWhatsApp:
		if waJID.MatchString(address) {
			return address, nil
		}
		if strings.Contains(address, "@") {
			return "", errf("That isn't a WhatsApp number.")
		}
		jid := owner.PhoneToJid(address)
		digits := strings.TrimSuffix(jid, "@s.whatsapp.net")
		if len(digits) < 8 || len(digits) > 15 {
			return "", errf("That isn't a WhatsApp number.")
		}
		return jid, nil
	case db.ChannelTelegram:
		if !telegramID.MatchString(address) {
			return "", errf("A Telegram account is its numeric user id.")
		}
		return address, nil
	default:
		return "", errf("Members can only have WhatsApp or Telegram accounts.")
	}
}

type account struct {
	channel, externalID string
	displayName         *string
}

func cleanName(name string) (string, error) {
	name = strx.Trim(strings.ReplaceAll(strx.NormalizeNewlines(name), "\n", " "))
	if name == "" {
		return "", errf("Give the member a name.")
	}
	if strx.Len16(name) > MaxName {
		return "", errf("A member's name is at most %d characters.", MaxName)
	}
	return name, nil
}

// checkAccounts normalizes the accounts and refuses the owner's own account
// and accounts another member holds. selfID is the member being saved ("" for
// a new one).
func checkAccounts(q db.Q, in []AccountInput, selfID string) ([]account, error) {
	if len(in) == 0 {
		return nil, errf("Add at least one account.")
	}
	if len(in) > MaxAccounts {
		return nil, errf("A member has at most %d accounts.", MaxAccounts)
	}
	var out []account
	seen := map[string]bool{}
	for _, a := range in {
		ext, err := NormalizeAccount(a.Channel, a.Address)
		if err != nil {
			return nil, err
		}
		key := a.Channel + "\x00" + ext
		if seen[key] {
			return nil, errf("That account is listed twice.")
		}
		seen[key] = true
		isOwner, err := owner.IsOwner(q, owner.Sender{Channel: a.Channel, ExternalID: ext})
		if err != nil {
			return nil, err
		}
		if isOwner {
			return nil, errf("That's your own account.")
		}
		var holder string
		err = q.QueryRow("SELECT m.name FROM member_accounts a JOIN members m ON m.id = a.member_id WHERE a.channel = ? AND (a.external_id = ? OR a.alt_id = ?) AND a.member_id != ? LIMIT 1", a.Channel, ext, ext, selfID).Scan(&holder)
		if err == nil {
			return nil, errf("That account already belongs to %s.", holder)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var dn *string
		if v := strx.Trim(a.DisplayName); v != "" {
			dn = &v
		}
		out = append(out, account{channel: a.Channel, externalID: ext, displayName: dn})
	}
	return out, nil
}

// Create adds a member with their accounts. It doesn't link them to an agent.
func Create(q db.Q, name string, accounts []AccountInput, now int64) (View, error) {
	var out View
	err := q.Tx(func(tx db.Q) error {
		name, err := cleanName(name)
		if err != nil {
			return err
		}
		list, err := checkAccounts(tx, accounts, "")
		if err != nil {
			return err
		}
		id := secret.NewID("mem")
		if _, err := tx.Exec("INSERT INTO members (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)", id, name, now, now); err != nil {
			return err
		}
		for _, a := range list {
			if err := insertAccount(tx, id, a, now); err != nil {
				return err
			}
		}
		out, err = get(tx, id)
		return err
	})
	return out, err
}

func insertAccount(q db.Q, memberID string, a account, now int64) error {
	_, err := q.Exec("INSERT INTO member_accounts (id, member_id, channel, external_id, display_name, created_at) VALUES (?, ?, ?, ?, ?, ?)", secret.NewID("mac"), memberID, a.channel, a.externalID, a.displayName, now)
	return err
}

// Update renames a member and replaces their accounts. An account whose
// channel and external id are unchanged keeps its row, so its learned alt id
// survives.
func Update(q db.Q, id, name string, accounts []AccountInput, now int64) (View, error) {
	var out View
	err := q.Tx(func(tx db.Q) error {
		if _, ok, err := db.One(tx.QueryRow("SELECT "+db.MemberCols+" FROM members WHERE id = ?", id), db.ScanMember); err != nil {
			return err
		} else if !ok {
			return errf("That member is gone.")
		}
		name, err := cleanName(name)
		if err != nil {
			return err
		}
		list, err := checkAccounts(tx, accounts, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE members SET name = ?, updated_at = ? WHERE id = ?", name, now, id); err != nil {
			return err
		}
		current, err := accountsOf(tx, id)
		if err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, a := range list {
			found := false
			for _, c := range current {
				if c.Channel == a.channel && c.ExternalID == a.externalID {
					found = true
					keep[c.ID] = true
					if a.displayName != nil {
						if _, err := tx.Exec("UPDATE member_accounts SET display_name = ? WHERE id = ?", *a.displayName, c.ID); err != nil {
							return err
						}
					}
				}
			}
			if !found {
				if err := insertAccount(tx, id, a, now); err != nil {
					return err
				}
			}
		}
		for _, c := range current {
			if !keep[c.ID] {
				if _, err := tx.Exec("DELETE FROM member_accounts WHERE id = ?", c.ID); err != nil {
					return err
				}
			}
		}
		out, err = get(tx, id)
		return err
	})
	return out, err
}

// Link adds a member to an agent. The default agent has no members.
func Link(q db.Q, agentID, memberID string, now int64) error {
	return q.Tx(func(tx db.Q) error {
		agent, ok, err := agents.Get(tx, agentID)
		if err != nil {
			return err
		}
		if !ok {
			return errf("That agent is gone.")
		}
		if agent.IsDefault {
			return errf("Palui is yours alone and has no members.")
		}
		if _, ok, err := db.One(tx.QueryRow("SELECT "+db.MemberCols+" FROM members WHERE id = ?", memberID), db.ScanMember); err != nil {
			return err
		} else if !ok {
			return errf("That member is gone.")
		}
		var linked, n int
		if err := tx.QueryRow("SELECT count(*), coalesce(sum(member_id = ?), 0) FROM agent_members WHERE agent_id = ?", memberID, agentID).Scan(&n, &linked); err != nil {
			return err
		}
		if linked > 0 {
			return nil
		}
		if n >= MaxPerAgent {
			return errf("An agent has at most %d members.", MaxPerAgent)
		}
		_, err = tx.Exec("INSERT OR IGNORE INTO agent_members (agent_id, member_id, created_at) VALUES (?, ?, ?)", agentID, memberID, now)
		return err
	})
}

// Unlink removes a member from an agent. When that was their last agent, the
// member and their accounts are deleted too, and deleted is true.
func Unlink(q db.Q, agentID, memberID string) (bool, error) {
	deleted := false
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("DELETE FROM agent_members WHERE agent_id = ? AND member_id = ?", agentID, memberID); err != nil {
			return err
		}
		var left int
		if err := tx.QueryRow("SELECT count(*) FROM agent_members WHERE member_id = ?", memberID).Scan(&left); err != nil {
			return err
		}
		if left > 0 {
			return nil
		}
		if _, err := tx.Exec("DELETE FROM member_accounts WHERE member_id = ?", memberID); err != nil {
			return err
		}
		n, err := db.Changes(tx.Exec("DELETE FROM members WHERE id = ?", memberID))
		deleted = n > 0
		return err
	})
	return deleted, err
}

func accountsOf(q db.Q, memberID string) ([]db.MemberAccount, error) {
	rows, err := q.Query("SELECT "+db.MemberAccountCols+" FROM member_accounts WHERE member_id = ? ORDER BY created_at, rowid", memberID)
	list, err := db.All(rows, err, db.ScanMemberAccount)
	if list == nil {
		list = []db.MemberAccount{}
	}
	return list, err
}

func agentsOf(q db.Q, memberID string) ([]string, error) {
	rows, err := q.Query("SELECT agent_id FROM agent_members WHERE member_id = ? ORDER BY created_at, agent_id", memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func get(q db.Q, id string) (View, error) {
	m, ok, err := db.One(q.QueryRow("SELECT "+db.MemberCols+" FROM members WHERE id = ?", id), db.ScanMember)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, errf("That member is gone.")
	}
	return view(q, m)
}

func view(q db.Q, m db.Member) (View, error) {
	acc, err := accountsOf(q, m.ID)
	if err != nil {
		return View{}, err
	}
	ids, err := agentsOf(q, m.ID)
	if err != nil {
		return View{}, err
	}
	return View{Member: m, Accounts: acc, AgentIDs: ids}, nil
}

func views(q db.Q, query string, args ...any) ([]View, error) {
	rows, err := q.Query(query, args...)
	list, err := db.All(rows, err, db.ScanMember)
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, m := range list {
		v, err := view(q, m)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ForAgent lists an agent's members by name.
func ForAgent(q db.Q, agentID string) ([]View, error) {
	return views(q, "SELECT "+prefixed("m")+" FROM members m JOIN agent_members am ON am.member_id = m.id WHERE am.agent_id = ? ORDER BY m.name, m.id", agentID)
}

// All lists every member by name.
func All(q db.Q) ([]View, error) {
	return views(q, "SELECT "+db.MemberCols+" FROM members ORDER BY name, id")
}

func prefixed(alias string) string {
	cols := strings.Split(db.MemberCols, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// HasAny reports whether the agent has at least one member.
func HasAny(q db.Q, agentID string) (bool, error) {
	var n int
	err := q.QueryRow("SELECT count(*) FROM (SELECT 1 FROM agent_members WHERE agent_id = ? LIMIT 1)", agentID).Scan(&n)
	return n > 0, err
}

func senderIDs(s Sender) []any {
	var ids []any
	for _, id := range []string{s.ExternalID, s.AltID} {
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// IsMember reports whether the sender is a member of this agent, matching any
// of their ids on an account's external id or learned alt id. Read only.
func IsMember(q db.Q, agentID string, s Sender) (bool, error) {
	if s.Channel == db.ChannelWeb || s.Channel == "" {
		return false, nil
	}
	ids := senderIDs(s)
	if len(ids) == 0 {
		return false, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := []any{agentID, s.Channel}
	args = append(args, ids...)
	args = append(args, ids...)
	var one int
	err := q.QueryRow("SELECT 1 FROM member_accounts a JOIN agent_members am ON am.member_id = a.member_id WHERE am.agent_id = ? AND a.channel = ? AND (a.external_id IN ("+marks+") OR a.alt_id IN ("+marks+")) LIMIT 1", args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Learn pairs a WhatsApp account with its other id (LID or phone JID) the
// first time a message carries both: a typed phone learns its LID, a picked
// LID learns its phone. It never writes when the sender has no account, when
// the account already has an alt id, or when another account already holds
// the other id.
func Learn(q db.Q, s Sender) error {
	if s.Channel != db.ChannelWhatsApp || s.ExternalID == "" || s.AltID == "" || s.ExternalID == s.AltID {
		return nil
	}
	for _, pair := range [][2]string{{s.ExternalID, s.AltID}, {s.AltID, s.ExternalID}} {
		have, other := pair[0], pair[1]
		rows, err := q.Query("SELECT "+db.MemberAccountCols+" FROM member_accounts WHERE channel = ? AND external_id = ?", s.Channel, have)
		list, err := db.All(rows, err, db.ScanMemberAccount)
		if err != nil {
			return err
		}
		if len(list) != 1 {
			continue
		}
		if list[0].AltID != nil {
			return nil
		}
		var n int
		if err := q.QueryRow("SELECT count(*) FROM member_accounts WHERE channel = ? AND (external_id = ? OR alt_id = ?)", s.Channel, other, other).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		_, err = q.Exec("UPDATE member_accounts SET alt_id = ? WHERE id = ? AND alt_id IS NULL", other, list[0].ID)
		return err
	}
	return nil
}

// SetRules turns on or off whether a member may add and remove chat rules
// for this agent from a shared chat. Off by default; only the owner sets it.
func SetRules(q db.Q, agentID, memberID string, on bool) error {
	n, err := db.Changes(q.Exec("UPDATE agent_members SET can_set_rules = ? WHERE agent_id = ? AND member_id = ?", db.Bool(on), agentID, memberID))
	if err != nil {
		return err
	}
	if n == 0 {
		return errf("That member isn't in this agent.")
	}
	return nil
}

// RulesOn lists the ids of this agent's members who may set chat rules.
func RulesOn(q db.Q, agentID string) (map[string]bool, error) {
	rows, err := q.Query("SELECT member_id FROM agent_members WHERE agent_id = ? AND can_set_rules = 1", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RuleSetter is the member behind this sender when the owner let them set
// chat rules for this agent; ok false for anyone else. Read only.
func RuleSetter(q db.Q, agentID string, s Sender) (db.Member, bool, error) {
	if s.Channel == db.ChannelWeb || s.Channel == "" {
		return db.Member{}, false, nil
	}
	ids := senderIDs(s)
	if len(ids) == 0 {
		return db.Member{}, false, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := []any{agentID, s.Channel}
	args = append(args, ids...)
	args = append(args, ids...)
	return db.One(q.QueryRow("SELECT "+prefixed("m")+" FROM members m JOIN member_accounts a ON a.member_id = m.id JOIN agent_members am ON am.member_id = m.id WHERE am.agent_id = ? AND am.can_set_rules = 1 AND a.channel = ? AND (a.external_id IN ("+marks+") OR a.alt_id IN ("+marks+")) LIMIT 1", args...), db.ScanMember)
}

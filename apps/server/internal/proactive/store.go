package proactive

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// Nudge kinds (RFC 4.2). cal_event is an internal row per calendar event that
// remembers its last snapshot, so a change can be told; it's never sent.
const (
	KindCalDepart  = "cal_depart"
	KindCalStart   = "cal_start"
	KindCalChanged = "cal_changed"
	KindCalEvent   = "cal_event"
	KindWAWaiting  = "wa_waiting"
	KindSumMorning = "sum_morning"
	KindSumEvening = "sum_evening"
)

// Nudge statuses. pending: found, waiting to be told (held by quiet hours,
// the rate limit or the model). scheduled: a timed nudge waiting for wake_at.
// firing: claimed by a sweep. tracking: a cal_event row.
const (
	StatusPending   = "pending"
	StatusScheduled = "scheduled"
	StatusFiring    = "firing"
	StatusSent      = "sent"
	StatusDone      = "done"
	StatusSnoozed   = "snoozed"
	StatusDropped   = "dropped"
	StatusExpired   = "expired"
	StatusTracking  = "tracking"
)

// OutboxSourcePrefix marks proactive outbox rows: "proactive:<agentId>".
const OutboxSourcePrefix = "proactive:"

// Retention for nudges and ticks.
const RetentionMs = 30 * 24 * 3600_000

func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Get is an agent's row and parsed config. ok false means it was never saved:
// the config is the defaults and proactive is off.
func Get(q db.Q, agentID string) (db.AgentProactive, Config, bool, error) {
	row, ok, err := db.One(q.QueryRow("SELECT "+db.AgentProactiveCols+" FROM agent_proactive WHERE agent_id = ?", agentID), db.ScanAgentProactive)
	if err != nil {
		return db.AgentProactive{}, Config{}, false, err
	}
	if !ok {
		return db.AgentProactive{AgentID: agentID}, Defaults(), false, nil
	}
	cfg, err := ParseConfig(row.Config)
	return row, cfg, true, err
}

// SaveInput changes an agent's proactive settings. Nil fields stay.
type SaveInput struct {
	AgentID string
	Enabled *bool
	// "" clears the target.
	ChatID *string
	Config *Config
}

// CheckTarget refuses a chat proactive may not write to: missing, or one
// whose audience isn't the owner (RFC 11).
func CheckTarget(q db.Q, chatID string) (db.Chat, error) {
	c, ok, err := chats.Get(q, chatID)
	if err != nil {
		return db.Chat{}, err
	}
	if !ok {
		return db.Chat{}, bad("That chat doesn't exist")
	}
	shared, err := agents.IsSharedChat(q, c)
	if err != nil {
		return db.Chat{}, err
	}
	if shared {
		return db.Chat{}, bad("Proactive can only write to your own DM or the web chat")
	}
	return c, nil
}

// Save upserts the row. Turning on needs a target and at least one source,
// and ticks at once; turning off drops the waiting timed nudges (the ledger
// stays, so turning back on doesn't repeat old things).
func Save(q db.Q, in SaveInput, now int64) (db.AgentProactive, Config, error) {
	var out db.AgentProactive
	var outCfg Config
	err := q.Tx(func(tx db.Q) error {
		if _, ok, err := agents.Get(tx, in.AgentID); err != nil {
			return err
		} else if !ok {
			return bad("That agent doesn't exist")
		}
		row, cfg, existed, err := Get(tx, in.AgentID)
		if err != nil {
			return err
		}
		if in.Config != nil {
			if err := in.Config.Validate(); err != nil {
				return err
			}
			cfg = *in.Config
		}
		enabled := row.Enabled
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		chatID := row.ChatID
		if in.ChatID != nil {
			if *in.ChatID == "" {
				chatID = nil
			} else {
				if _, err := CheckTarget(tx, *in.ChatID); err != nil {
					return err
				}
				id := *in.ChatID
				chatID = &id
			}
		}
		if enabled {
			if chatID == nil {
				return bad("Pick a chat to send to first")
			}
			if cfg.Calendar == nil && cfg.WhatsApp == nil {
				return bad("Add a calendar or WhatsApp source first")
			}
			if err := checkSources(tx, in.AgentID, cfg); err != nil {
				return err
			}
		}
		rawCfg, err := MarshalConfig(cfg)
		if err != nil {
			return err
		}
		next := row.NextTickAt
		paused := row.PausedReason
		switch {
		case enabled && (!row.Enabled || in.Config != nil || in.ChatID != nil):
			// On, or changed: look again now, and lift a self-pause.
			next, paused = &now, nil
		case !enabled:
			next, paused = nil, nil
		}
		if !existed {
			_, err = tx.Exec("INSERT INTO agent_proactive (agent_id, enabled, chat_id, config, next_tick_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)", in.AgentID, enabled, chatID, string(rawCfg), next, now, now)
		} else {
			_, err = tx.Exec("UPDATE agent_proactive SET enabled = ?, chat_id = ?, config = ?, next_tick_at = ?, paused_reason = ?, updated_at = ? WHERE agent_id = ?", enabled, chatID, string(rawCfg), next, paused, now, in.AgentID)
		}
		if err != nil {
			return err
		}
		if row.Enabled && !enabled {
			if _, err := tx.Exec("UPDATE nudges SET status = ?, reason = 'proactive turned off', updated_at = ? WHERE agent_id = ? AND status IN (?, ?, ?)", StatusDropped, now, in.AgentID, StatusScheduled, StatusPending, StatusSnoozed); err != nil {
				return err
			}
		}
		out, outCfg, _, err = Get(tx, in.AgentID)
		return err
	})
	return out, outCfg, err
}

// checkSources: every source connector exists, has the right type, and this
// agent may use it (connectors.agent_ids, NULL or [] meaning every agent).
func checkSources(q db.Q, agentID string, cfg Config) error {
	check := func(id, what string, types ...string) error {
		var typ string
		var agentIDs sql.NullString
		err := q.QueryRow("SELECT type, agent_ids FROM connectors WHERE id = ?", id).Scan(&typ, &agentIDs)
		if noRows(err) {
			return bad("The %s connector %s doesn't exist", what, id)
		}
		if err != nil {
			return err
		}
		okType := false
		for _, t := range types {
			okType = okType || typ == t
		}
		if !okType {
			return bad("%s isn't a %s connector", id, what)
		}
		if agentIDs.Valid && agentIDs.String != "" && agentIDs.String != "[]" && !strings.Contains(agentIDs.String, `"`+agentID+`"`) {
			return bad("This agent can't use the connector %s. Allow it on the Connectors page.", id)
		}
		return nil
	}
	if cfg.Calendar != nil {
		calType := "gcal"
		if cfg.Calendar.Tool != "" {
			calType = "mcp"
		}
		if err := check(cfg.Calendar.ConnectorID, "calendar", calType); err != nil {
			return err
		}
	}
	if cfg.WhatsApp != nil {
		if err := check(cfg.WhatsApp.ConnectorID, "WhatsApp", "mcp"); err != nil {
			return err
		}
	}
	return nil
}

// Enabled lists the rows that are on.
func Enabled(q db.Q) ([]db.AgentProactive, error) {
	rows, err := q.Query("SELECT " + db.AgentProactiveCols + " FROM agent_proactive WHERE enabled = 1 ORDER BY created_at, agent_id")
	return db.All(rows, err, db.ScanAgentProactive)
}

// ListNudges is the newest nudges of an agent (tracking rows left out).
func ListNudges(q db.Q, agentID string, limit int) ([]db.Nudge, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND status != ? ORDER BY updated_at DESC, id DESC LIMIT ?", agentID, StatusTracking, limit)
	return db.All(rows, err, db.ScanNudge)
}

// NudgesSince is an agent's nudges updated at or after a time.
func NudgesSince(q db.Q, agentID string, since int64) ([]db.Nudge, error) {
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND status != ? AND updated_at >= ? ORDER BY updated_at DESC, id DESC", agentID, StatusTracking, since)
	return db.All(rows, err, db.ScanNudge)
}

// GetNudge is one nudge.
func GetNudge(q db.Q, id string) (db.Nudge, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.NudgeCols+" FROM nudges WHERE id = ?", id), db.ScanNudge)
}

func nudgeByKey(q db.Q, agentID, key string) (db.Nudge, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND key = ?", agentID, key), db.ScanNudge)
}

func insertNudge(q db.Q, agentID string, c Candidate, status string, wakeAt *int64, reason *string, now int64) (db.Nudge, error) {
	snap, err := jsonx.Marshal(c.Snapshot)
	if err != nil {
		return db.Nudge{}, err
	}
	return db.ScanNudge(q.WQueryRow("INSERT INTO nudges (id, agent_id, key, kind, subject, status, wake_at, snapshot, reason, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.NudgeCols,
		secret.NewID("ndg"), agentID, c.Key, c.Kind, c.Subject, status, wakeAt, string(snap), reason, now, now))
}

func setStatus(q db.Q, id, status string, reason string, now int64) error {
	var r *string
	if reason != "" {
		r = &reason
	}
	_, err := q.Exec("UPDATE nudges SET status = ?, reason = ?, updated_at = ? WHERE id = ?", status, r, now, id)
	return err
}

// Mutes lists an agent's muted subjects, newest first.
func Mutes(q db.Q, agentID string) ([]db.ProactiveMute, error) {
	rows, err := q.Query("SELECT "+db.ProactiveMuteCols+" FROM proactive_mutes WHERE agent_id = ? ORDER BY created_at DESC, subject", agentID)
	return db.All(rows, err, db.ScanProactiveMute)
}

func muted(q db.Q, agentID, subject string) (bool, error) {
	var x int
	err := q.QueryRow("SELECT 1 FROM proactive_mutes WHERE agent_id = ? AND subject = ?", agentID, subject).Scan(&x)
	if noRows(err) {
		return false, nil
	}
	return err == nil, err
}

// Mute adds a subject; every open nudge on it is dropped.
func Mute(q db.Q, agentID, subject, label string, now int64) error {
	if strings.TrimSpace(subject) == "" {
		return bad("Nothing to mute")
	}
	return q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("INSERT INTO proactive_mutes (agent_id, subject, label, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (agent_id, subject) DO NOTHING", agentID, subject, clip(label, 120), now); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE nudges SET status = ?, reason = 'muted', updated_at = ? WHERE agent_id = ? AND subject = ? AND status IN (?, ?, ?, ?)", StatusDropped, now, agentID, subject, StatusPending, StatusScheduled, StatusSnoozed, StatusSent)
		return err
	})
}

// Unmute removes a subject. ok false when it wasn't muted.
func Unmute(q db.Q, agentID, subject string) (bool, error) {
	res, err := q.Exec("DELETE FROM proactive_mutes WHERE agent_id = ? AND subject = ?", agentID, subject)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Ticks is an agent's ticks since a time, newest first.
func Ticks(q db.Q, agentID string, since int64, limit int) ([]db.ProactiveTick, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := q.Query("SELECT "+db.ProactiveTickCols+" FROM proactive_ticks WHERE agent_id = ? AND started_at >= ? ORDER BY started_at DESC, id DESC LIMIT ?", agentID, since, limit)
	return db.All(rows, err, db.ScanProactiveTick)
}

// Prune drops nudges and ticks past retention. Open nudges stay.
func Prune(q db.Q, now int64) error {
	cut := now - RetentionMs
	if _, err := q.Exec("DELETE FROM proactive_ticks WHERE started_at < ?", cut); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM nudges WHERE updated_at < ? AND status IN (?, ?, ?, ?, ?)", cut, StatusSent, StatusDone, StatusDropped, StatusExpired, StatusTracking)
	return err
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

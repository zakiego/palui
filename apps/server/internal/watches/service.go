// Package watches is the watch primitive (PLAN 6e): someone in a chat asks
// the agent to keep an eye on something ("kabarin kalau laba resmi Danantara
// udah keluar"). Every few minutes a read-only turn checks it and answers in
// JSON whether the condition is met. The first time it is, one message goes
// to the chat and the watch is done. A watch never outlives its expiry: the
// last check lands on expires_at, and if that one isn't met either the chat
// gets a short "belum ada" notice.
//
// The service here owns the rows; Engine (engine.go) is the scheduler
// extension that claims due watches and runs their checks.
package watches

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	StatusActive    = "active"
	StatusChecking  = "checking"
	StatusDone      = "done"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
	StatusFailed    = "failed"
)

// Limits. Anyone in an allowed chat can make a watch, so every one of them is
// capped: how many run at once per chat, how long one lives, how often it
// checks and how many tokens it may spend in total.
const (
	MaxActivePerChat = 10
	MinEveryMin      = 15
	MaxEveryMin      = 1440
	DefaultEveryMin  = 30
	DefaultHours     = 7 * 24
	MaxHours         = 14 * 24
	// TokenCap is the total for one watch, all its checks together.
	TokenCap = int64(1_000_000)
	// MaxFails in a row stop a watch.
	MaxFails = 5
	// LeaseMs is how long a claimed check may run before another sweep takes it back.
	LeaseMs = int64(15 * 60_000)
	// LateMs: a watch found more than this past its expiry (Palui was down)
	// expires without one more check.
	LateMs = int64(60 * 60_000)

	MaxTopic    = 80
	MaxQuestion = 1000
	MaxDoneWhen = 1000
	MaxNotes    = 2000
	MaxSummary  = 300
	MaxMessage  = 3000
)

// WatchError carries a message meant for the model or the person asking.
type WatchError struct{ Msg string }

func (e *WatchError) Error() string { return e.Msg }

func watchErr(format string, a ...any) error { return &WatchError{fmt.Sprintf(format, a...)} }

// Ended says whether a status is final.
func Ended(status string) bool {
	return status == StatusDone || status == StatusExpired || status == StatusCancelled || status == StatusFailed
}

type CreateInput struct {
	ChatID    string
	AgentID   string
	CreatedBy string
	Topic     string
	Question  string
	DoneWhen  string
	// 0 is DefaultEveryMin.
	EveryMin int64
	// Unix ms; must be in the future and at most MaxHours away.
	ExpiresAt int64
	// Unix ms; 0 starts now. Otherwise it must be in the future and before
	// ExpiresAt, and nothing is checked until then.
	StartsAt int64
}

// Create adds an active watch. Its first check is one interval from now (the
// turn that made it has just looked), and never later than the expiry. With
// StartsAt, the first check is exactly StartsAt.
func Create(q db.Q, in CreateInput, now int64) (db.Watch, error) {
	topic, question, doneWhen := strx.Trim(in.Topic), strx.Trim(in.Question), strx.Trim(in.DoneWhen)
	switch {
	case topic == "":
		return db.Watch{}, watchErr("Give the watch a short topic.")
	case strx.Len16(topic) > MaxTopic:
		return db.Watch{}, watchErr("The topic is too long (at most %d characters).", MaxTopic)
	case question == "":
		return db.Watch{}, watchErr("Say what to watch for in question.")
	case strx.Len16(question) > MaxQuestion:
		return db.Watch{}, watchErr("question is too long (at most %d characters).", MaxQuestion)
	case doneWhen == "":
		return db.Watch{}, watchErr("Say in doneWhen what counts as found, so old claims don't trigger it.")
	case strx.Len16(doneWhen) > MaxDoneWhen:
		return db.Watch{}, watchErr("doneWhen is too long (at most %d characters).", MaxDoneWhen)
	}
	every := in.EveryMin
	if every == 0 {
		every = DefaultEveryMin
	}
	if every < MinEveryMin || every > MaxEveryMin {
		return db.Watch{}, watchErr("everyMin must be between %d and %d.", MinEveryMin, MaxEveryMin)
	}
	if in.ExpiresAt <= now {
		return db.Watch{}, watchErr("The end time has already passed.")
	}
	if in.ExpiresAt > now+int64(MaxHours)*3600_000 {
		return db.Watch{}, watchErr("A watch can run for at most %d days.", MaxHours/24)
	}
	if in.StartsAt != 0 {
		if in.StartsAt <= now {
			return db.Watch{}, watchErr("The start time has already passed. Leave startAt out to start now.")
		}
		if in.StartsAt >= in.ExpiresAt {
			return db.Watch{}, watchErr("The start time must be before the end time.")
		}
	}
	n, err := CountActive(q, in.ChatID)
	if err != nil {
		return db.Watch{}, err
	}
	if n >= MaxActivePerChat {
		return db.Watch{}, watchErr("This chat already has %d active watches, the most it can have. Cancel one first.", MaxActivePerChat)
	}
	next := min(now+every*60_000, in.ExpiresAt)
	var startsAt *int64
	if in.StartsAt != 0 {
		next, startsAt = in.StartsAt, &in.StartsAt
	}
	var agentID *string
	if in.AgentID != "" {
		agentID = &in.AgentID
	}
	return db.ScanWatch(q.WQueryRow("INSERT INTO watches (id, chat_id, agent_id, created_by, topic, question, done_when, every_min, expires_at, status, next_check_at, starts_at, token_cap, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.WatchCols,
		secret.NewID("wat"), in.ChatID, agentID, strx.Slice16(in.CreatedBy, 100), topic, question, doneWhen, every, in.ExpiresAt, StatusActive, next, startsAt, TokenCap, now, now))
}

// CountActive counts a chat's watches that haven't ended.
func CountActive(q db.Q, chatID string) (int, error) {
	var n int
	err := q.QueryRow("SELECT count(*) FROM watches WHERE chat_id = ? AND status IN (?, ?)", chatID, StatusActive, StatusChecking).Scan(&n)
	return n, err
}

func Get(q db.Q, id string) (db.Watch, bool, error) {
	w, err := db.ScanWatch(q.QueryRow("SELECT "+db.WatchCols+" FROM watches WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, nil
	}
	return w, err == nil, err
}

// List is a chat's watches: running ones by next check, then (with ended)
// the ended ones, latest first.
func List(q db.Q, chatID string, ended bool, limit int) ([]db.Watch, error) {
	if limit <= 0 {
		limit = 20
	}
	where := "status IN ('active', 'checking')"
	order := "next_check_at ASC, id"
	if ended {
		where = "status NOT IN ('active', 'checking')"
		order = "coalesce(ended_at, updated_at) DESC, id"
	}
	rows, err := q.Query("SELECT "+db.WatchCols+" FROM watches WHERE chat_id = ? AND "+where+" ORDER BY "+order+" LIMIT ?", chatID, limit)
	return db.All(rows, err, db.ScanWatch)
}

// Cancel ends a watch that hasn't ended. A check already running finds the
// row cancelled and sends nothing.
func Cancel(q db.Q, id string, now int64) (db.Watch, error) {
	w, err := db.ScanWatch(q.WQueryRow("UPDATE watches SET status = ?, next_check_at = NULL, lease_until = NULL, ended_at = ?, updated_at = ? WHERE id = ? AND status IN (?, ?) RETURNING "+db.WatchCols,
		StatusCancelled, now, now, id, StatusActive, StatusChecking))
	if errors.Is(err, sql.ErrNoRows) {
		cur, ok, gerr := Get(q, id)
		if gerr != nil {
			return cur, gerr
		}
		if !ok {
			return cur, watchErr("No watch %s.", id)
		}
		return cur, nil
	}
	return w, err
}

// Claim takes the most overdue active watch for a check.
func Claim(q db.Q, now int64) (db.Watch, bool, error) {
	w, err := db.ScanWatch(q.WQueryRow("UPDATE watches SET status = ?, lease_until = ?, updated_at = ? WHERE id = (SELECT id FROM watches WHERE status = ? AND next_check_at <= ? ORDER BY next_check_at, id LIMIT 1) AND status = ? RETURNING "+db.WatchCols,
		StatusChecking, now+LeaseMs, now, StatusActive, now, StatusActive))
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, nil
	}
	return w, err == nil, err
}

// ReleaseStale puts checks whose lease ran out back in line.
func ReleaseStale(q db.Q, now int64) error {
	_, err := q.Exec("UPDATE watches SET status = ?, lease_until = NULL, updated_at = ? WHERE status = ? AND lease_until < ?", StatusActive, now, StatusChecking, now)
	return err
}

// Recover releases every claimed check (nothing runs after a restart). A
// check's send and its status move are one transaction, so a claimed row was
// never sent.
func Recover(q db.Q, now int64) error {
	_, err := q.Exec("UPDATE watches SET status = ?, lease_until = NULL, updated_at = ? WHERE status = ?", StatusActive, now, StatusChecking)
	return err
}

// NextDue is the earliest next check or lease end.
func NextDue(q db.Q) (int64, bool, error) {
	var n sql.NullInt64
	err := q.QueryRow("SELECT min(t) FROM (SELECT min(next_check_at) AS t FROM watches WHERE status = ? UNION ALL SELECT min(lease_until) FROM watches WHERE status = ?)", StatusActive, StatusChecking).Scan(&n)
	if err != nil || !n.Valid {
		return 0, false, err
	}
	return n.Int64, true, nil
}

// Prune drops ended watches older than 30 days.
func Prune(q db.Q, now int64) error {
	_, err := q.Exec("DELETE FROM watches WHERE status NOT IN (?, ?) AND coalesce(ended_at, updated_at) < ?", StatusActive, StatusChecking, now-30*24*3600_000)
	return err
}

package proactive

// Memory (RFC 8): short notes carried between runs. The model adds them
// through "remember" and removes them through "forget"; the owner adds them on
// the tab or from chat. A reply to a proactive message is kept as a reply
// note: the owner's words, but saved without asking, so it expires after
// ReplyNoteDays unless the owner keeps it. Owner notes never expire unless the
// owner gave them a date, and are never dropped on their own. When memory is
// full, model notes go first, then expiring reply notes; an owner note that
// still doesn't fit is refused with the notes worth deleting.

import (
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	// NoteSourceOwner and NoteSourceModel are who wrote a note.
	// NoteSourceReply is the owner's reply to a proactive message, saved on
	// its own.
	NoteSourceOwner = "owner"
	NoteSourceModel = "model"
	NoteSourceReply = "reply"
	// MaxNoteChars caps one note (UTF-16 units).
	MaxNoteChars = 300
	// MaxNotes and MaxNotesChars cap one agent's memory.
	MaxNotes      = 50
	MaxNotesChars = 4000
	// Model note lifetimes, in days.
	NoteDaysDefault = 7
	NoteDaysMax     = 30
	// ReplyNoteDays is how long a reply note lives.
	ReplyNoteDays = 3
	// OwnerNoteDaysMax caps an owner note's optional lifetime.
	OwnerNoteDaysMax = 365
	dayMs            = 24 * 3600_000
	// fullListed caps the notes a NotesFullError names.
	fullListed = 10
)

// NotesFull is the refusal for an owner note that doesn't fit.
const NotesFull = "Proactive memory is full. Delete an old note first."

// NotesFullError is the refusal for a note that doesn't fit, with the notes
// the caller could delete: the oldest owner notes and the reply notes, oldest
// first. It unwraps to a ConfigError so callers that only check for one keep
// working.
type NotesFullError struct {
	ConfigError
	Count, Chars int
	Oldest       []db.ProactiveNote
	Replies      []db.ProactiveNote
}

func (e *NotesFullError) Unwrap() error { return &e.ConfigError }

func notesFull(list []db.ProactiveNote) *NotesFullError {
	e := &NotesFullError{ConfigError: ConfigError{NotesFull}, Count: len(list), Chars: notesChars(list), Oldest: []db.ProactiveNote{}, Replies: []db.ProactiveNote{}}
	for _, x := range list {
		switch {
		case x.Source == NoteSourceReply && len(e.Replies) < fullListed:
			e.Replies = append(e.Replies, x)
		case x.Source == NoteSourceOwner && len(e.Oldest) < fullListed:
			e.Oldest = append(e.Oldest, x)
		}
	}
	return e
}

// cleanNote is one note as stored: one paragraph, plain, capped.
func cleanNote(s string) string {
	s = ctlAll.ReplaceAllString(s, " ")
	s = strings.TrimSpace(manySp.ReplaceAllString(s, " "))
	if strx.Len16(s) > MaxNoteChars {
		s = strx.Slice16(s, MaxNoteChars-1) + "…"
	}
	return s
}

// Notes lists an agent's live notes: owner notes first, then replies, then the
// model's, oldest first within each.
func Notes(q db.Q, agentID string, now int64) ([]db.ProactiveNote, error) {
	rows, err := q.Query("SELECT "+db.ProactiveNoteCols+" FROM proactive_notes WHERE agent_id = ? AND (expires_at IS NULL OR expires_at > ?) ORDER BY CASE source WHEN 'owner' THEN 0 WHEN 'reply' THEN 1 ELSE 2 END, created_at, id", agentID, now)
	return db.All(rows, err, db.ScanProactiveNote)
}

func notesChars(list []db.ProactiveNote) int {
	n := 0
	for _, x := range list {
		n += strx.Len16(x.Text)
	}
	return n
}

// AddNote stores a note. days is its lifetime: for a model note 0 means
// NoteDaysDefault (at most NoteDaysMax), for an owner note 0 means it never
// expires (at most OwnerNoteDaysMax), and a reply note always lives
// ReplyNoteDays.
func AddNote(q db.Q, agentID, text, source string, days int, now int64) (db.ProactiveNote, error) {
	var expires *int64
	switch source {
	case NoteSourceModel:
		if days <= 0 {
			days = NoteDaysDefault
		}
		days = min(days, NoteDaysMax)
	case NoteSourceReply:
		days = ReplyNoteDays
	default:
		days = min(max(days, 0), OwnerNoteDaysMax)
	}
	if days > 0 {
		e := now + int64(days)*dayMs
		expires = &e
	}
	return addNote(q, agentID, text, source, expires, now)
}

// AddOwnerNoteUntil stores an owner note that expires at until (Unix ms),
// which must be in the future and within OwnerNoteDaysMax.
func AddOwnerNoteUntil(q db.Q, agentID, text string, until, now int64) (db.ProactiveNote, error) {
	if until <= now {
		return db.ProactiveNote{}, bad("That date has already passed")
	}
	if until > now+int64(OwnerNoteDaysMax)*dayMs {
		return db.ProactiveNote{}, bad("A note can expire at most %d days from now", OwnerNoteDaysMax)
	}
	return addNote(q, agentID, text, NoteSourceOwner, &until, now)
}

// EndOfDay is the end of a local date (YYYY-MM-DD) in tz, as Unix ms: the
// moment a note "until" that date expires.
func EndOfDay(date, tz string) (int64, error) {
	d, err := time.ParseInLocation("2006-01-02", date, loadTZ(tz))
	if err != nil {
		return 0, bad("Use a date like 2026-11-30")
	}
	return d.AddDate(0, 0, 1).UnixMilli(), nil
}

// addNote stores a note. Expired notes are dropped first. A note that doesn't
// fit pushes out the oldest model notes; an owner or reply note then pushes
// out the oldest expiring reply notes. A model note that still doesn't fit is
// dropped quietly; any other is refused with a NotesFullError.
func addNote(q db.Q, agentID, text, source string, expires *int64, now int64) (db.ProactiveNote, error) {
	text = cleanNote(text)
	if text == "" {
		return db.ProactiveNote{}, bad("A note can't be empty")
	}
	var out db.ProactiveNote
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("DELETE FROM proactive_notes WHERE agent_id = ? AND expires_at IS NOT NULL AND expires_at <= ?", agentID, now); err != nil {
			return err
		}
		list, err := Notes(tx, agentID, now)
		if err != nil {
			return err
		}
		for _, x := range list {
			if strings.EqualFold(x.Text, text) {
				out = x
				return nil
			}
		}
		// evictable: the model's notes for anyone, then reply notes that
		// expire anyway (not legacy ones without a date) for the owner.
		evictable := func(x db.ProactiveNote) int {
			switch {
			case x.Source == NoteSourceModel:
				return 0
			case x.Source == NoteSourceReply && x.ExpiresAt != nil && source != NoteSourceModel:
				return 1
			}
			return -1
		}
		fits := func() bool { return len(list)+1 <= MaxNotes && notesChars(list)+strx.Len16(text) <= MaxNotesChars }
		for !fits() {
			pick := -1
			for i, x := range list {
				if r := evictable(x); r >= 0 && (pick < 0 || r < evictable(list[pick])) {
					pick = i
				}
			}
			if pick < 0 {
				if source == NoteSourceModel {
					return nil // memory is full of the owner's notes: drop the model's quietly
				}
				return notesFull(list)
			}
			if _, err := tx.Exec("DELETE FROM proactive_notes WHERE id = ?", list[pick].ID); err != nil {
				return err
			}
			list = append(list[:pick], list[pick+1:]...)
		}
		out, err = db.ScanProactiveNote(tx.WQueryRow("INSERT INTO proactive_notes (id, agent_id, text, source, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING "+db.ProactiveNoteCols,
			secret.NewID("pmem"), agentID, text, source, expires, now, now))
		return err
	})
	return out, err
}

// EditNote changes a note's text, or with keep turns a model or reply note
// (or a dated owner note) into an owner note that never expires. ok false when it doesn't exist.
func EditNote(q db.Q, agentID, id string, text *string, keep bool, now int64) (bool, error) {
	if text != nil {
		t := cleanNote(*text)
		if t == "" {
			return false, bad("A note can't be empty")
		}
		res, err := q.Exec("UPDATE proactive_notes SET text = ?, updated_at = ? WHERE id = ? AND agent_id = ?", t, now, id, agentID)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, nil
		}
	}
	if keep {
		res, err := q.Exec("UPDATE proactive_notes SET source = ?, expires_at = NULL, updated_at = ? WHERE id = ? AND agent_id = ?", NoteSourceOwner, now, id, agentID)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, nil
		}
	}
	return true, nil
}

// ForgetNote deletes a note. ok false when it wasn't there. onlyModel keeps
// the model from deleting the owner's notes.
func ForgetNote(q db.Q, agentID, id string, onlyModel bool) (bool, error) {
	query := "DELETE FROM proactive_notes WHERE id = ? AND agent_id = ?"
	if onlyModel {
		query += " AND source = 'model'"
	}
	res, err := q.Exec(query, id, agentID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PruneNotes deletes expired notes.
func PruneNotes(q db.Q, now int64) error {
	_, err := q.Exec("DELETE FROM proactive_notes WHERE expires_at IS NOT NULL AND expires_at <= ?", now)
	return err
}

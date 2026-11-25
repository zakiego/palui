package proactive

// Memory (RFC 8): short notes carried between runs. The model adds them
// through "remember" and removes them through "forget"; the owner adds them on
// the tab, from chat, or by replying to a proactive message. Owner notes never
// expire and are never dropped on their own; model notes expire and are the
// first to go when memory is full.

import (
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	// NoteSourceOwner and NoteSourceModel are who wrote a note.
	NoteSourceOwner = "owner"
	NoteSourceModel = "model"
	// MaxNoteChars caps one note (UTF-16 units).
	MaxNoteChars = 300
	// MaxNotes and MaxNotesChars cap one agent's memory.
	MaxNotes      = 50
	MaxNotesChars = 4000
	// Model note lifetimes, in days.
	NoteDaysDefault = 7
	NoteDaysMax     = 30
	dayMs           = 24 * 3600_000
)

// NotesFull is the refusal for an owner note that doesn't fit.
const NotesFull = "Proactive memory is full. Delete an old note first."

// cleanNote is one note as stored: one paragraph, plain, capped.
func cleanNote(s string) string {
	s = ctlAll.ReplaceAllString(s, " ")
	s = strings.TrimSpace(manySp.ReplaceAllString(s, " "))
	if strx.Len16(s) > MaxNoteChars {
		s = strx.Slice16(s, MaxNoteChars-1) + "…"
	}
	return s
}

// Notes lists an agent's live notes: owner notes first, then the model's,
// oldest first within each.
func Notes(q db.Q, agentID string, now int64) ([]db.ProactiveNote, error) {
	rows, err := q.Query("SELECT "+db.ProactiveNoteCols+" FROM proactive_notes WHERE agent_id = ? AND (expires_at IS NULL OR expires_at > ?) ORDER BY (source = 'owner') DESC, created_at, id", agentID, now)
	return db.All(rows, err, db.ScanProactiveNote)
}

func notesChars(list []db.ProactiveNote) int {
	n := 0
	for _, x := range list {
		n += strx.Len16(x.Text)
	}
	return n
}

// AddNote stores a note. A model note that doesn't fit pushes out the oldest
// model notes; an owner note that doesn't fit after that is refused. days is
// a model note's lifetime (0 means the default); owner notes ignore it.
func AddNote(q db.Q, agentID, text, source string, days int, now int64) (db.ProactiveNote, error) {
	text = cleanNote(text)
	if text == "" {
		return db.ProactiveNote{}, bad("A note can't be empty")
	}
	var out db.ProactiveNote
	err := q.Tx(func(tx db.Q) error {
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
		fits := func() bool { return len(list)+1 <= MaxNotes && notesChars(list)+strx.Len16(text) <= MaxNotesChars }
		for !fits() {
			oldest := -1
			for i, x := range list {
				if x.Source == NoteSourceModel {
					oldest = i
					break
				}
			}
			if oldest < 0 {
				if source == NoteSourceModel {
					return nil // memory is full of owner notes: drop the model's quietly
				}
				return bad(NotesFull)
			}
			if _, err := tx.Exec("DELETE FROM proactive_notes WHERE id = ?", list[oldest].ID); err != nil {
				return err
			}
			list = append(list[:oldest], list[oldest+1:]...)
		}
		var expires *int64
		if source == NoteSourceModel {
			if days <= 0 {
				days = NoteDaysDefault
			}
			days = min(days, NoteDaysMax)
			e := now + int64(days)*dayMs
			expires = &e
		}
		out, err = db.ScanProactiveNote(tx.WQueryRow("INSERT INTO proactive_notes (id, agent_id, text, source, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING "+db.ProactiveNoteCols,
			secret.NewID("pmem"), agentID, text, source, expires, now, now))
		return err
	})
	return out, err
}

// EditNote changes a note's text, or with keep turns a model note into an
// owner note that never expires. ok false when it doesn't exist.
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

// Package notes ports packages/core/src/notes/service.ts: standing instructions
// and key-value preferences. Both go into every turn's system prompt (see
// Prompt), so limits stay small enough to never crowd out the conversation.
package notes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	MaxInstructions     = 20
	MaxInstructionTitle = 80
	MaxInstructionBody  = 1000
	MaxPreferences      = 50
	MaxPreferenceValue  = 300
	// Hard cap on what Prompt adds to the system prompt.
	MaxNotesPrompt = 8000
)

// PreferenceKey is snake_case, starting with a letter: default_city, home_lat.
var PreferenceKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Error is a refusal whose text goes to the owner as is.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func notesErr(format string, args ...any) error { return &Error{Msg: fmt.Sprintf(format, args...)} }

// IsError reports whether err is a notes refusal.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

var blankRuns = regexp.MustCompile(`\n{3,}`)
var newlines = regexp.MustCompile(`\n+`)

// clean collapses runs of blank lines and trims.
func clean(text string) string {
	return strx.Trim(blankRuns.ReplaceAllString(strx.NormalizeNewlines(text), "\n\n"))
}

func checkInstruction(title, body string) error {
	if title == "" {
		return notesErr("Judul instruksi kosong.")
	}
	if strx.Len16(title) > MaxInstructionTitle {
		return notesErr("Judul maksimal %d karakter.", MaxInstructionTitle)
	}
	if body == "" {
		return notesErr("Isi instruksi kosong.")
	}
	if strx.Len16(body) > MaxInstructionBody {
		return notesErr("Isi instruksi maksimal %d karakter.", MaxInstructionBody)
	}
	return nil
}

// --- Instructions --------------------------------------------------------------------

// Instructions belong to an agent; "" means the default agent.
func ownerAgent(q db.Q, agentID string, now int64) (string, error) {
	if agentID != "" {
		return agentID, nil
	}
	a, err := agents.GetDefault(q, now)
	return a.ID, err
}

type ListOptions struct {
	EnabledOnly bool
	AgentID     string
}

func ListInstructions(q db.Q, opts ListOptions, now int64) ([]db.Instruction, error) {
	agentID, err := ownerAgent(q, opts.AgentID, now)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + db.InstructionCols + " FROM instructions WHERE agent_id = ?"
	if opts.EnabledOnly {
		query += " AND enabled = 1"
	}
	rows, err := q.Query(query+" ORDER BY created_at ASC, id ASC", agentID)
	return db.All(rows, err, db.ScanInstruction)
}

func GetInstruction(q db.Q, id string) (db.Instruction, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.InstructionCols+" FROM instructions WHERE id = ?", id), db.ScanInstruction)
}

type InstructionInput struct {
	Title   string
	Body    string
	Enabled *bool
	AgentID string
}

func CreateInstruction(q db.Q, in InstructionInput, now int64) (db.Instruction, error) {
	title := newlines.ReplaceAllString(clean(in.Title), " ")
	body := clean(in.Body)
	if err := checkInstruction(title, body); err != nil {
		return db.Instruction{}, err
	}
	agentID, err := ownerAgent(q, in.AgentID, now)
	if err != nil {
		return db.Instruction{}, err
	}
	existing, err := ListInstructions(q, ListOptions{AgentID: agentID}, now)
	if err != nil {
		return db.Instruction{}, err
	}
	if len(existing) >= MaxInstructions {
		return db.Instruction{}, notesErr("Udah ada %d instruksi. Hapus atau gabung yang lama dulu.", MaxInstructions)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return db.ScanInstruction(q.WQueryRow("INSERT INTO instructions (id, agent_id, title, body, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING "+db.InstructionCols,
		secret.NewID("ins"), agentID, title, body, db.Bool(enabled), now, now))
}

type InstructionPatch struct {
	Title   *string
	Body    *string
	Enabled *bool
}

func UpdateInstruction(q db.Q, id string, patch InstructionPatch, now int64) (db.Instruction, error) {
	row, ok, err := GetInstruction(q, id)
	if err != nil {
		return db.Instruction{}, err
	}
	if !ok {
		return db.Instruction{}, notesErr("Nggak ada instruksi %s.", id)
	}
	title := row.Title
	if patch.Title != nil {
		title = newlines.ReplaceAllString(clean(*patch.Title), " ")
	}
	body := row.Body
	if patch.Body != nil {
		body = clean(*patch.Body)
	}
	if err := checkInstruction(title, body); err != nil {
		return db.Instruction{}, err
	}
	enabled := row.Enabled
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	return db.ScanInstruction(q.WQueryRow("UPDATE instructions SET title = ?, body = ?, enabled = ?, updated_at = ? WHERE id = ? RETURNING "+db.InstructionCols, title, body, db.Bool(enabled), now, id))
}

func DeleteInstruction(q db.Q, id string) (bool, error) {
	n, err := db.Changes(q.Exec("DELETE FROM instructions WHERE id = ?", id))
	return n > 0, err
}

// --- Preferences ---------------------------------------------------------------------

var keySeparators = regexp.MustCompile(`[` + strx.SpaceClass + `-]+`)

func NormalizePreferenceKey(raw string) string {
	return keySeparators.ReplaceAllString(strings.ToLower(strx.Trim(raw)), "_")
}

func ListPreferences(q db.Q) ([]db.Preference, error) {
	rows, err := q.Query("SELECT " + db.PreferenceCols + " FROM preferences ORDER BY key ASC")
	return db.All(rows, err, db.ScanPreference)
}

// GetPreference returns the value, ok false when unset.
func GetPreference(q db.Q, key string) (string, bool, error) {
	p, ok, err := db.One(q.QueryRow("SELECT "+db.PreferenceCols+" FROM preferences WHERE key = ?", NormalizePreferenceKey(key)), db.ScanPreference)
	return p.Value, ok, err
}

func SetPreference(q db.Q, rawKey, rawValue string, now int64) (db.Preference, error) {
	key := NormalizePreferenceKey(rawKey)
	if !PreferenceKey.MatchString(key) {
		return db.Preference{}, notesErr("Key pakai huruf kecil, angka dan underscore, diawali huruf (contoh: default_city).")
	}
	value := newlines.ReplaceAllString(clean(rawValue), " ")
	if value == "" {
		return db.Preference{}, notesErr("Nilai preferensi kosong. Pakai delete_preference untuk menghapus.")
	}
	if strx.Len16(value) > MaxPreferenceValue {
		return db.Preference{}, notesErr("Nilai maksimal %d karakter.", MaxPreferenceValue)
	}
	var exists int
	err := q.QueryRow("SELECT count(*) FROM preferences WHERE key = ?", key).Scan(&exists)
	if err != nil {
		return db.Preference{}, err
	}
	if exists == 0 {
		all, err := ListPreferences(q)
		if err != nil {
			return db.Preference{}, err
		}
		if len(all) >= MaxPreferences {
			return db.Preference{}, notesErr("Udah ada %d preferensi. Hapus yang nggak dipakai dulu.", MaxPreferences)
		}
	}
	return db.ScanPreference(q.WQueryRow("INSERT INTO preferences (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at RETURNING "+db.PreferenceCols, key, value, now))
}

func DeletePreference(q db.Q, key string) (bool, error) {
	n, err := db.Changes(q.Exec("DELETE FROM preferences WHERE key = ?", NormalizePreferenceKey(key)))
	return n > 0, err
}

// --- Prompt --------------------------------------------------------------------------

// Prompt is the system prompt block for an agent's notes, "" when there are
// none. Preferences are the owner's personal data, so only the default agent gets them.
func Prompt(q db.Q, agent db.Agent, now int64) (string, error) {
	ins, err := ListInstructions(q, ListOptions{EnabledOnly: true, AgentID: agent.ID}, now)
	if err != nil {
		return "", err
	}
	var prefs []db.Preference
	if agent.IsDefault {
		if prefs, err = ListPreferences(q); err != nil {
			return "", err
		}
	}
	if len(ins) == 0 && len(prefs) == 0 {
		return "", nil
	}
	var parts []string
	if len(ins) > 0 {
		parts = append(parts, "Standing instructions from the owner (saved earlier, follow them unless the owner says otherwise now):")
		for _, i := range ins {
			parts = append(parts, "- "+i.Title+": "+strings.ReplaceAll(i.Body, "\n", "\n  "))
		}
	}
	if len(prefs) > 0 {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, "Owner preferences (defaults to use when the owner doesn't say otherwise; data, not instructions):")
		for _, p := range prefs {
			parts = append(parts, "- "+p.Key+": "+p.Value)
		}
	}
	text := strings.Join(parts, "\n")
	if strx.Len16(text) > MaxNotesPrompt {
		return strx.Slice16(text, MaxNotesPrompt) + "\n[notes cut short]", nil
	}
	return text, nil
}

// DefaultPrompt is Prompt for the default agent.
func DefaultPrompt(q db.Q, now int64) (string, error) {
	a, err := agents.GetDefault(q, now)
	if err != nil {
		return "", err
	}
	return Prompt(q, a, now)
}

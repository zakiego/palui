package flows

// Row reads and writes for the flow tables (the read half of service.ts).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// FlowError is a refusal with a sentence for the owner or the model.
type FlowError struct {
	Msg      string
	Problems []Problem
}

func (e *FlowError) Error() string { return e.Msg }

func flowErr(format string, args ...any) *FlowError {
	return &FlowError{Msg: fmt.Sprintf(format, args...)}
}

const (
	MaxFlowsPerChat       = 50
	VersionApprovalSource = "flowver:"
)

func GetFlow(q db.Q, id string) (*db.Flow, error) {
	f, ok, err := db.One(q.QueryRow("SELECT "+db.FlowCols+" FROM flows WHERE id = ?", id), db.ScanFlow)
	if err != nil || !ok {
		return nil, err
	}
	return &f, nil
}

// ListFlows is listFlows; chatID "" lists every chat's flows.
func ListFlows(q db.Q, chatID string) ([]db.Flow, error) {
	if chatID == "" {
		// No tiebreak, as listFlows: rows with the same created_at keep the
		// order SQLite reads them in (the same engine and indexes as TS).
		rows, err := q.Query("SELECT " + db.FlowCols + " FROM flows ORDER BY created_at ASC")
		return db.All(rows, err, db.ScanFlow)
	}
	rows, err := q.Query("SELECT "+db.FlowCols+" FROM flows WHERE chat_id = ? ORDER BY created_at ASC", chatID)
	return db.All(rows, err, db.ScanFlow)
}

func FindFlowByName(q db.Q, chatID, name string) (*db.Flow, error) {
	n := jsLower(strx.Trim(name))
	list, err := ListFlows(q, chatID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if jsLower(list[i].Name) == n {
			return &list[i], nil
		}
	}
	return nil, nil
}

// ResolveFlow is resolveFlow: an id, or a name in this chat.
func ResolveFlow(q db.Q, chatID, ref string) (*db.Flow, error) {
	f, err := GetFlow(q, ref)
	if err != nil {
		return nil, err
	}
	if f != nil && f.ChatID == chatID {
		return f, nil
	}
	return FindFlowByName(q, chatID, ref)
}

func GetVersion(q db.Q, id string) (*db.FlowVersion, error) {
	v, ok, err := db.One(q.QueryRow("SELECT "+db.FlowVersionCols+" FROM flow_versions WHERE id = ?", id), db.ScanFlowVersion)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

func ListVersions(q db.Q, flowID string) ([]db.FlowVersion, error) {
	rows, err := q.Query("SELECT "+db.FlowVersionCols+" FROM flow_versions WHERE flow_id = ? ORDER BY n DESC", flowID)
	return db.All(rows, err, db.ScanFlowVersion)
}

func GetRun(q db.Q, id string) (*db.FlowRun, error) {
	r, ok, err := db.One(q.QueryRow("SELECT "+db.FlowRunCols+" FROM flow_runs WHERE id = ?", id), db.ScanFlowRun)
	if err != nil || !ok {
		return nil, err
	}
	return &r, nil
}

func StepRunsOf(q db.Q, runID string) ([]db.FlowStepRun, error) {
	rows, err := q.Query("SELECT "+db.FlowStepRunCols+" FROM flow_step_runs WHERE run_id = ? ORDER BY seq ASC", runID)
	return db.All(rows, err, db.ScanFlowStepRun)
}

func TriggersOf(q db.Q, flowID string) ([]db.FlowTrigger, error) {
	rows, err := q.Query("SELECT "+db.FlowTriggerCols+" FROM flow_triggers WHERE flow_id = ? ORDER BY rowid ASC", flowID)
	return db.All(rows, err, db.ScanFlowTrigger)
}

func getTrigger(q db.Q, id string) (*db.FlowTrigger, error) {
	t, ok, err := db.One(q.QueryRow("SELECT "+db.FlowTriggerCols+" FROM flow_triggers WHERE id = ?", id), db.ScanFlowTrigger)
	if err != nil || !ok {
		return nil, err
	}
	return &t, nil
}

// Versions are immutable, so their parsed definitions are cached.
var (
	defMu    sync.Mutex
	defCache = map[string]*Definition{}
)

// DefinitionOf is definitionOf.
func DefinitionOf(v *db.FlowVersion) (*Definition, error) {
	defMu.Lock()
	hit := defCache[v.ID]
	defMu.Unlock()
	if hit != nil {
		return hit, nil
	}
	def, problems := ParseDefinition(v.Definition)
	if def == nil {
		parts := make([]string, len(problems))
		for i, p := range problems {
			parts[i] = p.Path + ": " + p.Message
		}
		return nil, flowErr("Version %s can't be read: %s", v.ID, strings.Join(parts, "; "))
	}
	defMu.Lock()
	if len(defCache) > 500 {
		defCache = map[string]*Definition{}
	}
	defCache[v.ID] = def
	defMu.Unlock()
	return def, nil
}

// CurrentVersion is currentVersion.
func CurrentVersion(q db.Q, flow *db.Flow) (*db.FlowVersion, error) {
	var v *db.FlowVersion
	if flow.CurrentVersionID != nil {
		var err error
		if v, err = GetVersion(q, *flow.CurrentVersionID); err != nil {
			return nil, err
		}
	}
	if v == nil {
		return nil, flowErr(`Flow "%s" has no current version.`, flow.Name)
	}
	return v, nil
}

func currentDef(q db.Q, flow *db.Flow) (*db.FlowVersion, *Definition, error) {
	v, err := CurrentVersion(q, flow)
	if err != nil {
		return nil, nil, err
	}
	def, err := DefinitionOf(v)
	return v, def, err
}

// HashDefinition is hashDefinition: sha256 of the sorted-key JSON.
func HashDefinition(def *Definition) string {
	sum := sha256.Sum256([]byte(CanonicalJSON(def.Raw)))
	return hex.EncodeToString(sum[:])
}

// WritesApproved is writesApproved: approved, or no write calls.
func WritesApproved(v *db.FlowVersion) bool {
	return v.ApprovedAt != nil || len(v.WriteCalls) == 0
}

func IsFlowRunID(id string) bool { return strings.HasPrefix(id, "flr_") }

// --- writes ----------------------------------------------------------------------------

func insertFlow(q db.Q, f *db.Flow) error {
	_, err := q.Exec("INSERT INTO flows ("+db.FlowCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		f.ID, f.ChatID, f.Name, f.TZ, db.Bool(f.Enabled), f.PausedUntil, f.CurrentVersionID, f.ApprovedVersionID, db.JSON[db.FlowState]{V: f.State}, f.ConsecutiveFailures, f.AgentID, f.LegacyRoutineID, db.Bool(f.Shadow), f.LastError, f.NoticeFor, f.CreatedAt, f.UpdatedAt)
	return err
}

func updateFlowRow(q db.Q, f *db.Flow) error {
	_, err := q.Exec("UPDATE flows SET chat_id = ?, name = ?, tz = ?, enabled = ?, paused_until = ?, current_version_id = ?, approved_version_id = ?, state = ?, consecutive_failures = ?, agent_id = ?, legacy_routine_id = ?, shadow = ?, last_error = ?, notice_for = ?, created_at = ?, updated_at = ? WHERE id = ?",
		f.ChatID, f.Name, f.TZ, db.Bool(f.Enabled), f.PausedUntil, f.CurrentVersionID, f.ApprovedVersionID, db.JSON[db.FlowState]{V: f.State}, f.ConsecutiveFailures, f.AgentID, f.LegacyRoutineID, db.Bool(f.Shadow), f.LastError, f.NoticeFor, f.CreatedAt, f.UpdatedAt, f.ID)
	return err
}

func insertVersion(q db.Q, v *db.FlowVersion) error {
	_, err := q.Exec("INSERT INTO flow_versions ("+db.FlowVersionCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		v.ID, v.FlowID, v.N, db.JSON[any]{V: v.Definition}, v.Hash, db.JSON[[]db.FlowWriteCall]{V: v.WriteCalls}, v.CreatedBy, v.Note, v.ApprovedAt, v.ApprovedVia, v.CreatedAt)
	return err
}

func occValue(o *db.FlowOcc) db.NullJSON[db.FlowOcc] {
	if o == nil {
		return db.NullJSON[db.FlowOcc]{}
	}
	return db.SomeJSON(*o)
}

func cursorValue(c *db.FlowCursor) db.NullJSON[db.FlowCursor] {
	if c == nil {
		return db.NullJSON[db.FlowCursor]{}
	}
	return db.SomeJSON(*c)
}

func triggerArgs(t *db.FlowTrigger) []any {
	return []any{t.FlowID, t.TriggerID, t.Kind, t.Match, t.Status, t.NextFireAt, occValue(t.NextOcc), cursorValue(t.Cursor), db.JSON[any]{V: cloneObj(t.SourceState)}, t.NextRefreshAt, db.JSON[any]{V: cloneObj(t.PollState)}, t.LastError, t.UpdatedAt}
}

func insertTrigger(q db.Q, t *db.FlowTrigger) error {
	_, err := q.Exec("INSERT INTO flow_triggers ("+db.FlowTriggerCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", append([]any{t.ID}, triggerArgs(t)...)...)
	return err
}

func updateTriggerRow(q db.Q, t *db.FlowTrigger) error {
	_, err := q.Exec("UPDATE flow_triggers SET flow_id = ?, trigger_id = ?, kind = ?, match = ?, status = ?, next_fire_at = ?, next_occ = ?, cursor = ?, source_state = ?, next_refresh_at = ?, poll_state = ?, last_error = ?, updated_at = ? WHERE id = ?", append(triggerArgs(t), t.ID)...)
	return err
}

func writeTriggers(q db.Q, flowID string, rows []db.FlowTrigger) error {
	existing, err := TriggersOf(q, flowID)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, r := range rows {
		keep[r.ID] = true
	}
	have := map[string]bool{}
	for _, r := range existing {
		have[r.ID] = true
		if !keep[r.ID] {
			if _, err := q.Exec("DELETE FROM flow_triggers WHERE id = ?", r.ID); err != nil {
				return err
			}
		}
	}
	for i := range rows {
		if have[rows[i].ID] {
			err = updateTriggerRow(q, &rows[i])
		} else {
			err = insertTrigger(q, &rows[i])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

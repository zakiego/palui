// Package approvals ports packages/core/src/approvals/store.ts: pending tool
// approvals. A turn that ends with tool-approval-request parts is saved here
// with everything needed to resume it, so it survives a restart. All requests
// from one run are answered together in one resume call.
package approvals

import (
	"encoding/json"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const TTLMs = 10 * 60_000

// ResumeContext is what a resume needs besides the messages (stored in
// `context`). Field order follows the TS type. Rows written by the TS build
// may carry keys in another order; the row's Context keeps them as stored.
type ResumeContext struct {
	// "message", "schedule", "hook", "approval", "reminder" or "flow".
	Trigger string `json:"trigger"`
	// The trigger of the turn that asked; schedule and hook keep their stricter policy on resume.
	OriginTrigger string    `json:"originTrigger"`
	JobID         string    `json:"jobId,omitempty"`
	ToolScope     *[]string `json:"toolScope,omitempty"`
	// Namespaced tools a flow prompt step allowed ("*": all).
	ToolNames   *[]string `json:"toolNames,omitempty"`
	ModelRole   string    `json:"modelRole"`
	Deliver     bool      `json:"deliver"`
	SilentToken string    `json:"silentToken,omitempty"`
	Tainted     bool      `json:"tainted"`
	SessionID   *string   `json:"sessionId"`
	// The system prompt as sent, block by block (a plain string on old rows).
	System     json.RawMessage `json:"system"`
	BatchRunID string          `json:"batchRunId,omitempty"`
	ResumedAt  int64           `json:"resumedAt,omitempty"`
	AgentID    string          `json:"agentId,omitempty"`
	// "owner", "members" or "outside". "sharedOwner" (or "guest") only on an approval asked before PLAN 19.9 step 1.
	Mode string `json:"mode,omitempty"`
}

// DecodeContext reads a row's context into the typed shape.
func DecodeContext(ctx *jsonx.Object) (ResumeContext, error) {
	var out ResumeContext
	b, err := jsonx.Marshal(ctx)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

// askedIn: approvals asked in this chat, its own and those of shared chats asked here.
const askedIn = "(ask_chat_id = ? OR (ask_chat_id IS NULL AND chat_id = ?))"

type CreateInput struct {
	RunID  string
	ChatID string
	// Where the prompt goes when that isn't ChatID (a shared chat asks in the owner's DM).
	AskChatID   *string
	ApprovalID  string
	ToolCallID  string
	Tool        string
	ToolClass   string
	ArgsPreview string
	// AI SDK v7 ModelMessage[] as JSON.
	Messages db.RawJSON
	Context  ResumeContext
	// ContextJSON, when set, is stored instead of Context: the turn writes the
	// object in the TS key order, nulls included (toolScope: null).
	ContextJSON *jsonx.Object
}

func Create(q db.Q, in CreateInput, now int64, ttlMs int64) (db.Approval, error) {
	if ttlMs == 0 {
		ttlMs = TTLMs
	}
	var ctx db.RawJSON
	var err error
	if in.ContextJSON != nil {
		ctx, err = db.Raw(in.ContextJSON)
	} else {
		ctx, err = db.Raw(in.Context)
	}
	if err != nil {
		return db.Approval{}, err
	}
	msgs := in.Messages
	if len(msgs) == 0 {
		msgs = db.RawJSON("[]")
	}
	return db.ScanApproval(q.WQueryRow("INSERT INTO pending_approvals (id, run_id, chat_id, ask_chat_id, approval_id, tool_call_id, tool, tool_class, args_preview, messages, context, expires_at, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?) RETURNING "+db.ApprovalCols,
		secret.NewID("apr"), in.RunID, in.ChatID, in.AskChatID, in.ApprovalID, in.ToolCallID, in.Tool, in.ToolClass, in.ArgsPreview, msgs, ctx, now+ttlMs, now))
}

func Get(q db.Q, id string) (db.Approval, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ApprovalCols+" FROM pending_approvals WHERE id = ?", id), db.ScanApproval)
}

func PendingForChat(q db.Q, chatID string) ([]db.Approval, error) {
	rows, err := q.Query("SELECT "+db.ApprovalCols+" FROM pending_approvals WHERE "+askedIn+" AND status = 'pending' ORDER BY created_at ASC", chatID, chatID)
	return db.All(rows, err, db.ScanApproval)
}

func ForRun(q db.Q, runID string) ([]db.Approval, error) {
	rows, err := q.Query("SELECT "+db.ApprovalCols+" FROM pending_approvals WHERE run_id = ? ORDER BY created_at ASC", runID)
	return db.All(rows, err, db.ScanApproval)
}

// Decide records a decision on a pending row. False when it was already decided or expired.
func Decide(q db.Q, id string, approved bool, now int64) (bool, error) {
	status := "denied"
	if approved {
		status = "approved"
	}
	n, err := db.Changes(q.Exec("UPDATE pending_approvals SET status = ?, decided_at = ? WHERE id = ? AND status = 'pending'", status, now, id))
	return n > 0, err
}

func uniqueRuns(rows []db.Approval) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if !seen[r.RunID] {
			seen[r.RunID] = true
			out = append(out, r.RunID)
		}
	}
	return out
}

// ExpireDue expires due rows and returns the run ids that now have every request decided.
func ExpireDue(q db.Q, now int64) ([]string, error) {
	rows, err := q.WQuery("UPDATE pending_approvals SET status = 'expired', decided_at = ? WHERE status = 'pending' AND expires_at <= ? RETURNING "+db.ApprovalCols, now, now)
	all, err := db.All(rows, err, db.ScanApproval)
	if err != nil {
		return nil, err
	}
	return uniqueRuns(all), nil
}

const SupersededPreview = "dropped: the same call was made again before the owner answered"

// Superseded is what SupersedeSameCall did.
type Superseded struct {
	Superseded []db.Approval
	// Runs that had another request answered and are now fully decided: they still owe a resume.
	Resume []string
}

type SameCall struct {
	ChatID string
	RunID  string
	Tool   string
	Args   any
}

// SupersedeSameCall: the model made a call again (same tool, same arguments)
// while an older request for it still waits in this chat, so the older one is
// moot. It's marked superseded and its call denied. A run left with only
// superseded requests is closed here.
func SupersedeSameCall(q db.Q, in SameCall, now int64) (Superseded, error) {
	var out Superseded
	pending, err := PendingForChat(q, in.ChatID)
	if err != nil {
		return out, err
	}
	want := jsonx.Canonical(in.Args)
	for _, r := range pending {
		if r.Tool != in.Tool || r.RunID == in.RunID {
			continue
		}
		toolCallID := ""
		if r.ToolCallID != nil {
			toolCallID = *r.ToolCallID
		}
		callID := r.RunID + ":" + toolCallID
		var args db.RawJSON
		err := q.QueryRow("SELECT args FROM tool_calls WHERE id = ?", callID).Scan(&args)
		if err != nil {
			continue
		}
		prior, err := args.Parse()
		if err != nil || jsonx.Canonical(prior) != want {
			continue
		}
		err = q.Tx(func(tx db.Q) error {
			if _, err := tx.Exec("UPDATE pending_approvals SET status = 'superseded', decided_at = ? WHERE id = ?", now, r.ID); err != nil {
				return err
			}
			_, err := tx.Exec("UPDATE tool_calls SET status = 'denied', result_preview = ? WHERE id = ?", SupersededPreview, callID)
			return err
		})
		if err != nil {
			return out, err
		}
		out.Superseded = append(out.Superseded, r)
	}
	for _, runID := range uniqueRuns(out.Superseded) {
		rows, err := ForRun(q, runID)
		if err != nil {
			return out, err
		}
		anyPending, anyOther := false, false
		for _, r := range rows {
			if r.Status == "pending" {
				anyPending = true
			}
			if r.Status != "superseded" {
				anyOther = true
			}
		}
		if anyPending {
			continue
		}
		if anyOther {
			out.Resume = append(out.Resume, runID)
			continue
		}
		ok, err := MarkResumed(q, runID, now)
		if err != nil {
			return out, err
		}
		if ok {
			if _, err := q.Exec("UPDATE runs SET status = 'ok' WHERE id = ?", runID); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func resumed(r db.Approval) bool {
	v, _ := r.Context.Get("resumedAt")
	return jsonx.Truthy(v)
}

// ReadyToResume lists runs whose requests are all decided but not resumed yet
// (after a restart). A resumed row keeps its decision status and gets context.resumedAt.
func ReadyToResume(q db.Q) ([]string, error) {
	rows, err := q.Query("SELECT " + db.ApprovalCols + " FROM pending_approvals WHERE status IN ('approved', 'denied', 'expired', 'superseded', 'pending')")
	all, err := db.All(rows, err, db.ScanApproval)
	if err != nil {
		return nil, err
	}
	var order []string
	byRun := map[string][]db.Approval{}
	for _, r := range all {
		if _, ok := byRun[r.RunID]; !ok {
			order = append(order, r.RunID)
		}
		byRun[r.RunID] = append(byRun[r.RunID], r)
	}
	var out []string
	for _, id := range order {
		ready := true
		for _, r := range byRun[id] {
			if r.Status == "pending" || resumed(r) {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, id)
		}
	}
	return out, nil
}

// MarkResumed claims a run for resuming. False when another caller already resumed it.
func MarkResumed(q db.Q, runID string, now int64) (bool, error) {
	claimed := false
	err := q.Tx(func(tx db.Q) error {
		rows, err := tx.Query("SELECT "+db.ApprovalCols+" FROM pending_approvals WHERE run_id = ?", runID)
		all, err := db.All(rows, err, db.ScanApproval)
		if err != nil {
			return err
		}
		if len(all) == 0 {
			return nil
		}
		for _, r := range all {
			if r.Status == "pending" || resumed(r) {
				return nil
			}
		}
		for _, r := range all {
			ctx := r.Context.Clone()
			ctx.Set("resumedAt", float64(now))
			raw, err := db.Raw(ctx)
			if err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE pending_approvals SET context = ? WHERE id = ?", raw, r.ID); err != nil {
				return err
			}
		}
		claimed = true
		return nil
	})
	return claimed, err
}

// Preview is a value as text for an approval prompt, cut to max characters
// (300 by default) with an ellipsis.
func Preview(value any, max int) string {
	if max == 0 {
		max = 300
	}
	s, ok := value.(string)
	if !ok {
		b, err := jsonx.Marshal(value)
		if err != nil {
			s = ""
		} else {
			s = string(b)
		}
	}
	if strx.Len16(s) > max {
		return strx.Slice16(s, max-1) + "…"
	}
	return s
}

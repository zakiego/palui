package approvals_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// approvals/store.ts has no test file of its own (the engine tests drive it in
// M3); these pin the store's rules.

func create(t *testing.T, d db.Q, runID, chatID, tool string, now int64) db.Approval {
	t.Helper()
	a, err := approvals.Create(d, approvals.CreateInput{RunID: runID, ChatID: chatID, ApprovalID: "ap_" + runID + tool, ToolCallID: "call_" + tool, Tool: tool, ToolClass: "write", ArgsPreview: "{}", Messages: db.RawJSON(`[{"role":"user","content":"hi"}]`), Context: approvals.ResumeContext{Trigger: "message", OriginTrigger: "message", ModelRole: "default", Deliver: true, System: []byte(`"sys"`)}}, now, 0)
	testutil.Must(t, err)
	return a
}

func TestCreateDecideExpireAndResume(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	a := create(t, d, "run_1", chatID, "x__a", 1000)
	b := create(t, d, "run_1", chatID, "x__b", 1001)
	if a.ExpiresAt != 1000+approvals.TTLMs || a.Status != "pending" {
		t.Fatalf("%+v", a)
	}
	ctx, err := approvals.DecodeContext(a.Context)
	if err != nil || ctx.Trigger != "message" || string(ctx.System) != `"sys"` {
		t.Fatal(ctx, err)
	}
	if p, _ := approvals.PendingForChat(d, chatID); len(p) != 2 {
		t.Fatal(len(p))
	}
	if ok, _ := approvals.Decide(d, a.ID, true, 2000); !ok {
		t.Fatal("decide")
	}
	if ok, _ := approvals.Decide(d, a.ID, false, 2001); ok {
		t.Fatal("decided twice")
	}
	if r, _ := approvals.ReadyToResume(d); len(r) != 0 {
		t.Fatal(r)
	}
	runs, err := approvals.ExpireDue(d, b.ExpiresAt)
	if err != nil || !reflect.DeepEqual(runs, []string{"run_1"}) {
		t.Fatal(runs, err)
	}
	if r, _ := approvals.ReadyToResume(d); !reflect.DeepEqual(r, []string{"run_1"}) {
		t.Fatal(r)
	}
	if ok, _ := approvals.MarkResumed(d, "run_1", 3000); !ok {
		t.Fatal("mark")
	}
	if ok, _ := approvals.MarkResumed(d, "run_1", 3001); ok {
		t.Fatal("marked twice")
	}
	if r, _ := approvals.ReadyToResume(d); len(r) != 0 {
		t.Fatal(r)
	}
	got, _, _ := approvals.Get(d, a.ID)
	if v, _ := got.Context.Get("resumedAt"); v != float64(3000) {
		t.Fatal(v)
	}
	if !strings.HasPrefix(jsonx.Stringify(got.Context), `{"trigger":"message","originTrigger":"message","modelRole":"default"`) {
		t.Fatal(jsonx.Stringify(got.Context))
	}
}

func TestAskedInStillFindsOldRowsAskedHere(t *testing.T) {
	d, chatID, otherID := testutil.Setup(t)
	row, err := approvals.Create(d, approvals.CreateInput{RunID: "run_s", ChatID: otherID, ApprovalID: "ap", Tool: "x__a", ToolClass: "write", Context: approvals.ResumeContext{System: []byte(`""`)}}, 1, 0)
	testutil.Must(t, err)
	// Only rows from before PLAN 19.9 step 5 have ask_chat_id.
	_, err = d.Exec("UPDATE pending_approvals SET ask_chat_id = ? WHERE id = ?", chatID, row.ID)
	testutil.Must(t, err)
	if p, _ := approvals.PendingForChat(d, chatID); len(p) != 1 {
		t.Fatal(len(p))
	}
	if p, _ := approvals.PendingForChat(d, otherID); len(p) != 0 {
		t.Fatal(len(p))
	}
}

func TestSupersedeSameCall(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	old := create(t, d, "run_old", chatID, "x__send", 1000)
	_, err := d.Exec("INSERT INTO runs (id, chat_id, trigger, status, started_at) VALUES ('run_old', ?, 'message', 'awaiting_approval', 1)", chatID)
	testutil.Must(t, err)
	_, err = d.Exec(`INSERT INTO tool_calls (id, run_id, tool, class, args, status, created_at) VALUES ('run_old:call_x__send', 'run_old', 'x__send', 'write', '{"b":2,"a":1}', 'pending', 1)`)
	testutil.Must(t, err)
	args, _ := jsonx.Parse([]byte(`{"a":1,"b":2}`))
	res, err := approvals.SupersedeSameCall(d, approvals.SameCall{ChatID: chatID, RunID: "run_new", Tool: "x__send", Args: args}, 2000)
	testutil.Must(t, err)
	if len(res.Superseded) != 1 || res.Superseded[0].ID != old.ID || len(res.Resume) != 0 {
		t.Fatalf("%+v", res)
	}
	var status, preview, runStatus string
	testutil.Must(t, d.QueryRow("SELECT status, result_preview FROM tool_calls WHERE id = 'run_old:call_x__send'").Scan(&status, &preview))
	testutil.Must(t, d.QueryRow("SELECT status FROM runs WHERE id = 'run_old'").Scan(&runStatus))
	if status != "denied" || preview != approvals.SupersededPreview || runStatus != "ok" {
		t.Fatal(status, preview, runStatus)
	}
	got, _, _ := approvals.Get(d, old.ID)
	if got.Status != "superseded" {
		t.Fatal(got.Status)
	}
}

func TestPreview(t *testing.T) {
	if approvals.Preview("abc", 0) != "abc" || approvals.Preview(map[string]any{"a": 1}, 0) != `{"a":1}` || approvals.Preview(nil, 0) != "null" {
		t.Fatal("preview")
	}
	if got := approvals.Preview(strings.Repeat("x", 10), 5); got != "xxxx…" {
		t.Fatal(got)
	}
}

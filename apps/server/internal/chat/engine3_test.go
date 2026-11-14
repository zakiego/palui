package chat_test

// Ports engine.test.ts (part 3: interrupted runs, approval edge cases, hook
// delivery, text answers, the same call asked again, tool search, tools that
// decide their own approval, cancelling a message to a contact).

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func restartWith(t *testing.T, d *db.DB, steps []models.Step, extraReg map[string]*connectors.Runtime) (*chattest.Harness, *models.ScriptedModel) {
	t.Helper()
	m := models.NewScripted(steps...)
	reg := map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", m), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}
	for k, v := range extraReg {
		reg[k] = v
	}
	h := chattest.New(t, chattest.Options{DB: d, Registry: reg})
	testutil.Must(t, h.Engine.Start())
	h.Engine.Idle()
	return h, m
}

func orphan(t *testing.T, q db.Q, toolClass, trigger string) db.Chat {
	t.Helper()
	c := firstChat(t, q)
	if trigger == "" {
		trigger = "message"
	}
	insert(t, q, "runs", kv{"id", "run_orphan", "chat_id", c.ID, "trigger", trigger, "status", "running", "started_at", 1})
	insert(t, q, "messages", kv{"id", "msg_orphan", "chat_id", c.ID, "session_id", c.SessionID, "role", "user", "content", `{"text":"cek saldo"}`, "text", "cek saldo", "status", "handled", "run_id", "run_orphan", "created_at", 2})
	if toolClass != "" {
		insert(t, q, "tool_calls", kv{"id", "run_orphan:c1", "run_id", "run_orphan", "tool", "alpha__x", "class", toolClass, "status", "ok", "created_at", 3})
	}
	return c
}

func runStatus(t *testing.T, q db.Q, id string) string {
	return scalar[string](t, q, "SELECT status FROM runs WHERE id = ?", id)
}

func msgStatus(t *testing.T, q db.Q, id string) string {
	return scalar[string](t, q, "SELECT status FROM messages WHERE id = ?", id)
}

func TestInterruptedRuns(t *testing.T) {
	t.Run("re-runs the batch of a crashed run that changed nothing", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi")}, extra{})
		h.sendIdle("init")
		orphan(t, h.DB, "read", "")
		_, m := restartWith(t, h.DB, []models.Step{models.Say("saldo kamu 10k")}, nil)
		eq(t, lastText(t, h.DB), "saldo kamu 10k")
		eq(t, runStatus(t, h.DB, "run_orphan"), "error")
		eq(t, msgStatus(t, h.DB, "msg_orphan"), "handled")
		contains(t, models.PromptText(m.Prompts[0]), "cek saldo")
	})

	t.Run("skips a batch that already crashed the process twice instead of crash-looping", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi")}, extra{})
		h.sendIdle("init")
		orphan(t, h.DB, "read", "")
		for _, id := range []string{"1", "2"} {
			insert(t, h.DB, "events", kv{"at", 1, "level", "warn", "kind", "run.recovered", "message", "x", "data", `{"runId":"run_old` + id + `","messageIds":["msg_orphan"]}`})
		}
		_, m := restartWith(t, h.DB, []models.Step{models.Say("jangan dipanggil")}, nil)
		eq(t, len(m.Prompts), 0)
		contains(t, lastText(t, h.DB), "keputus berkali-kali")
		eq(t, msgStatus(t, h.DB, "msg_orphan"), "handled")
	})

	t.Run("re-runs a batch whose write call was emitted but never ran", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi")}, extra{})
		h.sendIdle("init")
		orphan(t, h.DB, "", "")
		insert(t, h.DB, "tool_calls", kv{"id", "run_orphan:c2", "run_id", "run_orphan", "tool", "alpha__x", "class", "write", "status", "pending", "created_at", 3})
		restartWith(t, h.DB, []models.Step{models.Say("saldo kamu 10k")}, nil)
		eq(t, lastText(t, h.DB), "saldo kamu 10k")
	})

	t.Run("tells the owner instead of re-running when a write tool already ran", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi")}, extra{})
		h.sendIdle("init")
		orphan(t, h.DB, "write", "")
		_, m := restartWith(t, h.DB, []models.Step{models.Say("jangan dipanggil")}, nil)
		eq(t, len(m.Prompts), 0)
		if !strings.HasPrefix(lastText(t, h.DB), "Turn tadi kepotong restart") {
			t.Fatal(lastText(t, h.DB))
		}
		eq(t, runStatus(t, h.DB, "run_orphan"), "error")
		eq(t, msgStatus(t, h.DB, "msg_orphan"), "handled")
	})

	t.Run("treats a resumed approval cut off mid-run as unsafe", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi")}, extra{})
		h.sendIdle("init")
		orphan(t, h.DB, "", "approval")
		restartWith(t, h.DB, nil, nil)
		if !strings.HasPrefix(lastText(t, h.DB), "Turn tadi kepotong restart") {
			t.Fatal(lastText(t, h.DB))
		}
	})

	t.Run("a turn stopped by a drain after a tool step is recovered on the next start", func(t *testing.T) {
		gate := chattest.NewGate()
		readRT := func() *connectors.Runtime { return toolsRT("alpha", tools("read", tool("read", ret(1)))) }
		h := basic(t, []models.Step{gate.After(models.CallTool("alpha__read", objOf(), "r1"))}, extra{registry: map[string]*connectors.Runtime{"alpha": readRT()}, rows: alphaRow})
		h.send("berapa saldo")
		chattest.Wait(20 * time.Millisecond)
		done := make(chan struct{})
		go func() { h.Engine.Stop(2_000); close(done) }()
		gate.Release()
		<-done
		run := allRuns(t, h.DB)[0]
		eq(t, run.Status, "running")
		eq(t, *run.Error, "interrupted by a restart")
		eq(t, len(outboxTexts(t, h.DB)), 0)
		_, m := restartWith(t, h.DB, []models.Step{models.Say("saldo 10k")}, map[string]*connectors.Runtime{"alpha": readRT()})
		jsonEq(t, outboxTexts(t, h.DB), `["saldo 10k"]`)
		eq(t, len(m.Prompts), 1)
	})
}

func nukeQuiet(executed *executedLog) map[string]*connectors.Runtime {
	return map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools("nuke", connectors.PaluiTool{Class: "destructive", Tool: conntest.FnTool("delete", func(in *jsonx.Object) (any, error) { executed.add(str(in, "what")); return nil, nil })}))}
}

func TestApprovalEdgeCases(t *testing.T) {
	t.Run("doesn't claim the resume while the model is off, tells the owner once, and resumes when it's back", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{models.CallTool("alpha__nuke", objOf("what", "temp"), "c1"), models.Say("udah dihapus")}, extra{registry: nukeQuiet(executed), rows: alphaRow})
		h.sendIdle("hapus temp")
		testutil.Must(t, connectors.Pause(h.DB, "cliproxy", "", 1))
		h.sendIdle("ya")
		h.Engine.ExpireApprovals(h.Clock.Now())
		h.Engine.Idle()
		eq(t, len(executed.get()), 0)
		n := 0
		for _, x := range outboxTexts(t, h.DB) {
			if strings.Contains(x, "belum aku jalanin") {
				n++
			}
		}
		eq(t, n, 1)
		if _, ok := allApprovals(t, h.DB, "")[0].Context.Get("resumedAt"); ok {
			t.Fatal("claimed")
		}
		testutil.Must(t, connectors.Resume(h.DB, "cliproxy", "", 1))
		h.Engine.ExpireApprovals(h.Clock.Now())
		h.Engine.Idle()
		jsonEq(t, executed.get(), `["temp"]`)
		eq(t, lastText(t, h.DB), "udah dihapus")
	})

	t.Run("ignores a button answer that comes from a different chat", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{models.CallTool("alpha__nuke", objOf("what", "temp"), "c1")}, extra{registry: nukeQuiet(executed), rows: alphaRow})
		h.sendIdle("hapus temp")
		a := allApprovals(t, h.DB, "")[0]
		h.sendIdle("", func(m *connectors.InboundMessage) {
			m.ChatExternalID, m.ChatKind = "-100", "group"
			m.Callback = &connectors.InboundCallback{Data: "approve:" + a.ID}
		})
		eq(t, len(executed.get()), 0)
		eq(t, allApprovals(t, h.DB, "")[0].Status, "pending")
		eq(t, lastText(t, h.DB), "Persetujuan itu gak ketemu.")
	})
}

func TestHookDelivery(t *testing.T) {
	t.Run("forwards the text when no model is on for a summary, and marks it handled after", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi")}, extra{})
		h.sendIdle("init")
		testutil.Must(t, connectors.Pause(h.DB, "cliproxy", "", 1))
		insertHook(t, h, "msg_hook_s", "summarize", "deploy ok")
		need(h.Engine.ReplayPending())
		h.Engine.Idle()
		eq(t, lastText(t, h.DB), "deploy ok")
		eq(t, msgStatus(t, h.DB, "msg_hook_s"), "handled")
	})

	t.Run("keeps a hook pending when a drain stops it before its summary ran, and summarizes it after the restart", func(t *testing.T) {
		never := chattest.NewGate()
		h := basic(t, []models.Step{models.Say("hi"), never.After(models.Say("never"))}, extra{})
		h.sendIdle("init")
		h.send("tulis esai panjang")
		chattest.Wait(20 * time.Millisecond)
		insertHook(t, h, "msg_hook_d", "summarize", "deploy ok")
		need(h.Engine.ReplayPending())
		h.Engine.Stop(50)
		eq(t, msgStatus(t, h.DB, "msg_hook_d"), "pending")
		m := models.NewScripted(models.Say("jawaban esai"), models.Say("Ringkasan: deploy sukses."))
		h2 := chattest.New(t, chattest.Options{DB: h.DB, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", m), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}})
		testutil.Must(t, h2.Engine.Start())
		h2.Engine.Idle()
		if !has(outboxTexts(t, h.DB), "Ringkasan: deploy sukses.") {
			t.Fatal(outboxTexts(t, h.DB))
		}
		eq(t, msgStatus(t, h.DB, "msg_hook_d"), "handled")
	})

	t.Run("dispatches a summarize hook once even when the app pokes twice", func(t *testing.T) {
		gate := chattest.NewGate()
		h := basic(t, []models.Step{models.Say("hi"), gate.After(models.Say("Ringkasan."))}, extra{})
		h.sendIdle("init")
		insertHook(t, h, "msg_hook_twice", "summarize", "deploy ok")
		need(h.Engine.ReplayPending())
		need(h.Engine.ReplayPending())
		gate.Release()
		h.Engine.Idle()
		n := 0
		for _, x := range outboxTexts(t, h.DB) {
			if x == "Ringkasan." {
				n++
			}
		}
		eq(t, n, 1)
		eq(t, len(h.m.Prompts), 2)
	})
}

func twoCalls() models.Step {
	u := models.Usage{InputTokens: models.InputTokens{Total: i64p(1), NoCache: i64p(1)}, OutputTokens: models.OutputTokens{Total: i64p(1), Text: i64p(1)}}
	return models.Step{Events: []models.Event{
		{Type: models.EventToolCall, ToolCallID: "c1", ToolName: "alpha__nuke", Input: `{"what":"a"}`},
		{Type: models.EventToolCall, ToolCallID: "c2", ToolName: "alpha__nuke", Input: `{"what":"b"}`},
		{Type: models.EventFinish, FinishReason: models.FinishReason{Unified: "tool-calls"}, Usage: u},
	}}
}

func i64p(v int64) *int64 { return &v }

func TestApprovalAnswersByText(t *testing.T) {
	t.Run("a bare 'ya' answers every pending request of the newest run and says so", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{twoCalls(), models.Say("dua-duanya beres")}, extra{registry: nukeQuiet(executed), rows: alphaRow})
		h.sendIdle("hapus a dan b")
		eq(t, count(t, h.DB, "pending_approvals"), 2)
		h.sendIdle("ya")
		got := executed.get()
		if len(got) != 2 || !has(got, "a") || !has(got, "b") {
			t.Fatal(got)
		}
		if !has(outboxTexts(t, h.DB), `Oke, "ya" aku pakai buat 2 aksi yang terakhir ditanya: alpha › nuke, alpha › nuke.`) {
			t.Fatal(outboxTexts(t, h.DB))
		}
		eq(t, lastText(t, h.DB), "dua-duanya beres")
	})

	t.Run("a 'ya' quoting some other message isn't taken as an approval", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{twoCalls(), models.Say("oke")}, extra{registry: nukeQuiet(executed), rows: alphaRow})
		h.sendIdle("hapus a dan b")
		h.sendIdle("ya", func(m *connectors.InboundMessage) { m.ReplyToChannelMsgID = "some-other-message" })
		eq(t, len(executed.get()), 0)
		eq(t, count(t, h.DB, "pending_approvals WHERE status = 'pending'"), 2)
	})

	t.Run("a reply quoting a prompt answers that one when several runs wait", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{models.CallTool("alpha__nuke", objOf("what", "a"), "c1"), models.CallTool("alpha__nuke", objOf("what", "b"), "c2"), models.Say("a dihapus")}, extra{registry: nukeQuiet(executed), rows: alphaRow})
		h.sendIdle("hapus a")
		h.sendIdle("hapus b")
		a := allApprovals(t, h.DB, "tool_call_id = 'c1'")[0]
		b := allApprovals(t, h.DB, "tool_call_id = 'c2'")[0]
		_, err := h.DB.Exec("UPDATE outbox SET channel_msg_id = 'wa-prompt-a' WHERE source = ?", "approval:"+a.ID)
		testutil.Must(t, err)
		h.sendIdle("ya", func(m *connectors.InboundMessage) { m.ReplyToChannelMsgID = "wa-prompt-a" })
		jsonEq(t, executed.get(), `["a"]`)
		eq(t, allApprovals(t, h.DB, "id = ?", b.ID)[0].Status, "pending")
	})
}

func TestTheSameCallAskedAgain(t *testing.T) {
	alpha := func(executed *executedLog, cls func() string) map[string]*connectors.Runtime {
		return map[string]*connectors.Runtime{"alpha": toolsRT("alpha", func() *connectors.ToolSet {
			return conntest.Tools("nuke", connectors.PaluiTool{Class: cls(), Tool: conntest.FnTool("delete", func(in *jsonx.Object) (any, error) {
				executed.add(str(in, "what"))
				return objOf("deleted", true), nil
			})})
		})}
	}
	destructive := func() string { return "destructive" }
	pendingIDs := func(q db.Q) string {
		var ids []string
		for _, r := range allApprovals(t, q, "status = 'pending'") {
			ids = append(ids, *r.ToolCallID)
		}
		return jsonx.Stringify(ids)
	}

	t.Run("drops the older request, so only the newest one is live and only it expires with a follow-up", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{models.CallTool("alpha__nuke", objOf("what", "temp"), "c1"), models.CallTool("alpha__nuke", objOf("what", "temp"), "c2"), models.Say("oke, gak jadi")}, extra{registry: alpha(executed, destructive), rows: alphaRow})
		h.sendIdle("hapus temp")
		first := allApprovals(t, h.DB, "")[0]
		h.sendIdle("hapus temp")
		eq(t, allApprovals(t, h.DB, "id = ?", first.ID)[0].Status, "superseded")
		eq(t, pendingIDs(h.DB), `["c2"]`)
		eq(t, runStatus(t, h.DB, first.RunID), "ok")
		tc := need(db.ScanToolCall(h.DB.QueryRow("SELECT "+db.ToolCallCols+" FROM tool_calls WHERE id = ?", first.RunID+":c1")))
		eq(t, tc.Status, "denied")
		eq(t, *tc.ResultPreview, "dropped: the same call was made again before the owner answered")
		hit := false
		for _, e := range listEvents(t, h.DB) {
			if e.Kind == "approval.superseded" {
				hit = true
			}
		}
		eq(t, hit, true)
		h.sendIdle("", func(m *connectors.InboundMessage) {
			m.Callback = &connectors.InboundCallback{Data: "approve:" + first.ID}
		})
		eq(t, len(executed.get()), 0)
		eq(t, lastText(t, h.DB), "Itu udah dijawab atau udah kedaluwarsa.")
		h.Engine.ExpireApprovals(h.Clock.Now() + 10*60_000)
		h.Engine.Idle()
		eq(t, len(h.m.Prompts), 3)
		n := 0
		for _, x := range outboxTexts(t, h.DB) {
			if x == "oke, gak jadi" {
				n++
			}
		}
		eq(t, n, 1)
	})

	t.Run("drops the older request when the call now runs without asking", func(t *testing.T) {
		executed := &executedLog{}
		var mu sync.Mutex
		cls := "destructive"
		get := func() string { mu.Lock(); defer mu.Unlock(); return cls }
		h := basic(t, []models.Step{models.CallTool("alpha__nuke", objOf("what", "temp"), "c1"), models.CallTool("alpha__nuke", objOf("what", "temp"), "c2"), models.Say("udah")}, extra{registry: alpha(executed, get), rows: alphaRow})
		h.sendIdle("hapus temp")
		mu.Lock()
		cls = "read"
		mu.Unlock()
		h.sendIdle("hapus temp")
		jsonEq(t, executed.get(), `["temp"]`)
		eq(t, lastText(t, h.DB), "udah")
		contains(t, prompt(h.m, 1), "Not run yet: still waiting for the owner's approval.")
		eq(t, allApprovals(t, h.DB, "")[0].Status, "superseded")
		h.Engine.ExpireApprovals(h.Clock.Now() + 10*60_000)
		h.Engine.Idle()
		eq(t, len(h.m.Prompts), 3)
		eq(t, lastText(t, h.DB), "udah")
	})

	t.Run("leaves a request for other arguments alone, whatever the key order", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{models.CallTool("alpha__nuke", objOf("what", "a", "force", true), "c1"), models.CallTool("alpha__nuke", objOf("what", "b", "force", true), "c2"), models.CallTool("alpha__nuke", objOf("force", true, "what", "a"), "c3")}, extra{registry: alpha(executed, destructive), rows: alphaRow})
		h.sendIdle("hapus a")
		h.sendIdle("hapus b")
		eq(t, pendingIDs(h.DB), `["c1","c2"]`)
		h.sendIdle("hapus a")
		eq(t, pendingIDs(h.DB), `["c2","c3"]`)
	})

	t.Run("still resumes a run whose other request was already answered", func(t *testing.T) {
		executed := &executedLog{}
		both := models.CallTools(models.ScriptedCall{ToolName: "alpha__nuke", Input: objOf("what", "a"), ToolCallID: "c1"}, models.ScriptedCall{ToolName: "alpha__nuke", Input: objOf("what", "b"), ToolCallID: "c2"})
		h := basic(t, []models.Step{both, models.CallTool("alpha__nuke", objOf("what", "b"), "c3"), models.Say("a kehapus")}, extra{registry: alpha(executed, destructive), rows: alphaRow})
		h.sendIdle("hapus a dan b")
		a := allApprovals(t, h.DB, "tool_call_id = 'c1'")[0]
		eq(t, h.Engine.DecideApproval(a.ID, true, "dashboard"), true)
		h.Engine.Idle()
		eq(t, len(executed.get()), 0)
		h.sendIdle("hapus b")
		jsonEq(t, executed.get(), `["a"]`)
		contains(t, prompt(h.m, 2), "Dropped: the same call was made again later")
		eq(t, lastText(t, h.DB), "a kehapus")
		eq(t, pendingIDs(h.DB), `["c3"]`)
	})
}

func readTool(desc string) connectors.PaluiTool {
	return connectors.PaluiTool{Class: "read", Tool: conntest.FnTool(desc, ret(desc))}
}

func TestToolSearch(t *testing.T) {
	skill := func() *connectors.Runtime {
		return conntest.ToolsRuntime("notes", tools("save_instruction", readTool("Save a standing instruction"), "list_instructions", readTool("List instructions"), "delete_instruction", readTool("Delete an instruction")), "skill", nil)
	}
	sholat := func(calls *executedLog) *connectors.Runtime {
		return toolsRT("mcp-jadwal-sholat", tools(
			"search_cities", readTool("Search cities"),
			"get_prayer_times", connectors.PaluiTool{Class: "read", Tool: conntest.FnTool("Prayer times for a city and date", func(in *jsonx.Object) (any, error) { calls.add(str(in, "city")); return "Subuh 04:30", nil })},
			"list_methods", readTool("List calculation methods"),
		))
	}
	setup := func(t *testing.T, steps []models.Step, calls *executedLog, more map[string]*connectors.Runtime) *world {
		reg := map[string]*connectors.Runtime{"notes": skill(), "mcp-jadwal-sholat": sholat(calls)}
		rows := []chattest.Row{{"notes", "skill"}, {"mcp-jadwal-sholat", "tools"}}
		for id, rt := range more {
			reg[id] = rt
			rows = append(rows, chattest.Row{id, "tools"})
		}
		h := basic(t, steps, extra{registry: reg, rows: rows})
		setBudget(t, h.DB, func(b *settings.Budgets) { b.MaxTools = 4 })
		return h
	}

	t.Run("finds a remote MCP tool over the cap and calls it in the same turn", func(t *testing.T) {
		calls := &executedLog{}
		h := setup(t, []models.Step{models.CallTool("palui__search_tools", objOf("query", "jadwal sholat"), ""), models.CallTool("mcp-jadwal-sholat__get_prayer_times", objOf("city", "Banjarbaru"), ""), models.Say("Subuh 04:30")}, calls, nil)
		h.sendIdle("jadwal sholat banjarbaru")
		jsonEq(t, h.m.Offered[0], `["notes__save_instruction","notes__list_instructions","notes__delete_instruction","palui__search_tools"]`)
		if !has(h.m.Offered[1], "mcp-jadwal-sholat__get_prayer_times") {
			t.Fatal(h.m.Offered[1])
		}
		jsonEq(t, calls.get(), `["Banjarbaru"]`)
		p := prompt(h.m, 0)
		contains(t, p, "jadwal-sholat (3 to load with palui__search_tools: search_cities, get_prayer_times, list_methods)")
		contains(t, p, "Never tell the owner a tool or capability doesn't exist without searching first.")
	})

	t.Run("keeps loaded tools for the next turns of the session", func(t *testing.T) {
		h := setup(t, []models.Step{models.CallTool("palui__search_tools", objOf("names", []string{"get_prayer_times"}), ""), models.Say("ok"), models.Say("lagi")}, &executedLog{}, nil)
		h.sendIdle("jadwal sholat")
		h.sendIdle("besok?")
		next := h.m.Offered[len(h.m.Offered)-1]
		if !has(next, "mcp-jadwal-sholat__get_prayer_times") || !has(next, "palui__search_tools") || len(next) != 4 {
			t.Fatal(next)
		}
	})

	t.Run("never finds tools of a disabled connector or a disabled tool", func(t *testing.T) {
		off := toolsRT("mcp-off", tools("prayer_alarm", readTool("Prayer alarm")))
		h := setup(t, []models.Step{models.CallTool("palui__search_tools", objOf("query", "prayer alarm"), ""), models.Say("ok")}, &executedLog{}, map[string]*connectors.Runtime{"mcp-off": off})
		testutil.Must(t, connectors.Pause(h.DB, "mcp-off", "", 1))
		h.Sup.Reconcile()
		h.sendIdle("alarm sholat")
		res := need(db.ScanToolCall(h.DB.QueryRow("SELECT " + db.ToolCallCols + " FROM tool_calls WHERE tool = 'palui__search_tools'")))
		notContains(t, *res.ResultPreview, "prayer_alarm")
		notContains(t, prompt(h.m, 0), "mcp-off")
	})

	t.Run("tells a flow search each tool's class and trust, and leaves destructive ones out", func(t *testing.T) {
		ops := toolsRT("mcp-ops", tools(
			"prayer_wipe", connectors.PaluiTool{Class: "destructive", Tool: conntest.FnTool("Wipe prayer data", ret("ok"))},
			"prayer_log", connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("Log a prayer", ret("ok"))},
		))
		h := setup(t, []models.Step{models.CallTool("palui__search_tools", objOf("query", "prayer", "for", "flow"), ""), models.Say("ok")}, &executedLog{}, map[string]*connectors.Runtime{"mcp-ops": ops})
		h.sendIdle("cari tool sholat buat flow")
		res := need(db.ScanToolCall(h.DB.QueryRow("SELECT " + db.ToolCallCols + " FROM tool_calls WHERE tool = 'palui__search_tools'")))
		contains(t, *res.ResultPreview, `"class":"write","untrusted"`)
		notContains(t, *res.ResultPreview, "prayer_wipe")
	})

	t.Run("loads everything up front when the tools fit", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("ok")}, extra{registry: map[string]*connectors.Runtime{"notes": skill(), "mcp-jadwal-sholat": sholat(&executedLog{})}, rows: []chattest.Row{{"notes", "skill"}, {"mcp-jadwal-sholat", "tools"}}})
		h.sendIdle("halo")
		eq(t, len(h.m.Offered[0]), 6)
		if has(h.m.Offered[0], "palui__search_tools") {
			t.Fatal(h.m.Offered[0])
		}
		contains(t, prompt(h.m, 0), "jadwal-sholat (3 tools)")
	})
}

func TestToolsThatDecideTheirOwnApproval(t *testing.T) {
	registry := func(sent *executedLog, seen *[]connectors.TurnInfo, mu *sync.Mutex, ask bool) map[string]*connectors.Runtime {
		return map[string]*connectors.Runtime{"send": conntest.ToolsRuntime("send", tools(), "", func(h *connectors.RunningConnector) {
			h.Tools = func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				mu.Lock()
				*seen = append(*seen, turn)
				mu.Unlock()
				return conntest.Tools("msg", connectors.PaluiTool{
					Class:         "write",
					Tool:          conntest.FnTool("m", func(in *jsonx.Object) (any, error) { sent.add(str(in, "text")); return nil, nil }),
					NeedsApproval: func(json.RawMessage) bool { return ask },
					Preview: func(in json.RawMessage) string {
						var v struct{ Text string }
						_ = json.Unmarshal(in, &v)
						return "Kirim ke Zaki:\n" + v.Text
					},
				}), nil
			}
		})}
	}
	rows := []chattest.Row{{"send", "tools"}}

	t.Run("asks when needsApproval says so, with the tool's own preview", func(t *testing.T) {
		sent := &executedLog{}
		var seen []connectors.TurnInfo
		var mu sync.Mutex
		h := basic(t, []models.Step{models.CallTool("send__msg", objOf("text", "gue otw"), "s1")}, extra{registry: registry(sent, &seen, &mu, true), rows: rows})
		h.sendIdle("chat Zaki bilang gue otw")
		eq(t, len(sent.get()), 0)
		p := allApprovals(t, h.DB, "")[0]
		eq(t, p.Tool, "send__msg")
		eq(t, *p.ArgsPreview, "Kirim ke Zaki:\ngue otw")
		contains(t, lastText(t, h.DB), "Kirim ke Zaki:\ngue otw")
		eq(t, seen[0].Origin, "message")
		eq(t, seen[0].Mode, "owner")
		eq(t, *seen[0].OwnerText, "chat Zaki bilang gue otw")
		eq(t, seen[0].Tainted(), false)
	})

	t.Run("asks even when the agent may run every tool without asking", func(t *testing.T) {
		sent := &executedLog{}
		var seen []connectors.TurnInfo
		var mu sync.Mutex
		h := basic(t, []models.Step{models.CallTool("send__msg", objOf("text", "gue otw"), "s1")}, extra{registry: registry(sent, &seen, &mu, true), rows: rows})
		allowAll(t, h.DB, "", nil)
		h.sendIdle("chat Zaki bilang gue otw")
		eq(t, len(sent.get()), 0)
		eq(t, allApprovals(t, h.DB, "")[0].Tool, "send__msg")
	})

	t.Run("runs without asking when needsApproval says no in an untainted owner turn", func(t *testing.T) {
		sent := &executedLog{}
		var seen []connectors.TurnInfo
		var mu sync.Mutex
		h := basic(t, []models.Step{models.CallTool("send__msg", objOf("text", "gue otw"), "s1")}, extra{registry: registry(sent, &seen, &mu, false), rows: rows})
		h.sendIdle("chat Zaki bilang gue otw")
		jsonEq(t, sent.get(), `["gue otw"]`)
		eq(t, count(t, h.DB, "pending_approvals"), 0)
	})
}

// fakeOutbound stands in for outbound/service.ts (M4): one held send to "Zaki"
// whose outbox row a cancel marks cancelled.
type fakeOutbound struct {
	id, ownerChatID, outboxID string
	cancelled                 bool
}

func (f *fakeOutbound) OwnerChatID(q db.Q, id string) (string, bool, error) {
	if id != f.id {
		return "", false, nil
	}
	return f.ownerChatID, true, nil
}

func (f *fakeOutbound) Cancel(q db.Q, id string, now int64) (string, error) {
	f.cancelled = true
	_, err := q.Exec("UPDATE outbox SET status = 'cancelled' WHERE id = ?", f.outboxID)
	return "Oke, pesan ke Zaki dibatalin.", err
}

func (f *fakeOutbound) HeldFromNotice(q db.Q, chatID, channelMsgID string) (string, error) {
	return "", nil
}

func (f *fakeOutbound) NewestHeld(q db.Q, chatID string, now int64) (string, error) {
	if f.cancelled || chatID != f.ownerChatID {
		return "", nil
	}
	return f.id, nil
}

func TestRouterCancelsAMessageToAContact(t *testing.T) {
	held := func(t *testing.T) (*world, *fakeOutbound) {
		fo := &fakeOutbound{id: "obm_1"}
		h := basic(t, nil, extra{deps: chat.Deps{Outbound: fo}})
		h.sendIdle("init")
		c := firstChat(t, h.DB)
		fo.ownerChatID = c.ID
		insert(t, h.DB, "outbox", kv{"id", "out_held", "chat_id", c.ID, "channel", "whatsapp", "payload", `{"text":"otw"}`, "next_attempt_at", h.Clock.Now() + 15_000, "source", "outbound:obm_1", "created_at", h.Clock.Now()})
		fo.outboxID = "out_held"
		return h, fo
	}
	status := func(t *testing.T, q db.Q) string {
		return scalar[string](t, q, "SELECT status FROM outbox WHERE id = 'out_held'")
	}

	t.Run("a bare batal cancels the newest held send", func(t *testing.T) {
		h, _ := held(t)
		h.sendIdle("batal")
		eq(t, status(t, h.DB), "cancelled")
		eq(t, lastText(t, h.DB), "Oke, pesan ke Zaki dibatalin.")
	})
	t.Run("the cancel button cancels it", func(t *testing.T) {
		h, fo := held(t)
		h.sendIdle("", func(m *connectors.InboundMessage) {
			m.Callback = &connectors.InboundCallback{Data: "ob:cancel:" + fo.id}
		})
		eq(t, status(t, h.DB), "cancelled")
	})
}

var _ = regexp.MustCompile

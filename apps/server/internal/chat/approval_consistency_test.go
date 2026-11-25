package chat_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
)

// The same tool on the same agent gets the same answer in the owner's turn,
// whatever the conversation read before. These replay the reports: a sandbox
// run that asked only after an untrusted read in the same turn, and writes in
// a DM that asked for as long as an untrusted call sat in the history.
func TestApprovalsDontDependOnWhatTheConversationRead(t *testing.T) {
	untrusted := connectors.PaluiTool{Class: "read", Untrusted: true, Tool: conntest.FnTool("search", ret("someone else wrote this"))}
	others := connectors.PaluiTool{Class: "read", Untrusted: true, OthersWrote: true, Tool: conntest.FnTool("read chats", ret("another agent's chat"))}

	t.Run("a write runs before and after an untrusted read, in the same turn and in later ones", func(t *testing.T) {
		ran := &executedLog{}
		h := basic(t, []models.Step{
			models.CallTool("alpha__start", objOf("task", "a"), "w1"), models.Say("jalan"),
			models.CallTool("web__search", objOf(), "s1"), models.CallTool("alpha__start", objOf("task", "b"), "w2"), models.Say("jalan"),
			models.CallTool("alpha__start", objOf("task", "c"), "w3"), models.Say("jalan"),
		}, extra{registry: map[string]*connectors.Runtime{
			"web": toolsRT("web", tools("search", untrusted)),
			"alpha": toolsRT("alpha", tools("start", connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("start", func(in *jsonx.Object) (any, error) {
				ran.add(str(in, "task"))
				return "ok", nil
			})})),
		}, rows: []chattest.Row{{"web", "tools"}, {"alpha", "tools"}}})
		h.sendIdle("mulai a")
		h.sendIdle("cari lalu mulai b")
		h.sendIdle("mulai c")
		jsonEq(t, ran.get(), `["a","b","c"]`)
		eq(t, count(t, h.DB, "pending_approvals"), 0)
	})

	t.Run("a read that carries a saved file runs after an untrusted read in the same turn", func(t *testing.T) {
		ran := &executedLog{}
		h := basic(t, []models.Step{
			models.CallTool("sandbox__run", objOf("files", []any{objOf("name", "sk.docx", "content", "palui:attachment/att_one")}), "r1"), models.Say("dibaca"),
			models.CallTool("web__search", objOf(), "s1"), models.CallTool("sandbox__run", objOf("files", []any{objOf("name", "sk.docx", "content", "palui:attachment/att_one")}), "r2"), models.Say("diubah"),
		}, extra{registry: map[string]*connectors.Runtime{
			"web":     toolsRT("web", tools("search", untrusted)),
			"sandbox": toolsRT("sandbox", tools("run", connectors.PaluiTool{Class: "read", Tool: conntest.FnTool("run", func(*jsonx.Object) (any, error) { ran.add("run"); return "ok", nil })})),
		}, rows: []chattest.Row{{"web", "tools"}, {"sandbox", "tools"}}})
		h.sendIdle("baca docx-nya")
		h.sendIdle("cari, terus ubah kelurahan jadi desa")
		jsonEq(t, ran.get(), `["run","run"]`)
		eq(t, count(t, h.DB, "pending_approvals"), 0)
	})

	t.Run("an owner reply and a write run after reading other people's chats", func(t *testing.T) {
		ran := &executedLog{}
		h := basic(t, []models.Step{
			models.CallTool("chats__list", objOf(), "l1"), models.Say("ini daftarnya"),
			models.CallTool("flows__answer", objOf("answer", "done"), "a1"), models.Say("dicatat"),
			models.CallTool("alpha__start", objOf("task", "x"), "w1"), models.Say("jalan"),
		}, extra{registry: map[string]*connectors.Runtime{
			"chats": toolsRT("chats", tools("list", others)),
			"flows": toolsRT("flows", tools("answer", connectors.PaluiTool{Class: "write", OwnerReply: true, Tool: conntest.FnTool("answer", func(*jsonx.Object) (any, error) { ran.add("answer"); return nil, nil })})),
			"alpha": toolsRT("alpha", tools("start", connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("start", func(*jsonx.Object) (any, error) { ran.add("start"); return nil, nil })})),
		}, rows: []chattest.Row{{"chats", "tools"}, {"flows", "tools"}, {"alpha", "tools"}}})
		h.sendIdle("chat apa aja hari ini?")
		h.sendIdle("udah")
		h.sendIdle("mulai x")
		jsonEq(t, ran.get(), `["answer","start"]`)
		eq(t, count(t, h.DB, "pending_approvals"), 0)
	})

	t.Run("a destructive tool asks every time with the same reason, and an expired request changes nothing", func(t *testing.T) {
		executed := &executedLog{}
		h := basic(t, []models.Step{
			models.CallTool("alpha__nuke", objOf("what", "temp"), "c1"), models.Say("oke, gak jadi"),
			models.Say("tadi belum jalan"),
			models.CallTool("web__search", objOf(), "s1"), models.CallTool("alpha__nuke", objOf("what", "temp"), "c2"),
		}, extra{registry: map[string]*connectors.Runtime{
			"web":   toolsRT("web", tools("search", untrusted)),
			"alpha": nukeRegistry(executed)["alpha"],
		}, rows: []chattest.Row{{"web", "tools"}, {"alpha", "tools"}}})
		h.sendIdle("hapus temp")
		first := allApprovals(t, h.DB, "")[0]
		eq(t, *first.Reason, "destructive")
		// The owner hears why, and where to change it.
		prompts := outboxTexts(t, h.DB)
		contains(t, prompts[len(prompts)-1], "Kenapa nanya: ini tool destruktif")
		contains(t, prompts[len(prompts)-1], "/app/agents/")
		contains(t, prompts[len(prompts)-1], "?tab=tools")

		h.Engine.ExpireApprovals(h.Clock.Now() + 10*60_000)
		h.Engine.Idle()
		eq(t, allApprovals(t, h.DB, "")[0].Status, "expired")
		// The model hears it on the resume, so it can tell the owner.
		resumed := prompt(h.m, 1)
		contains(t, resumed, "No answer from the owner within 10 minutes")
		contains(t, resumed, "Palui asked the owner first because it is a destructive tool")
		contains(t, resumed, "?tab=tools")

		// And again in the next turn's history.
		h.sendIdle("kenapa harus nanya?")
		next := prompt(h.m, 2)
		contains(t, next, "Not run: the owner didn't answer within 10 minutes. Palui asked the owner first because it is a destructive tool")

		h.sendIdle("hapus temp lagi")
		eq(t, len(executed.get()), 0)
		second := allApprovals(t, h.DB, "tool_call_id = 'c2'")[0]
		eq(t, *second.Reason, "destructive")
		eq(t, second.Status, "pending")
	})

	t.Run("with ask-after-untrusted on, the reason says so in the history", func(t *testing.T) {
		h := basic(t, []models.Step{
			models.CallTool("web__search", objOf(), "s1"), models.CallTool("alpha__start", objOf("task", "a"), "w1"),
			models.Say("masih nunggu"),
		}, extra{registry: map[string]*connectors.Runtime{
			"web":   toolsRT("web", tools("search", untrusted)),
			"alpha": toolsRT("alpha", tools("start", connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("start", ret("ok"))})),
		}, rows: []chattest.Row{{"web", "tools"}, {"alpha", "tools"}}})
		askAfterUntrusted(t, h.DB, "")
		h.sendIdle("cari lalu mulai a")
		eq(t, *allApprovals(t, h.DB, "")[0].Reason, "untrusted")
		contains(t, outboxTexts(t, h.DB)[0], "Kenapa nanya: obrolan ini tadi baca konten dari luar")
		h.sendIdle("kenapa nanya?")
		contains(t, prompt(h.m, 2), "Not run yet: still waiting for the owner's approval. Palui asked the owner first because this conversation read untrusted content earlier")
	})
}

package chat_test

// Chat rules from a members chat: only members the owner allowed get the
// tools, the rule lands in that chat's prompt only, and the owner hears about
// it without the member's text. Fake names and numbers only.

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/chatrules"
	notesconn "github.com/zakiego/palui/apps/server/internal/connectors/notes"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func rulesWorld(t *testing.T, steps []models.Step, allow bool) (*agentWorld, db.Chat, string) {
	t.Helper()
	reg := memberRegistry(&executedLog{})
	reg["notes"] = notesconn.Runtime
	rows := append(append([]chattest.Row{}, memberRows...), chattest.Row{"notes", "notes"})
	h := membersWorld(t, steps, reg, rows)
	var aniID string
	testutil.Must(t, h.DB.QueryRow("SELECT id FROM members WHERE name = 'Ani'").Scan(&aniID))
	if allow {
		testutil.Must(t, members.SetRules(h.DB, h.chef.ID, aniID, true))
	}
	return h, chatWhere(t, h.DB, "external_id = ?", tgMember), aniID
}

func TestChatRulesFromMembers(t *testing.T) {
	t.Run("an allowed member saves a rule for their chat, the owner is told without the text", func(t *testing.T) {
		h, dm, aniID := rulesWorld(t, []models.Step{
			models.CallTool("notes__save_chat_rule", objOf("title", "Tanggal", "body", "Jangan bahas tanggal lagi."), "c1"),
			models.Say("oke"),
			models.Say("siap"),
		}, true)
		h.sendIdle("jangan bahas tanggal lagi ya", memberDM)
		if !has(h.m.Offered[0], "notes__save_chat_rule") || !has(h.m.Offered[0], "notes__delete_chat_rule") {
			t.Fatal(h.m.Offered[0])
		}
		if has(h.m.Offered[0], "notes__save_instruction") {
			t.Fatal("a member must never get save_instruction", h.m.Offered[0])
		}
		contains(t, prompt(h.m, 0), "Everyone in this batch may set rules for this chat")
		rules := need(chatrules.List(h.DB, h.chef.ID, dm.ID))
		eq(t, len(rules), 1)
		eq(t, rules[0].MemberID != nil && *rules[0].MemberID == aniID, true)
		eq(t, rules[0].AuthorName, "Ani")
		changes := need(chatrules.Changes(h.DB, h.chef.ID))
		eq(t, len(changes), 1)
		eq(t, changes[0].ByName, "Ani")
		eq(t, count(t, h.DB, "instructions"), 0)
		// The owner hears who changed what where, never the rule's text.
		var text string
		testutil.Must(t, h.DB.QueryRow("SELECT json_extract(payload, '$.text') FROM outbox WHERE source = 'system' AND chat_id != ? ORDER BY created_at DESC LIMIT 1", dm.ID).Scan(&text))
		contains(t, text, "Ani nambah aturan baru")
		notContains(t, text, "tanggal")
		// The next turn in that chat carries the rule under its own header.
		h.sendIdle("halo lagi", memberDM)
		p := prompt(h.m, -1)
		contains(t, p, chatrules.PromptHeader)
		contains(t, p, "- Tanggal (by Ani): Jangan bahas tanggal lagi.")
		notContains(t, p, "Standing instructions from the owner")
	})

	t.Run("a member the owner didn't allow gets no rule tools", func(t *testing.T) {
		h, _, _ := rulesWorld(t, []models.Step{models.Say("gak bisa")}, false)
		h.sendIdle("jangan bahas tanggal lagi ya", memberDM)
		for _, n := range []string{"notes__save_chat_rule", "notes__delete_chat_rule", "notes__list_chat_rules"} {
			if has(h.m.Offered[0], n) {
				t.Fatal(n, h.m.Offered[0])
			}
		}
		notContains(t, prompt(h.m, 0), "may set rules for this chat")
	})

	t.Run("a member can't remove the owner's rule", func(t *testing.T) {
		h, dm, _ := rulesWorld(t, []models.Step{models.CallTool("notes__delete_chat_rule", objOf("id", "cru_owner"), "c1"), models.Say("gak bisa")}, true)
		_, err := h.DB.Exec("INSERT INTO chat_rules (id, agent_id, chat_id, title, body, member_id, author_name, created_at) VALUES ('cru_owner', ?, ?, 'Bahasa', 'Jawab pakai bahasa Indonesia.', NULL, 'Owner', 1)", h.chef.ID, dm.ID)
		testutil.Must(t, err)
		h.sendIdle("hapus aturan bahasa", memberDM)
		eq(t, len(need(chatrules.List(h.DB, h.chef.ID, dm.ID))), 1)
		contains(t, prompt(h.m, -1), "cuma owner yang bisa hapus")
	})

	t.Run("chat rules stay out of the owner's own chat", func(t *testing.T) {
		h, dm, _ := rulesWorld(t, []models.Step{models.Say("ok")}, true)
		need(chatrules.Add(h.DB, h.chef.ID, dm.ID, "Tanggal", "Jangan bahas tanggal lagi.", chatrules.By{Name: "Ani", MemberID: sp("mbr_x")}, "", 1))
		h.sendIdle("halo")
		p := prompt(h.m, -1)
		notContains(t, p, chatrules.PromptHeader)
		if has(h.m.Offered[0], "notes__save_chat_rule") {
			t.Fatal(h.m.Offered[0])
		}
	})
}

func TestRuleSetterFor(t *testing.T) {
	d := testutil.DB(t)
	agent := db.Agent{ID: "agt_x"}
	group := db.Chat{ID: "chat_g", Channel: "whatsapp", Kind: "group"}
	ani := need(members.Create(d, "Ani", []members.AccountInput{{Channel: "whatsapp", Address: "6281200000001"}}, 1))
	budi := need(members.Create(d, "Budi", []members.AccountInput{{Channel: "whatsapp", Address: "6281200000002"}}, 1))
	_, err := d.Exec("INSERT INTO agent_members (agent_id, member_id, created_at, can_set_rules) VALUES ('agt_x', ?, 1, 1), ('agt_x', ?, 1, 0)", ani.ID, budi.ID)
	testutil.Must(t, err)
	aniJID, budiJID := "6281200000001@s.whatsapp.net", "6281200000002@s.whatsapp.net"
	msg := func(id string, owner bool, sender string) db.Message {
		m := db.Message{ID: id, Role: "user", FromOwner: owner}
		if sender != "" {
			m.SenderID = &sender
		}
		return m
	}
	batch := map[string]bool{"m1": true, "m2": true}
	cases := []struct {
		name     string
		audience string
		origin   string
		resumed  bool
		history  []db.Message
		want     string
	}{
		{"allowed member", "members", "message", false, []db.Message{msg("m1", false, aniJID)}, "Ani"},
		{"owner then allowed member names the member", "members", "message", false, []db.Message{msg("m1", true, ""), msg("m2", false, aniJID)}, "Ani"},
		{"owner alone", "members", "message", false, []db.Message{msg("m1", true, "")}, "Owner"},
		{"a member without the switch in the batch", "members", "message", false, []db.Message{msg("m1", false, aniJID), msg("m2", false, budiJID)}, ""},
		{"a stranger in the batch", "members", "message", false, []db.Message{msg("m1", false, aniJID), msg("m2", false, "6281200000009@s.whatsapp.net")}, ""},
		{"outside chat", "outside", "message", false, []db.Message{msg("m1", false, aniJID)}, ""},
		{"owner's own chat", "owner", "message", false, []db.Message{msg("m1", true, "")}, ""},
		{"not a message turn", "members", "schedule", false, []db.Message{msg("m1", false, aniJID)}, ""},
		{"approval resume", "members", "message", true, []db.Message{msg("m1", false, aniJID)}, ""},
		{"history outside the batch doesn't count", "members", "message", false, []db.Message{msg("old", false, budiJID), msg("m1", false, aniJID)}, "Ani"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chat.RuleSetterFor(d, group, agent, c.audience, c.origin, c.resumed, c.history, batch)
			testutil.Must(t, err)
			name := ""
			if got != nil {
				name = got.Name
			}
			eq(t, name, c.want)
		})
	}
}

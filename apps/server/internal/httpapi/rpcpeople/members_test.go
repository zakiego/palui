package rpcpeople_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// getAgentMembersFn, saveMemberFn and setAgentMemberFn (PLAN 19.4, 19.7).
// Made-up numbers only.

const memberOwnerPhone = "6281200000009"

func membersEnv(t *testing.T) (*apitest.Env, string) {
	e, token := agentsEnv(t)
	_, err := owner.SetWhatsAppOwner(e.DB, memberOwnerPhone, agentsNow)
	testutil.Must(t, err)
	return e, token
}

func saveMember(t *testing.T, e *apitest.Env, token, input string) string {
	t.Helper()
	r := mustOK(t, e, token, "saveMemberFn", input)
	return at(r, "id").(string)
}

func membersOf(t *testing.T, e *apitest.Env, token, agentID string) any {
	t.Helper()
	return mustOK(t, e, token, "getAgentMembersFn", `{"id":"`+agentID+`"}`)
}

func TestSaveMemberAddsOneWithTypedNumberAndTelegramID(t *testing.T) {
	e, token := membersEnv(t)
	arisan := createAgent(t, e.DB, plainAgent("arisan", "Arisan"))
	id := saveMember(t, e, token, `{"agentId":"`+arisan.ID+`","name":" Bu Sari ","accounts":[{"channel":"whatsapp","address":"0812 0000 0001"},{"channel":"telegram","address":"123456789"}]}`)
	r := membersOf(t, e, token, arisan.ID)
	if at(r, "members", 0, "id") != id || at(r, "members", 0, "name") != "Bu Sari" {
		t.Fatal(js(r))
	}
	if js(at(r, "members", 0, "accounts", 0, "address")) != `"6281200000001@s.whatsapp.net"` || at(r, "members", 0, "accounts", 0, "label") != "+62 812-0000-0001" {
		t.Fatal(js(r))
	}
	if at(r, "members", 0, "accounts", 1, "channel") != "telegram" || at(r, "members", 0, "accounts", 1, "label") != "123456789" {
		t.Fatal(js(r))
	}
	if js(at(r, "members", 0, "agents")) != `[{"id":"`+arisan.ID+`","slug":"arisan","name":"Arisan"}]` || js(at(r, "others")) != `[]` {
		t.Fatal(js(r))
	}
}

func TestSaveMemberEditsAndRemovingTheLastAgentDeletesThem(t *testing.T) {
	e, token := membersEnv(t)
	arisan := createAgent(t, e.DB, plainAgent("arisan", "Arisan"))
	id := saveMember(t, e, token, `{"agentId":"`+arisan.ID+`","name":"Bu Sari","accounts":[{"channel":"whatsapp","address":"081200000001"}]}`)
	saveMember(t, e, token, `{"agentId":"`+arisan.ID+`","id":"`+id+`","name":"Sari","accounts":[{"channel":"telegram","address":"555"}]}`)
	r := membersOf(t, e, token, arisan.ID)
	if at(r, "members", 0, "name") != "Sari" || js(at(r, "members", 0, "accounts", 0, "address")) != `"555"` || at(r, "members", 0, "accounts", 1) != nil {
		t.Fatal(js(r))
	}
	mustOK(t, e, token, "setAgentMemberFn", `{"agentId":"`+arisan.ID+`","memberId":"`+id+`","on":false}`)
	if r := membersOf(t, e, token, arisan.ID); js(at(r, "members")) != `[]` || js(at(r, "others")) != `[]` {
		t.Fatal(js(r))
	}
	var n int
	testutil.Must(t, e.DB.QueryRow("SELECT count(*) FROM member_accounts").Scan(&n))
	if n != 0 {
		t.Fatalf("%d accounts left", n)
	}
}

func TestAMemberOfTwoAgentsShowsBothAndIsAnOtherForAThird(t *testing.T) {
	e, token := membersEnv(t)
	arisan := createAgent(t, e.DB, plainAgent("arisan", "Arisan"))
	cs := createAgent(t, e.DB, plainAgent("cs", "CS"))
	shop := createAgent(t, e.DB, plainAgent("shop", "Shop"))
	id := saveMember(t, e, token, `{"agentId":"`+arisan.ID+`","name":"Bu Sari","accounts":[{"channel":"telegram","address":"555"}]}`)
	mustOK(t, e, token, "setAgentMemberFn", `{"agentId":"`+cs.ID+`","memberId":"`+id+`","on":true}`)
	r := membersOf(t, e, token, cs.ID)
	if js(at(r, "members", 0, "agents")) != `[{"id":"`+arisan.ID+`","slug":"arisan","name":"Arisan"},{"id":"`+cs.ID+`","slug":"cs","name":"CS"}]` {
		t.Fatal(js(r))
	}
	r = membersOf(t, e, token, shop.ID)
	if js(at(r, "members")) != `[]` || at(r, "others", 0, "id") != id {
		t.Fatal(js(r))
	}
	// Removing from one of two keeps them.
	mustOK(t, e, token, "setAgentMemberFn", `{"agentId":"`+cs.ID+`","memberId":"`+id+`","on":false}`)
	if r := membersOf(t, e, token, arisan.ID); at(r, "members", 0, "id") != id {
		t.Fatal(js(r))
	}
}

func TestSaveMemberRefusalsAreReadable(t *testing.T) {
	e, token := membersEnv(t)
	arisan := createAgent(t, e.DB, plainAgent("arisan", "Arisan"))
	palui, err := agents.GetDefault(e.DB, agentsNow)
	testutil.Must(t, err)
	saveMember(t, e, token, `{"agentId":"`+arisan.ID+`","name":"Bu Sari","accounts":[{"channel":"telegram","address":"555"}]}`)
	for _, c := range []struct{ input, want string }{
		{`{"agentId":"` + arisan.ID + `","name":"Me","accounts":[{"channel":"whatsapp","address":"` + memberOwnerPhone + `"}]}`, "That's your own account."},
		{`{"agentId":"` + arisan.ID + `","name":"Again","accounts":[{"channel":"telegram","address":"555"}]}`, "That account already belongs to Bu Sari."},
		{`{"agentId":"` + palui.ID + `","name":"Pak Budi","accounts":[{"channel":"telegram","address":"777"}]}`, "Palui is yours alone and has no members."},
	} {
		status, msg := errorOf(t, e, token, "saveMemberFn", c.input)
		if status != 400 || msg != c.want {
			t.Fatalf("%s: %d %q", c.input, status, msg)
		}
	}
	// The refused save to the default agent left no member behind.
	var n int
	testutil.Must(t, e.DB.QueryRow("SELECT count(*) FROM members").Scan(&n))
	if n != 1 {
		t.Fatalf("%d members", n)
	}
}

func TestSaveMemberValidatesItsInput(t *testing.T) {
	e, token := membersEnv(t)
	arisan := createAgent(t, e.DB, plainAgent("arisan", "Arisan"))
	acc := `{"channel":"telegram","address":"1"}`
	six := strings.TrimSuffix(strings.Repeat(acc+",", 6), ",")
	for _, input := range []string{
		`{"agentId":"` + arisan.ID + `","name":"  ","accounts":[` + acc + `]}`,
		`{"agentId":"` + arisan.ID + `","name":"A","accounts":[]}`,
		`{"agentId":"` + arisan.ID + `","name":"A","accounts":[` + six + `]}`,
		`{"agentId":"` + arisan.ID + `","name":"A","accounts":[{"channel":"web","address":"web"}]}`,
	} {
		status, body := call(t, e, token, "saveMemberFn", input)
		if status != 400 || !strings.Contains(body, `"VALIDATION"`) {
			t.Fatalf("%s: %d %s", input, status, body)
		}
	}
}

func TestPeopleListsSendersAndDMsButNotTheOwnerHooksOrMembers(t *testing.T) {
	e, token := membersEnv(t)
	arisan := createAgent(t, e.DB, plainAgent("arisan", "Arisan"))
	group, err := chats.Ensure(e.DB, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120363000000000001@g.us", Kind: "group", Title: "Arisan"}, agentsNow)
	testutil.Must(t, err)
	msg := func(id, sender, name string, fromOwner int, at int64) {
		_, err := e.DB.Exec("INSERT INTO messages (id, chat_id, role, content, sender_id, sender_name, from_owner, created_at) VALUES (?, ?, 'user', '{}', ?, ?, ?, ?)", id, group.ID, sender, name, fromOwner, at)
		testutil.Must(t, err)
	}
	msg("m1", "6281200000002@s.whatsapp.net", "Guest Two", 0, agentsNow-dayMs)
	msg("m2", memberOwnerPhone+"@s.whatsapp.net", "Owner", 0, agentsNow)
	msg("m3", "hook:abc", "Hook", 0, agentsNow)
	msg("m4", "6281200000003@s.whatsapp.net", "Already", 0, agentsNow)
	msg("m5", "6281200000004@s.whatsapp.net", "Owner says", 1, agentsNow)
	// A DM chat with no stored message.
	_, err = chats.Ensure(e.DB, chats.EnsureInput{Channel: "telegram", ExternalID: "424242", Title: "Partner"}, agentsNow-2*dayMs)
	testutil.Must(t, err)
	saveMember(t, e, token, `{"agentId":"`+arisan.ID+`","name":"Already","accounts":[{"channel":"whatsapp","address":"6281200000003@s.whatsapp.net"}]}`)
	r := membersOf(t, e, token, arisan.ID)
	want := `[{"channel":"whatsapp","address":"6281200000002@s.whatsapp.net","label":"+62 812-0000-0002","name":"Guest Two","lastAt":` + js(float64(agentsNow-dayMs)) + `},` +
		`{"channel":"telegram","address":"424242","label":"424242","name":"Partner","lastAt":` + js(float64(agentsNow-2*dayMs)) + `}]`
	if js(at(r, "people")) != want {
		t.Fatal(js(at(r, "people")))
	}
}

func TestAgentMembersOfAMissingAgentFails(t *testing.T) {
	e, token := membersEnv(t)
	status, msg := errorOf(t, e, token, "getAgentMembersFn", `{"id":"agt_gone"}`)
	if status != 400 || msg != "That agent doesn't exist anymore." {
		t.Fatalf("%d %q", status, msg)
	}
}

package agents_test

// Port of agents.test.ts.

import (
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const png = "data:image/png;base64,iVBORw0KGgo="

func s(v string) *string { return &v }

func isAgentErr(err error) bool {
	var e *agents.Error
	return errors.As(err, &e)
}

func create(t *testing.T, d *db.DB, in agents.Input) db.Agent {
	t.Helper()
	a, err := agents.Create(d, in, 1)
	testutil.Must(t, err)
	return a
}

func update(t *testing.T, d *db.DB, id string, in agents.Input) db.Agent {
	t.Helper()
	a, err := agents.Update(d, id, in, 2)
	testutil.Must(t, err)
	return a
}

func def(t *testing.T, d *db.DB) db.Agent {
	a, err := agents.GetDefault(d, 1)
	testutil.Must(t, err)
	return a
}

func TestCreatesPaluiFromLegacyPersona(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "persona", "Kamu Palui versi lama.", 1))
	_, err := d.Exec("INSERT INTO instructions (id, title, body, created_at, updated_at) VALUES ('ins_a', 'a', 'b', 1, 1)")
	testutil.Must(t, err)
	palui := def(t, d)
	if palui.Slug != "palui" || !palui.IsDefault || palui.Persona != "Kamu Palui versi lama." || agents.Container(palui, "") != nil {
		t.Fatalf("%+v", palui)
	}
	var owner string
	testutil.Must(t, d.QueryRow("SELECT agent_id FROM instructions WHERE id = 'ins_a'").Scan(&owner))
	if owner != palui.ID || def(t, d).ID != palui.ID {
		t.Fatal(owner)
	}
}

func TestListsPaluiFirstAndValidates(t *testing.T) {
	d := testutil.DB(t)
	chef := create(t, d, agents.Input{Slug: "chef", Name: s(" Chef "), Persona: s("p")})
	list, err := agents.List(d, 1)
	testutil.Must(t, err)
	if len(list) != 2 || list[0].Slug != "palui" || list[1].Slug != "chef" || chef.Name != "Chef" || *agents.Container(chef, "") != "agent-chef" {
		t.Fatal(list)
	}
	for _, in := range []agents.Input{{Slug: "chef", Name: s("x"), Persona: s("p")}, {Slug: "9x", Name: s("x"), Persona: s("p")}} {
		if _, err := agents.Create(d, in, 1); !isAgentErr(err) {
			t.Fatal(err)
		}
	}
	neg := int64(-1)
	if _, err := agents.Update(d, chef.ID, agents.Input{DailyTokenCap: &neg}, 2); !isAgentErr(err) {
		t.Fatal(err)
	}
}

func TestPerChatContainers(t *testing.T) {
	d := testutil.DB(t)
	yes := true
	shop := create(t, d, agents.Input{Slug: "shop", Name: s("Shop"), Persona: s("p"), RecallPerChat: &yes})
	a := *agents.Container(shop, "cht_a")
	if !agents.RecallContainer.MatchString(a) || !strings.HasPrefix(a, "agent-shop-c") || len(a) != len("agent-shop-c")+10 {
		t.Fatal(a)
	}
	if *agents.Container(shop, "cht_b") == a || *agents.Container(shop, "cht_a") != a || *agents.Container(shop, "") != "agent-shop" {
		t.Fatal("containers")
	}
	long := agents.ChatContainer(strings.Repeat("x", 64), "cht_a")
	if len(long) > 64 || !agents.RecallContainer.MatchString(long) {
		t.Fatal(long)
	}
	palui := update(t, d, def(t, d).ID, agents.Input{RecallPerChat: &yes})
	if agents.Container(palui, "cht_a") != nil {
		t.Fatal("palui per chat")
	}
}

func seedRecall(t *testing.T, d *db.DB, config string) {
	_, err := d.Exec("INSERT INTO connectors (id, type, kind, label, enabled, status, config, created_at, updated_at) VALUES ('recall', 'recall', 'memory', 'recall', 1, 'on', ?, 1, 1)", config)
	testutil.Must(t, err)
}

func errMatches(t *testing.T, err error, sub string) {
	t.Helper()
	if !isAgentErr(err) || !strings.Contains(err.Error(), sub) {
		t.Fatalf("%v, want %q", err, sub)
	}
}

func TestReadsSharedButNeverOwnerMemory(t *testing.T) {
	d := testutil.DB(t)
	seedRecall(t, d, `{"container":"zaki"}`)
	shop := create(t, d, agents.Input{Slug: "shop", Name: s("Shop"), Persona: s("p"), RecallReadContainers: &[]string{" KB-Menu ", "kb-menu", "kb-faq"}})
	if strings.Join(agents.ReadContainers(shop), ",") != "kb-menu,kb-faq" {
		t.Fatal(agents.ReadContainers(shop))
	}
	sc := agents.Scope(shop, "cht_a")
	if *sc.Container != "agent-shop" || strings.Join(sc.Read, ",") != "kb-menu,kb-faq" || !sc.Locked {
		t.Fatalf("%+v", sc)
	}
	_, err := agents.Update(d, shop.ID, agents.Input{RecallReadContainers: &[]string{"zaki"}}, 2)
	errMatches(t, err, "owner's memory")
	_, err = agents.Update(d, shop.ID, agents.Input{RecallContainer: s("zaki")}, 2)
	errMatches(t, err, "owner's memory")
	_, err = agents.Update(d, shop.ID, agents.Input{RecallReadContainers: &[]string{"a", "b", "c", "d", "e", "f"}}, 2)
	errMatches(t, err, "At most 5")
	_, err = agents.Update(d, shop.ID, agents.Input{RecallReadContainers: &[]string{"no spaces!"}}, 2)
	if !isAgentErr(err) {
		t.Fatal(err)
	}
	if got := agents.ReadContainers(update(t, d, shop.ID, agents.Input{RecallReadContainers: &[]string{}})); len(got) != 0 {
		t.Fatal(got)
	}
	palui := update(t, d, def(t, d).ID, agents.Input{RecallReadContainers: &[]string{"kb-menu"}})
	ps := agents.Scope(palui, "")
	if ps.Container != nil || strings.Join(ps.Read, ",") != "kb-menu" || !ps.Locked {
		t.Fatalf("%+v", ps)
	}
}

// The report (2026-11-13): no container picked, so the owner's memory is
// recall's default (zaki on that server); both names stay reserved.
func TestReservesRecallDefaultContainer(t *testing.T) {
	d := testutil.DB(t)
	seedRecall(t, d, `{"url":"http://x"}`)
	shop := create(t, d, agents.Input{Slug: "shop", Name: s("Shop"), Persona: s("p")})
	_, err := agents.Update(d, shop.ID, agents.Input{RecallReadContainers: &[]string{"default"}}, 2)
	errMatches(t, err, "owner's memory")
	_, err = d.Exec(`UPDATE connectors SET config = '{"url":"http://x","defaultContainer":"zaki"}' WHERE id = 'recall'`)
	testutil.Must(t, err)
	_, err = agents.Update(d, shop.ID, agents.Input{RecallReadContainers: &[]string{"zaki"}}, 2)
	errMatches(t, err, "zaki is the owner's memory")
	_, err = agents.Update(d, shop.ID, agents.Input{RecallContainer: s("zaki")}, 2)
	errMatches(t, err, "zaki is the owner's memory")
	_, err = agents.Create(d, agents.Input{Slug: "spy", Name: s("Spy"), Persona: s("p"), RecallReadContainers: &[]string{"zaki"}}, 3)
	errMatches(t, err, "owner's memory")
	if got := agents.ReadContainers(update(t, d, shop.ID, agents.Input{RecallReadContainers: &[]string{"kb-menu"}})); strings.Join(got, ",") != "kb-menu" {
		t.Fatal(got)
	}
	_, err = d.Exec(`UPDATE connectors SET config = '{"url":"http://x","container":"owner","defaultContainer":"zaki"}' WHERE id = 'recall'`)
	testutil.Must(t, err)
	_, err = agents.Update(d, shop.ID, agents.Input{RecallReadContainers: &[]string{"owner"}}, 2)
	errMatches(t, err, "owner's memory")
}

func TestAvatar(t *testing.T) {
	d := testutil.DB(t)
	a := create(t, d, agents.Input{Slug: "cs", Name: s("CS"), Persona: s("p"), AvatarIcon: s("headset"), AvatarColor: s("teal")})
	if *a.AvatarIcon != "headset" || *a.AvatarColor != "teal" || a.AvatarImage != nil {
		t.Fatalf("%+v", a)
	}
	if got := update(t, d, a.ID, agents.Input{AvatarImage: s(png)}); *got.AvatarImage != png {
		t.Fatal("image")
	}
	if got := update(t, d, a.ID, agents.Input{AvatarImage: s("")}); got.AvatarImage != nil {
		t.Fatal("cleared")
	}
	for _, in := range []agents.Input{
		{AvatarImage: s("data:image/svg+xml;base64,PHN2Zz4=")},
		{AvatarImage: s("data:image/png;base64," + strings.Repeat("A", 100_000))},
		{AvatarIcon: s("skull")},
		{AvatarColor: s("#ff0000")},
	} {
		if _, err := agents.Update(d, a.ID, in, 2); !isAgentErr(err) {
			t.Fatal(err)
		}
	}
}

func TestPaluiHasNoSharedChatRules(t *testing.T) {
	d := testutil.DB(t)
	palui := def(t, d)
	r := update(t, d, palui.ID, agents.Input{GuestTools: s("none"), TelegramConnectorID: s("tgbot-x"), Name: s("Palui 2")})
	if r.GuestTools != "safe" || r.TelegramConnectorID != nil || r.Name != "Palui 2" {
		t.Fatalf("%+v", r)
	}
	if _, err := agents.Delete(d, palui.ID, 3); !isAgentErr(err) {
		t.Fatal(err)
	}
}

func TestAssignAndDelete(t *testing.T) {
	d := testutil.DB(t)
	chef := create(t, d, agents.Input{Slug: "chef", Name: s("Chef"), Persona: s("p")})
	_, err := notes.CreateInstruction(d, notes.InstructionInput{Title: "a", Body: "b", AgentID: chef.ID}, 1)
	testutil.Must(t, err)
	no := false
	group, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "1@g.us", Kind: "group", Allowed: &no}, 1)
	testutil.Must(t, err)
	c, _, err := agents.Assign(d, group.ID, &chef.ID, 1)
	testutil.Must(t, err)
	if *c.AgentID != chef.ID || !c.Allowed {
		t.Fatalf("%+v", c)
	}
	_, err = d.Exec("UPDATE chats SET allowed = 0 WHERE id = ?", group.ID)
	testutil.Must(t, err)
	// Back to Palui still turns the chat on.
	c, _, err = agents.Assign(d, group.ID, nil, 1)
	testutil.Must(t, err)
	if c.AgentID != nil || !c.Allowed {
		t.Fatalf("%+v", c)
	}
	_, _, err = agents.Assign(d, group.ID, &chef.ID, 1)
	testutil.Must(t, err)
	other := create(t, d, agents.Input{Slug: "cs", Name: s("CS"), Persona: s("p")})
	for _, r := range [][]any{{"mcp-a", "A", other.ID}, {"mcp-b", "B", chef.ID}, {"mcp-c", "C", nil}} {
		_, err := d.Exec("INSERT INTO connectors (id, type, kind, label, enabled, agent_id, agent_ids, created_at, updated_at) VALUES (?, 'mcp', 'tools', ?, 1, ?, CASE WHEN ? IS NULL THEN NULL ELSE json_array(?) END, 1, 1)", r[0], r[1], r[2], r[2], r[2])
		testutil.Must(t, err)
	}
	ok, err := agents.Delete(d, chef.ID, 5)
	if !ok || err != nil {
		t.Fatal(err)
	}
	got, _, _ := chats.Get(d, group.ID)
	if got.AgentID != nil {
		t.Fatal("chat kept agent")
	}
	rows, err := d.Query("SELECT id, agent_id, enabled FROM connectors ORDER BY id")
	testutil.Must(t, err)
	var lines []string
	for rows.Next() {
		var id string
		var agent *string
		var enabled bool
		testutil.Must(t, rows.Scan(&id, &agent, &enabled))
		a := "null"
		if agent != nil {
			a = *agent
		}
		lines = append(lines, id+":"+a+":"+map[bool]string{true: "1", false: "0"}[enabled])
	}
	rows.Close()
	if strings.Join(lines, ",") != "mcp-a:"+other.ID+":1,mcp-b:"+chef.ID+":0,mcp-c:null:1" {
		t.Fatal(lines)
	}
	list, err := notes.ListInstructions(d, notes.ListOptions{AgentID: chef.ID}, 6)
	testutil.Must(t, err)
	if len(list) != 0 {
		t.Fatal(list)
	}
}

// Deleting an agent removes its member links and deletes the members who were
// only there; a member also in another agent stays.
func TestDeleteRemovesItsMembers(t *testing.T) {
	d := testutil.DB(t)
	chef := create(t, d, agents.Input{Slug: "chef", Name: s("Chef"), Persona: s("p")})
	cs := create(t, d, agents.Input{Slug: "cs", Name: s("CS"), Persona: s("p")})
	only, err := members.Create(d, "Only", []members.AccountInput{{Channel: "whatsapp", Address: "0812 0000 0001"}}, 1)
	testutil.Must(t, err)
	both, err := members.Create(d, "Both", []members.AccountInput{{Channel: "telegram", Address: "4242"}}, 1)
	testutil.Must(t, err)
	testutil.Must(t, members.Link(d, chef.ID, only.ID, 1))
	testutil.Must(t, members.Link(d, chef.ID, both.ID, 1))
	testutil.Must(t, members.Link(d, cs.ID, both.ID, 1))
	ok, err := agents.Delete(d, chef.ID, 2)
	if !ok || err != nil {
		t.Fatal(err)
	}
	all, err := members.All(d)
	testutil.Must(t, err)
	if len(all) != 1 || all[0].ID != both.ID || len(all[0].Accounts) != 1 || len(all[0].AgentIDs) != 1 || all[0].AgentIDs[0] != cs.ID {
		t.Fatalf("%+v", all)
	}
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM member_accounts WHERE member_id = ?", only.ID).Scan(&n))
	if n != 0 {
		t.Fatal(n)
	}
}

func TestBotChatsKeepTheirAgent(t *testing.T) {
	d := testutil.DB(t)
	chef := create(t, d, agents.Input{Slug: "chef", Name: s("Chef"), Persona: s("p"), TelegramConnectorID: s("tgbot-chef")})
	cs := create(t, d, agents.Input{Slug: "cs", Name: s("CS"), Persona: s("p")})
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42", ConnectorID: "tgbot-chef", AgentID: &chef.ID}, 1)
	testutil.Must(t, err)
	if _, _, err := agents.Assign(d, chat.ID, &cs.ID, 1); !isAgentErr(err) {
		t.Fatal(err)
	}
	if _, err := agents.Create(d, agents.Input{Slug: "x2", Name: s("x"), Persona: s("p"), TelegramConnectorID: s("tgbot-chef")}, 1); !isAgentErr(err) {
		t.Fatal(err)
	}
}

func perms(pairs ...any) *jsonx.Map[*string] {
	m := jsonx.NewMap[*string]()
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == nil {
			m.Set(pairs[i].(string), nil)
		} else {
			v := pairs[i+1].(string)
			m.Set(pairs[i].(string), &v)
		}
	}
	return m
}

func TestToolPermissions(t *testing.T) {
	d := testutil.DB(t)
	palui := def(t, d)
	if palui.ToolPermissions.Valid {
		t.Fatal("not null")
	}
	yes, no := true, false
	set := func(p agents.PermissionsPatch) string {
		a, err := agents.SetToolPermissions(d, palui.ID, p, 2)
		testutil.Must(t, err)
		if !a.ToolPermissions.Valid {
			return "null"
		}
		return jsonx.Stringify(a.ToolPermissions.V)
	}
	if got := set(agents.PermissionsPatch{AllowAll: &yes}); got != `{"allowAll":true,"tools":{}}` {
		t.Fatal(got)
	}
	if got := set(agents.PermissionsPatch{Tools: perms("mcp__whoami", "ask", "mcp__drop", "deny")}); got != `{"allowAll":true,"tools":{"mcp__whoami":"ask","mcp__drop":"deny"}}` {
		t.Fatal(got)
	}
	if got := set(agents.PermissionsPatch{Tools: perms("mcp__whoami", nil)}); got != `{"allowAll":true,"tools":{"mcp__drop":"deny"}}` {
		t.Fatal(got)
	}
	if got := set(agents.PermissionsPatch{AllowAll: &no, Tools: perms("mcp__drop", nil)}); got != "null" {
		t.Fatal(got)
	}
	for _, p := range []agents.PermissionsPatch{{Tools: perms("no spaces", "allow")}, {Tools: perms("mcp__x", "sure")}} {
		if _, err := agents.SetToolPermissions(d, palui.ID, p, 2); !isAgentErr(err) {
			t.Fatal(err)
		}
	}
	if _, err := agents.SetToolPermissions(d, "agt_nope", agents.PermissionsPatch{AllowAll: &yes}, 2); !isAgentErr(err) {
		t.Fatal(err)
	}
}

func TestSharedChatsAndServing(t *testing.T) {
	d := testutil.DB(t)
	palui := def(t, d)
	chef := create(t, d, agents.Input{Slug: "chef", Name: s("Chef"), Persona: s("p")})
	gone := "agt_gone"
	if ok, _ := agents.ServesChat(d, palui.ID, &gone, 1); !ok {
		t.Fatal("deleted agent falls back to palui")
	}
	if ok, _ := agents.ServesChat(d, chef.ID, &chef.ID, 1); !ok {
		t.Fatal("chef")
	}
	clause, args, err := agents.ChatsServedBy(d, chef.ID)
	if err != nil || clause != `"chats"."agent_id" = ?` || args[0] != chef.ID {
		t.Fatal(clause)
	}
	web := db.Chat{Channel: "web", Kind: "dm", ExternalID: "web"}
	group := db.Chat{Channel: "whatsapp", Kind: "group", ExternalID: "1@g.us"}
	dm := db.Chat{Channel: "telegram", Kind: "dm", ExternalID: "77"}
	for c, want := range map[*db.Chat]bool{&web: false, &group: true, &dm: true} {
		if got, _ := agents.IsSharedChat(d, *c); got != want {
			t.Fatal(c.Channel, got)
		}
	}
}

func TestModelRoles(t *testing.T) {
	d := testutil.DB(t)
	a := create(t, d, agents.Input{Slug: "chef", Name: s("Chef"), Persona: s("Masak.")})
	if a.ModelRoles.Valid {
		t.Fatalf("a new agent uses the global models: %+v", a.ModelRoles)
	}
	a = update(t, d, a.ID, agents.Input{ModelRoles: &db.AgentModels{Fast: []db.ModelLink{{ConnectorID: "gw", Model: "  fast-1 ", Effort: "low"}}}})
	if !a.ModelRoles.Valid || len(a.ModelRoles.V.Default) != 0 || a.ModelRoles.V.Fast[0].Model != "fast-1" {
		t.Fatalf("%+v", a.ModelRoles)
	}
	// A patch without models leaves them.
	a = update(t, d, a.ID, agents.Input{Name: s("Chef 2")})
	if !a.ModelRoles.Valid {
		t.Fatal("a patch without models cleared them")
	}
	if _, err := agents.Update(d, a.ID, agents.Input{ModelRoles: &db.AgentModels{Default: []db.ModelLink{{ConnectorID: "gw", Model: "m", Effort: "turbo"}}}}, 3); !isAgentErr(err) {
		t.Fatal(err)
	}
	a = update(t, d, a.ID, agents.Input{ModelRoles: &db.AgentModels{}})
	if a.ModelRoles.Valid {
		t.Fatal("empty models should go back to the global ones")
	}
}

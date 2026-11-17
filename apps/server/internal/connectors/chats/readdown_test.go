package chats_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	chatsconn "github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	paluitools "github.com/zakiego/palui/apps/server/internal/tools"
)

const appURL = "https://palui.example.test"

type downEnv struct {
	d                                 *db.DB
	palui, club, shop                 db.Agent
	ownerDM, web, paluiGroup, guestDM db.Chat
	clubGroup, clubDM, shopGroup      db.Chat
	turn                              connectors.TurnInfo
}

// addFrom stores a message with a sender, like inbound does.
func addFrom(t *testing.T, d *db.DB, c db.Chat, role, text string, at int64, senderName string, fromOwner bool, extra *jsonx.Object) string {
	t.Helper()
	content := jsonx.NewMap[any]()
	content.Set("text", text)
	if extra != nil {
		for _, k := range extra.Keys() {
			v, _ := extra.Get(k)
			content.Set(k, v)
		}
	}
	b, _ := jsonx.Marshal(content)
	var name any
	if senderName != "" {
		name = senderName
	}
	id := secret.NewID("msg")
	_, err := d.Exec("INSERT INTO messages (id, chat_id, session_id, role, content, text, sender_name, from_owner, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'done', ?)", id, c.ID, c.SessionID, role, string(b), text, name, fromOwner, at)
	testutil.Must(t, err)
	_, err = d.Exec("UPDATE chats SET last_message_at = ? WHERE id = ?", at, c.ID)
	testutil.Must(t, err)
	return id
}

func downSetup(t *testing.T) downEnv {
	t.Helper()
	d := testutil.DB(t)
	_, err := owner.SetWhatsAppOwner(d, "6281200000001", NOW)
	testutil.Must(t, err)
	palui, err := agents.GetDefault(d, NOW)
	testutil.Must(t, err)
	club, err := agents.Create(d, agents.Input{Slug: "club", Name: testutil.Ptr("Book Club"), Persona: testutil.Ptr("p")}, NOW)
	testutil.Must(t, err)
	shop, err := agents.Create(d, agents.Input{Slug: "shop", Name: testutil.Ptr("Shop"), Persona: testutil.Ptr("p")}, NOW)
	testutil.Must(t, err)
	e := downEnv{
		d: d, palui: palui, club: club, shop: shop,
		ownerDM:    ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281200000001@s.whatsapp.net", Title: "Owner"}, NOW-100*MIN),
		web:        ensure(t, d, chats.EnsureInput{Channel: "web", ExternalID: "web", Title: "Web"}, NOW-100*MIN),
		paluiGroup: ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000001@g.us", Title: "Palui group", Kind: "group"}, NOW-100*MIN),
		guestDM:    ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281200000002@s.whatsapp.net", Title: "Guest"}, NOW-100*MIN),
		clubGroup:  ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000002@g.us", Title: "Book club", Kind: "group", AgentID: &club.ID}, NOW-90*MIN),
		clubDM:     ensure(t, d, chats.EnsureInput{Channel: "telegram", ExternalID: "700000001", Title: "Club member", AgentID: &club.ID}, NOW-90*MIN),
		shopGroup:  ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000003@g.us", Title: "Shop orders", Kind: "group", AgentID: &shop.ID}, NOW-90*MIN),
	}
	addFrom(t, d, e.paluiGroup, "user", "palui private note", NOW-80*MIN, "", true, nil)
	addFrom(t, d, e.clubDM, "user", "which book next?", NOW-70*MIN, "Member A", false, nil)
	addFrom(t, d, e.shopGroup, "user", "order 3 boxes", NOW-60*MIN, "Buyer B", false, nil)
	addFrom(t, d, e.clubGroup, "user", "meet on friday", NOW-30*MIN, "Member A", false, nil)
	e.turn = connectors.TurnInfo{RunID: "run_1", ChatID: e.ownerDM.ID, Channel: "whatsapp", Trigger: "message", Origin: "message", Mode: "owner", TZ: WITA, Now: NOW, AgentID: palui.ID}
	return e
}

func runtimeTools(t *testing.T, d *db.DB, turn connectors.TurnInfo) *connectors.ToolSet {
	t.Helper()
	set, err := chatsconn.RuntimeTools(d, turn, func() int64 { return NOW }, appURL)
	testutil.Must(t, err)
	return set
}

func downTools(t *testing.T, e downEnv) *connectors.ToolSet {
	return runtimeTools(t, e.d, e.turn)
}

func TestReadDownToolsAreOfferedToPaluiInTheOwnersDMAndWebChatOnly(t *testing.T) {
	e := downSetup(t)
	cases := []struct {
		name string
		mut  func(*connectors.TurnInfo)
		want string
	}{
		{"owner DM", func(*connectors.TurnInfo) {}, "search_chats,read_chat,list_agent_chats,read_agent_chat"},
		{"web chat", func(x *connectors.TurnInfo) { x.ChatID, x.Channel = e.web.ID, "web" }, "search_chats,read_chat,list_agent_chats,read_agent_chat"},
		{"owner's job", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "schedule", "schedule" }, "search_chats,read_chat,list_agent_chats,read_agent_chat"},
		{"non-default agent", func(x *connectors.TurnInfo) { x.ChatID, x.Channel, x.AgentID = e.clubDM.ID, "telegram", e.club.ID }, "search_chats,read_chat"},
		{"another agent's group", func(x *connectors.TurnInfo) { x.ChatID, x.AgentID, x.Mode = e.clubGroup.ID, e.club.ID, "outside" }, ""},
		{"a group Palui answers in", func(x *connectors.TurnInfo) { x.ChatID = e.paluiGroup.ID }, ""},
		{"a guest's DM", func(x *connectors.TurnInfo) { x.ChatID, x.Mode = e.guestDM.ID, "outside" }, "search_chats,read_chat"},
		{"hook", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "hook", "hook" }, ""},
		{"flow", func(x *connectors.TurnInfo) { x.Trigger, x.Origin, x.FlowID = "flow", "flow", "flw_1" }, "search_chats,read_chat"},
		{"outside mode", func(x *connectors.TurnInfo) { x.Mode = "outside" }, "search_chats,read_chat"},
	}
	for _, c := range cases {
		turn := e.turn
		c.mut(&turn)
		if got := strings.Join(runtimeTools(t, e.d, turn).Keys(), ","); got != c.want {
			t.Errorf("%s: %s", c.name, got)
		}
	}
}

func TestReadDownToolsAreSharedUntrustedReadsAnOutsideTurnNeverKeeps(t *testing.T) {
	e := downSetup(t)
	set := downTools(t, e)
	for _, name := range []string{"list_agent_chats", "read_agent_chat"} {
		pt, _ := set.Get(name)
		if pt.Class != "read" || !pt.Untrusted || pt.Data != connectors.DataShared {
			t.Fatalf("%s: %+v", name, pt)
		}
		if paluitools.InAudience(paluitools.AudienceOutside, pt.Data, pt.Class) || !paluitools.InAudience(paluitools.AudienceOwner, pt.Data, pt.Class) {
			t.Fatal(name)
		}
	}
}

func TestListAgentChatsListsOtherAgentsAndTheirChatsNeverPalui(t *testing.T) {
	e := downSetup(t)
	res := call(t, downTools(t, e), "list_agent_chats", `{}`)
	if strings.Join(res.Strs("agents", "slug"), ",") != "club,shop" {
		t.Fatal(res)
	}
	if got := strings.Join(res.Strs("agents.0.chats", "id"), ","); got != e.clubGroup.ID+","+e.clubDM.ID {
		t.Fatal(got)
	}
	if res.S("agents.0.name") != "Book Club" || res.S("agents.0.chats.0.kind") != "group" || res.N("agents.0.chats.0.messageCount") != 1 || res.S("agents.0.chats.0.lastMessageAt") == "" {
		t.Fatal(res.JSON("agents.0"))
	}
	s := res.String()
	for _, leak := range []string{e.paluiGroup.ID, e.ownerDM.ID, e.web.ID, "@g.us", "700000001", "externalId"} {
		if strings.Contains(s, leak) {
			t.Fatal("leaked", leak, s)
		}
	}
	one := call(t, downTools(t, e), "list_agent_chats", `{"agent":"shop"}`)
	if strings.Join(one.Strs("agents", "slug"), ",") != "shop" || one.Len("agents.0.chats") != 1 {
		t.Fatal(one)
	}
	if got := call(t, downTools(t, e), "list_agent_chats", `{"query":"member"}`); got.Len("agents.0.chats") != 1 || got.Len("agents.1.chats") != 0 {
		t.Fatal(got)
	}
	if got := call(t, downTools(t, e), "list_agent_chats", `{"limit":1}`); got.Len("agents.0.chats") != 1 {
		t.Fatal(got)
	}
	for _, ref := range []string{"palui", e.palui.ID, "nobody"} {
		if r := call(t, downTools(t, e), "list_agent_chats", `{"agent":"`+ref+`"}`); r.B("ok") || !strings.Contains(r.S("error"), "No other agent") {
			t.Fatal(ref, r)
		}
	}
}

func TestListAgentChatsSaysSoWhenThereIsNoOtherAgent(t *testing.T) {
	d := testutil.DB(t)
	palui, err := agents.GetDefault(d, NOW)
	testutil.Must(t, err)
	web := ensure(t, d, chats.EnsureInput{Channel: "web", ExternalID: "web", Title: "Web"}, NOW)
	set := runtimeTools(t, d, connectors.TurnInfo{ChatID: web.ID, Channel: "web", Trigger: "message", TZ: WITA, Now: NOW, AgentID: palui.ID})
	if r := call(t, set, "list_agent_chats", `{}`); r.N("count") != 0 || r.S("message") != "Belum ada agent lain selain Palui." {
		t.Fatal(r)
	}
}

func TestReadAgentChatLabelsWhoWroteEachMessage(t *testing.T) {
	e := downSetup(t)
	addFrom(t, e.d, e.clubGroup, "user", "I'll bring snacks", NOW-29*MIN, "", true, nil)
	addFrom(t, e.d, e.clubGroup, "assistant", "Noted for friday.", NOW-28*MIN, "", false, nil)
	addFrom(t, e.d, e.clubGroup, "user", "new release", NOW-27*MIN, "", false, kit(t, `{"hook":{"id":"hk_1","name":"Bookstore","mode":"forward"}}`))
	addFrom(t, e.d, e.clubGroup, "user", "no name here", NOW-26*MIN, "", false, nil)
	res := call(t, downTools(t, e), "read_agent_chat", `{"chat":"`+e.clubGroup.ID+`"}`)
	if !res.B("ok") || res.S("agent.slug") != "club" || res.S("chat.id") != e.clubGroup.ID || res.N("count") != 5 {
		t.Fatal(res)
	}
	var who []string
	for i := range 5 {
		who = append(who, res.S(fmt.Sprintf("messages.%d.from", i))+":"+res.S(fmt.Sprintf("messages.%d.name", i)))
	}
	if got := strings.Join(who, ","); got != "other:Member A,owner:Owner,agent:Book Club,hook:Bookstore,other:Someone" {
		t.Fatal(got)
	}
	if res.S("messages.0.text") != "meet on friday" || res.S("messages.0.id") == "" || res.S("messages.0.time") == "" {
		t.Fatal(res.JSON("messages.0"))
	}
	if res.Has("nextBefore") {
		t.Fatal("a short page has no nextBefore")
	}
}

func TestReadAgentChatNamesEachReplyAfterTheAgentWhoseRunWroteIt(t *testing.T) {
	e := downSetup(t)
	palui := e.palui
	// The group was Palui's before the owner moved it to the club agent.
	_, err := e.d.Exec("INSERT INTO runs (id, chat_id, agent_id, trigger, status, started_at) VALUES ('run_old', ?, ?, 'message', 'ok', ?), ('run_new', ?, ?, 'message', 'ok', ?)", e.clubGroup.ID, palui.ID, NOW-30*MIN, e.clubGroup.ID, e.club.ID, NOW-20*MIN)
	testutil.Must(t, err)
	old := addFrom(t, e.d, e.clubGroup, "assistant", "Palui wrote this.", NOW-29*MIN, "", false, nil)
	recent := addFrom(t, e.d, e.clubGroup, "assistant", "Club wrote this.", NOW-19*MIN, "", false, nil)
	_, err = e.d.Exec("UPDATE messages SET run_id = CASE id WHEN ? THEN 'run_old' ELSE 'run_new' END WHERE id IN (?, ?)", old, old, recent)
	testutil.Must(t, err)
	res := call(t, downTools(t, e), "read_agent_chat", `{"chat":"`+e.clubGroup.ID+`","query":"wrote this"}`)
	if got := res.S("messages.0.name") + "," + res.S("messages.1.name"); got != palui.Name+",Book Club" {
		t.Fatal(res)
	}
}

func kit(t *testing.T, raw string) *jsonx.Object {
	t.Helper()
	o, err := jsonx.ParseObject([]byte(raw))
	testutil.Must(t, err)
	return o
}

func TestReadAgentChatHidesFileRefsAndDashboardLinks(t *testing.T) {
	e := downSetup(t)
	addFrom(t, e.d, e.clubGroup, "assistant", "Saved: palui:attachment/att_abc123 open "+appURL+"/app/attachments?id=att_abc123 or https://example.org/app/x", NOW-20*MIN, "", false,
		kit(t, `{"quoted":{"text":"see `+appURL+`/app/chats"}}`))
	res := call(t, downTools(t, e), "read_agent_chat", `{"chat":"`+e.clubGroup.ID+`"}`)
	text := res.S("messages.1.text")
	if text != "Saved: [file disembunyikan] open [link dashboard disembunyikan] or https://example.org/app/x" {
		t.Fatal(text)
	}
	if res.S("messages.1.replyTo") != "see [link dashboard disembunyikan]" {
		t.Fatal(res.S("messages.1.replyTo"))
	}
	if strings.Contains(res.String(), "att_abc123") || strings.Contains(res.String(), appURL) {
		t.Fatal(res)
	}
}

func TestReadAgentChatRefusesPaluisOwnChats(t *testing.T) {
	e := downSetup(t)
	set := downTools(t, e)
	for _, c := range []db.Chat{e.ownerDM, e.web, e.paluiGroup} {
		r := call(t, set, "read_agent_chat", `{"chat":"`+c.ID+`"}`)
		if r.B("ok") || r.S("error") != fmt.Sprintf(`Chat "%s" is Palui's own. Use read_chat for it.`, c.ID) || strings.Contains(r.String(), "private note") {
			t.Fatal(r)
		}
	}
	if r := call(t, set, "read_agent_chat", `{"chat":"chat_missing"}`); r.B("ok") || !strings.Contains(r.S("error"), "list_agent_chats") {
		t.Fatal(r)
	}
	// By external id it doesn't resolve: only chat ids from list_agent_chats.
	if r := call(t, set, "read_agent_chat", `{"chat":"`+e.clubGroup.ExternalID+`"}`); r.B("ok") {
		t.Fatal(r)
	}
	// A deleted agent's chats go back to Palui, so they're Palui's own now.
	_, err := agents.Delete(e.d, e.shop.ID, NOW)
	testutil.Must(t, err)
	if r := call(t, downTools(t, e), "read_agent_chat", `{"chat":"`+e.shopGroup.ID+`"}`); r.B("ok") {
		t.Fatal(r)
	}
}

func TestReadAgentChatPagesBackWithNextBeforeWithoutSkipping(t *testing.T) {
	e := downSetup(t)
	// Pairs share a millisecond, so a time cursor would skip or repeat some.
	for i := range 9 {
		addFrom(t, e.d, e.clubDM, "user", fmt.Sprintf("m%d", i), NOW+int64(i/2)*MIN, "Member A", false, nil)
	}
	set := downTools(t, e)
	var seen []string
	before := ""
	for range 10 {
		in := `{"chat":"` + e.clubDM.ID + `","limit":4`
		if before != "" {
			in += `,"before":"` + before + `"`
		}
		res := call(t, set, "read_agent_chat", in+"}")
		page := res.Strs("messages", "text")
		seen = append(page, seen...)
		before = res.S("nextBefore")
		if before == "" {
			break
		}
		if res.S("messages.0.id") != before {
			t.Fatal("nextBefore isn't the oldest message")
		}
	}
	if len(seen) != 10 || seen[0] != "which book next?" {
		t.Fatal(seen)
	}
	got := map[string]bool{}
	for _, s := range seen {
		got[s] = true
	}
	if len(got) != 10 {
		t.Fatal("repeated", seen)
	}
	if r := call(t, set, "read_agent_chat", `{"chat":"`+e.clubDM.ID+`","before":"msg_missing"}`); r.B("ok") {
		t.Fatal(r)
	}
	// A message id from another chat isn't a cursor here.
	other := addFrom(t, e.d, e.shopGroup, "user", "x", NOW, "Buyer B", false, nil)
	if r := call(t, set, "read_agent_chat", `{"chat":"`+e.clubDM.ID+`","before":"`+other+`"}`); r.B("ok") {
		t.Fatal(r)
	}
}

func TestReadAgentChatCapsAt100AndClipsLongMessages(t *testing.T) {
	e := downSetup(t)
	for i := range 130 {
		addFrom(t, e.d, e.clubDM, "user", fmt.Sprintf("p%d %s", i, strings.Repeat("x", 600)), NOW+int64(i)*MIN, "Member A", false, nil)
	}
	set := downTools(t, e)
	def := call(t, set, "read_agent_chat", `{"chat":"`+e.clubDM.ID+`"}`)
	big := call(t, set, "read_agent_chat", `{"chat":"`+e.clubDM.ID+`","limit":1000}`)
	if def.N("count") != 30 || big.N("count") != 100 || !def.Has("nextBefore") {
		t.Fatal(def.N("count"), big.N("count"))
	}
	if !strings.HasSuffix(def.S("messages.0.text"), "… [dipotong, 605 karakter]") {
		t.Fatal(def.S("messages.0.text"))
	}
	pt, _ := set.Get("read_agent_chat")
	for _, in := range []string{`{"chat":"x","limit":0}`, `{"chat":"x","limit":101}`, `{}`} {
		if pt.Tool.Validate([]byte(in)) == nil {
			t.Fatal("accepted", in)
		}
	}
	lt, _ := set.Get("list_agent_chats")
	if lt.Tool.Validate([]byte(`{"limit":21}`)) == nil {
		t.Fatal("accepted limit 21")
	}
}

func TestReadAgentChatFiltersBySinceAndText(t *testing.T) {
	e := downSetup(t)
	set := downTools(t, e)
	addFrom(t, e.d, e.clubGroup, "user", "Friday works", NOW-10*MIN, "Member C", false, nil)
	if got := strings.Join(call(t, set, "read_agent_chat", `{"chat":"`+e.clubGroup.ID+`","query":"FRIDAY"}`).Strs("messages", "text"), ","); got != "meet on friday,Friday works" {
		t.Fatal(got)
	}
	if got := strings.Join(call(t, set, "read_agent_chat", `{"chat":"`+e.clubGroup.ID+`","since":"2026-11-10T09:45"}`).Strs("messages", "text"), ","); got != "Friday works" {
		t.Fatal(got)
	}
	if r := call(t, set, "read_agent_chat", `{"chat":"`+e.clubGroup.ID+`","since":"soon"}`); r.B("ok") {
		t.Fatal(r)
	}
}

package chats_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	chatsconn "github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Tuesday 2026-11-10 10:00 WITA.
const (
	NOW  = int64(1794276000000)
	WITA = "Asia/Makassar"
	MIN  = int64(60_000)
)

func addMessage(t *testing.T, d *db.DB, c db.Chat, role, text string, at int64, extra *jsonx.Object) {
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
	_, err := d.Exec("INSERT INTO messages (id, chat_id, session_id, role, content, text, status, from_owner, created_at) VALUES (?, ?, ?, ?, ?, ?, 'done', 0, ?)", secret.NewID("msg"), c.ID, c.SessionID, role, string(b), text, at)
	testutil.Must(t, err)
	_, err = d.Exec("UPDATE chats SET last_message_at = ? WHERE id = ?", at, c.ID)
	testutil.Must(t, err)
}

type env struct {
	db                    *db.DB
	dm, family, work, web db.Chat
	turn                  connectors.TurnInfo
	tools                 *connectors.ToolSet
}

func ensure(t *testing.T, d *db.DB, in chats.EnsureInput, at int64) db.Chat {
	t.Helper()
	c, err := chats.Ensure(d, in, at)
	testutil.Must(t, err)
	return c
}

func setup(t *testing.T) env {
	d := testutil.DB(t)
	dm := ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281234@s.whatsapp.net", Title: "Zaki"}, NOW-100*MIN)
	family := ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "1203630@g.us", Title: "Keluarga Besar", Kind: "group"}, NOW-90*MIN)
	work := ensure(t, d, chats.EnsureInput{Channel: "telegram", ExternalID: "-100777", Title: "Tim Vorneo", Kind: "group"}, NOW-80*MIN)
	web := ensure(t, d, chats.EnsureInput{Channel: "web", ExternalID: "web", Title: "Palui (web)"}, NOW-70*MIN)
	addMessage(t, d, family, "user", "besok kumpul jam 7 ya", NOW-60*MIN, nil)
	addMessage(t, d, family, "assistant", "Oke, aku catat.", NOW-59*MIN, nil)
	addMessage(t, d, family, "tool", `{"ok":true}`, NOW-58*MIN, nil)
	addMessage(t, d, family, "system", "summary", NOW-57*MIN, nil)
	addMessage(t, d, work, "user", "deploy jam 3", NOW-30*MIN, nil)
	addMessage(t, d, dm, "user", "halo", NOW-10*MIN, nil)
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: dm.ID, Channel: "whatsapp", Trigger: "message", TZ: WITA, Now: NOW}
	e := env{db: d, dm: dm, family: family, work: work, web: web, turn: turn}
	e.tools = tools(t, d, turn)
	return e
}

func tools(t *testing.T, d *db.DB, turn connectors.TurnInfo) *connectors.ToolSet {
	t.Helper()
	set, err := chatsconn.Tools(d, turn, func() int64 { return NOW })
	testutil.Must(t, err)
	return set
}

func call(t *testing.T, set *connectors.ToolSet, name string, input string) conntest.Value {
	return conntest.Call(t, set, name, input)
}

func TestManifestIsABuiltinSkill(t *testing.T) {
	m := chatsconn.Manifest
	if m.ID != "chats" || m.Kind != "skill" || !m.Builtin || m.Icon != "messages-square" {
		t.Fatalf("%+v", m)
	}
}

func TestExposesTwoReadToolsBothUntrusted(t *testing.T) {
	e := setup(t)
	for _, k := range e.tools.Keys() {
		pt, _ := e.tools.Get(k)
		if pt.Class != "read" || !pt.Untrusted {
			t.Fatal(k)
		}
	}
	if strings.Join(e.tools.Keys(), ",") != "search_chats,read_chat" {
		t.Fatal(e.tools.Keys())
	}
}

func TestAreOfferedInADMOrTheWebChatNeverInAGroupOrAHookTurn(t *testing.T) {
	e := setup(t)
	cases := []struct {
		mut  func(*connectors.TurnInfo)
		want bool
	}{
		{func(*connectors.TurnInfo) {}, true},
		{func(x *connectors.TurnInfo) { x.ChatID, x.Channel = e.web.ID, "web" }, true},
		{func(x *connectors.TurnInfo) { x.Trigger = "schedule" }, true},
		{func(x *connectors.TurnInfo) { x.ChatID = e.family.ID }, false},
		{func(x *connectors.TurnInfo) { x.Trigger = "hook" }, false},
		{func(x *connectors.TurnInfo) { x.ChatID = "chat_missing" }, false},
	}
	for i, c := range cases {
		turn := e.turn
		c.mut(&turn)
		got, err := chatsconn.OffersChatTools(e.db, turn)
		testutil.Must(t, err)
		if got != c.want {
			t.Fatalf("case %d: %v", i, got)
		}
	}
}

func TestSearchListsTheNewestConversationFirstChatsWithNoMessagesLast(t *testing.T) {
	e := setup(t)
	res := call(t, e.tools, "search_chats", `{}`)
	if got := strings.Join(res.Strs("chats", "title"), ","); got != "Zaki,Tim Vorneo,Keluarga Besar,Palui (web)" {
		t.Fatal(got)
	}
	want := fmt.Sprintf(`{"id":"%s","channel":"whatsapp","externalId":"1203630@g.us","title":"Keluarga Besar","kind":"group","lastMessageAt":"2026-11-10T09:03:00.000+08:00","messageCount":2}`, e.family.ID)
	if got := res.JSON("chats.2"); got != want {
		t.Fatal(got)
	}
	if res.Get("chats.3.lastMessageAt") != nil || res.N("chats.3.messageCount") != 0 {
		t.Fatal(res.JSON("chats.3"))
	}
}

func TestSearchMatchesTitleExternalIDAndChatIDCaseInsensitive(t *testing.T) {
	e := setup(t)
	for _, q := range []string{"keluarga", "@g.us", e.family.ID} {
		ids := call(t, e.tools, "search_chats", `{"query":"`+q+`"}`).Strs("chats", "id")
		if len(ids) != 1 || ids[0] != e.family.ID {
			t.Fatal(q, ids)
		}
	}
}

func TestSearchTreatsPercentAndUnderscoreLiterally(t *testing.T) {
	e := setup(t)
	ensure(t, e.db, chats.EnsureInput{Channel: "telegram", ExternalID: "9", Title: "Diskon 50% hari ini"}, NOW)
	if got := call(t, e.tools, "search_chats", `{"query":"0% h"}`).Strs("chats", "title"); len(got) != 1 || got[0] != "Diskon 50% hari ini" {
		t.Fatal(got)
	}
	if call(t, e.tools, "search_chats", `{"query":"k%r"}`).N("count") != 0 || call(t, e.tools, "search_chats", `{"query":"t_m"}`).N("count") != 0 {
		t.Fatal("wildcards matched")
	}
}

func TestSearchFiltersByChannelAndKind(t *testing.T) {
	e := setup(t)
	if got := strings.Join(call(t, e.tools, "search_chats", `{"channel":"whatsapp"}`).Strs("chats", "title"), ","); got != "Zaki,Keluarga Besar" {
		t.Fatal(got)
	}
	if got := strings.Join(call(t, e.tools, "search_chats", `{"kind":"group"}`).Strs("chats", "title"), ","); got != "Tim Vorneo,Keluarga Besar" {
		t.Fatal(got)
	}
	if call(t, e.tools, "search_chats", `{"channel":"telegram","kind":"dm"}`).N("count") != 0 {
		t.Fatal("dm on telegram")
	}
}

func TestSearchSaysSoWhenNothingMatches(t *testing.T) {
	e := setup(t)
	if got := call(t, e.tools, "search_chats", `{"query":"arisan"}`).String(); got != `{"ok":true,"count":0,"chats":[],"message":"Nggak ada chat yang cocok sama \"arisan\"."}` {
		t.Fatal(got)
	}
}

func TestSearchCapsResultsAt20(t *testing.T) {
	e := setup(t)
	for i := 0; i < 30; i++ {
		ensure(t, e.db, chats.EnsureInput{Channel: "telegram", ExternalID: fmt.Sprint(i)}, NOW)
	}
	all, err := chats.SearchChats(e.db, chats.Search{Limit: 500})
	testutil.Must(t, err)
	three, err := chats.SearchChats(e.db, chats.Search{Limit: 3})
	testutil.Must(t, err)
	if len(all) != 20 || len(three) != 3 {
		t.Fatal(len(all), len(three))
	}
}

func TestReadReadsOwnerAndPaluiMessagesOldestFirstSkippingToolAndSystemRows(t *testing.T) {
	e := setup(t)
	res := call(t, e.tools, "read_chat", `{"chat":"`+e.family.ID+`"}`)
	if !res.B("ok") || res.S("chat.id") != e.family.ID || res.S("chat.title") != "Keluarga Besar" {
		t.Fatal(res)
	}
	if got := res.JSON("messages"); got != `[{"role":"user","time":"2026-11-10T09:00:00.000+08:00","text":"besok kumpul jam 7 ya"},{"role":"assistant","time":"2026-11-10T09:01:00.000+08:00","text":"Oke, aku catat."}]` {
		t.Fatal(got)
	}
}

func TestReadFindsAChatByItsExactExternalIDAndAsksForTheChannelWhenAmbiguous(t *testing.T) {
	e := setup(t)
	if call(t, e.tools, "read_chat", `{"chat":"-100777"}`).S("chat.id") != e.work.ID {
		t.Fatal("by external id")
	}
	if got := call(t, e.tools, "read_chat", `{"chat":"-1007"}`).S("error"); got != `No chat "-1007". Call search_chats to get the id.` {
		t.Fatal(got)
	}
	ensure(t, e.db, chats.EnsureInput{Channel: "whatsapp", ExternalID: "-100777"}, NOW)
	res := call(t, e.tools, "read_chat", `{"chat":"-100777"}`)
	if res.B("ok") || res.S("error") != `"-100777" matches chats on telegram, whatsapp. Pass channel, or use the chat id.` {
		t.Fatal(res)
	}
	if call(t, e.tools, "read_chat", `{"chat":"-100777","channel":"telegram"}`).S("chat.id") != e.work.ID {
		t.Fatal("with channel")
	}
}

func TestReadReturnsTheNewestNCappedAt100(t *testing.T) {
	e := setup(t)
	for i := 0; i < 150; i++ {
		addMessage(t, e.db, e.web, "user", fmt.Sprintf("pesan %d", i), NOW+int64(i)*MIN, nil)
	}
	last5, err := chats.ReadMessages(e.db, e.web.ID, chats.MessageQuery{Limit: 5})
	testutil.Must(t, err)
	var texts []string
	for _, m := range last5 {
		texts = append(texts, m.Text)
	}
	if strings.Join(texts, ",") != "pesan 145,pesan 146,pesan 147,pesan 148,pesan 149" {
		t.Fatal(texts)
	}
	def, _ := chats.ReadMessages(e.db, e.web.ID, chats.MessageQuery{})
	big, _ := chats.ReadMessages(e.db, e.web.ID, chats.MessageQuery{Limit: 1000})
	if len(def) != 30 || len(big) != 100 {
		t.Fatal(len(def), len(big))
	}
}

func TestReadFiltersByBeforeSinceAndText(t *testing.T) {
	e := setup(t)
	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		addMessage(t, e.db, e.web, role, fmt.Sprintf("Rapat %d", i), NOW+int64(i)*60*MIN, nil)
	}
	read := func(extra string) string {
		res := call(t, e.tools, "read_chat", `{"chat":"`+e.web.ID+`"`+extra+`}`)
		return strings.Join(res.Strs("messages", "text"), ",")
	}
	cases := map[string]string{
		`,"before":"2026-11-10T12:00"`:                            "Rapat 0,Rapat 1",
		`,"since":"2026-11-10T14:00"`:                             "Rapat 4,Rapat 5",
		`,"since":"2026-11-10T11:00","before":"2026-11-10T13:00"`: "Rapat 1,Rapat 2",
		`,"since":"2026-11-10T05:00:00Z"`:                         "Rapat 3,Rapat 4,Rapat 5",
		`,"query":"rapat 3"`:                                      "Rapat 3",
	}
	for in, want := range cases {
		if got := read(in); got != want {
			t.Fatalf("%s: %s", in, got)
		}
	}
	if got := call(t, e.tools, "read_chat", `{"chat":"`+e.web.ID+`","before":"kemarin"}`).S("error"); got != `"kemarin" isn't a time. Use YYYY-MM-DDTHH:mm.` {
		t.Fatal(got)
	}
}

func TestReadClipsLongMessagesAndShowsMediaQuotesAndHooksAsShortMarkers(t *testing.T) {
	e := setup(t)
	parse := func(s string) *jsonx.Object { o, err := jsonx.ParseObject([]byte(s)); testutil.Must(t, err); return o }
	addMessage(t, e.db, e.web, "user", strings.Repeat("x", 2000), NOW, nil)
	addMessage(t, e.db, e.web, "user", "ini KTP", NOW+1, parse(`{"media":[{"kind":"image","mimeType":"image/jpeg","data":"`+strings.Repeat("A", 5000)+`"},{"kind":"document","mimeType":"application/pdf","filename":"tiket.pdf"}]}`))
	addMessage(t, e.db, e.web, "user", "", NOW+2, parse(`{"media":[{"kind":"audio","mimeType":"audio/ogg"}]}`))
	addMessage(t, e.db, e.web, "user", "setuju", NOW+3, parse(`{"quoted":{"from":"other","text":"`+strings.Repeat("y", 500)+`"}}`))
	addMessage(t, e.db, e.web, "user", "build failed", NOW+4, parse(`{"hook":{"id":"hook_1","name":"GitHub","mode":"summarize"}}`))
	res := call(t, e.tools, "read_chat", `{"chat":"`+e.web.ID+`"}`)
	if got := res.S("messages.0.text"); got != strings.Repeat("x", chatsconn.MessageMaxChars)+"… [dipotong, 2000 karakter]" {
		t.Fatal(got)
	}
	if got := res.S("messages.1.text"); got != "ini KTP [gambar] [dokumen: tiket.pdf]" {
		t.Fatal(got)
	}
	if strings.Contains(res.JSON("messages.1"), "AAAA") {
		t.Fatal("media data leaked")
	}
	if res.S("messages.2.text") != "[audio]" {
		t.Fatal(res.S("messages.2.text"))
	}
	if got := res.S("messages.3.replyTo"); got != strings.Repeat("y", 200)+"… [dipotong, 500 karakter]" {
		t.Fatal(got)
	}
	if res.S("messages.4.role") != "user" || res.S("messages.4.text") != "build failed" || res.S("messages.4.hook") != "GitHub" {
		t.Fatal(res.JSON("messages.4"))
	}
}

type agentEnv struct {
	env
	fin, palui           db.Agent
	finGroup, finDm      db.Chat
	paluiTools, finTools *connectors.ToolSet
}

func withAgents(t *testing.T) agentEnv {
	e := setup(t)
	palui, err := agents.GetDefault(e.db, NOW)
	testutil.Must(t, err)
	fin, err := agents.Create(e.db, agents.Input{Slug: "finansial", Name: testutil.Ptr("Finansial"), Persona: testutil.Ptr("Catat keuangan.")}, NOW)
	testutil.Must(t, err)
	finGroup := ensure(t, e.db, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120363413215483666@g.us", Title: "Finansial", Kind: "group", AgentID: &fin.ID}, NOW-50*MIN)
	finDm := ensure(t, e.db, chats.EnsureInput{Channel: "whatsapp", ExternalID: "6289999@s.whatsapp.net", Title: "Owner ke Finansial", AgentID: &fin.ID}, NOW-40*MIN)
	addMessage(t, e.db, finGroup, "user", "laundry 43rb dari Jago", NOW-20*MIN, nil)
	addMessage(t, e.db, finGroup, "assistant", "Tercatat.", NOW-19*MIN, nil)
	addMessage(t, e.db, finDm, "user", "rekap bulan ini", NOW-5*MIN, nil)
	pt := e.turn
	pt.AgentID = palui.ID
	ft := e.turn
	ft.ChatID, ft.AgentID = finDm.ID, fin.ID
	return agentEnv{env: e, fin: fin, palui: palui, finGroup: finGroup, finDm: finDm, paluiTools: tools(t, e.db, pt), finTools: tools(t, e.db, ft)}
}

func sortedIDs(v conntest.Value) string {
	ids := v.Strs("chats", "id")
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func sorted(ids ...string) string {
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func TestPerAgentSearchListsOnlyTheCallersChats(t *testing.T) {
	a := withAgents(t)
	if got := sortedIDs(call(t, a.paluiTools, "search_chats", `{}`)); got != sorted(a.dm.ID, a.family.ID, a.work.ID, a.web.ID) {
		t.Fatal(got)
	}
	if got := sortedIDs(call(t, a.finTools, "search_chats", `{}`)); got != sorted(a.finGroup.ID, a.finDm.ID) {
		t.Fatal(got)
	}
	for _, q := range []string{"Finansial", "120363413215483666", a.finGroup.ID} {
		if call(t, a.paluiTools, "search_chats", `{"query":"`+q+`"}`).N("count") != 0 {
			t.Fatal(q)
		}
	}
	if call(t, a.finTools, "search_chats", `{"query":"Keluarga"}`).N("count") != 0 {
		t.Fatal("fin sees Keluarga")
	}
}

func TestPerAgentReadRefusesAnotherAgentsChatAndLeaksNothing(t *testing.T) {
	a := withAgents(t)
	for _, chat := range []string{a.finGroup.ID, a.finGroup.ExternalID} {
		res := call(t, a.paluiTools, "read_chat", `{"chat":"`+chat+`"}`)
		want := fmt.Sprintf(`{"ok":false,"error":"Chat \"%s\" belongs to another agent. This agent can only read the chats it answers in."}`, chat)
		if res.String() != want || strings.Contains(res.String(), "laundry") {
			t.Fatal(res)
		}
	}
	if call(t, a.finTools, "read_chat", `{"chat":"`+a.dm.ID+`"}`).B("ok") {
		t.Fatal("fin read Palui's DM")
	}
}

func TestPerAgentReadStillReadsTheCallersOwnChats(t *testing.T) {
	a := withAgents(t)
	if got := strings.Join(call(t, a.finTools, "read_chat", `{"chat":"`+a.finGroup.ExternalID+`"}`).Strs("messages", "text"), ","); got != "laundry 43rb dari Jago,Tercatat." {
		t.Fatal(got)
	}
	if got := strings.Join(call(t, a.paluiTools, "read_chat", `{"chat":"`+a.family.ID+`"}`).Strs("messages", "text"), ","); got != "besok kumpul jam 7 ya,Oke, aku catat." {
		t.Fatal(got)
	}
}

func TestPerAgentFollowsTheChatsAgent(t *testing.T) {
	a := withAgents(t)
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: a.family.ID, Channel: "whatsapp", Trigger: "message", TZ: WITA, Now: NOW, AgentID: a.palui.ID}
	_, _, err := agents.Assign(a.db, a.family.ID, &a.fin.ID, NOW)
	testutil.Must(t, err)
	if call(t, tools(t, a.db, turn), "read_chat", `{"chat":"`+a.family.ID+`"}`).B("ok") {
		t.Fatal("read after reassign")
	}
	_, err = agents.Delete(a.db, a.fin.ID, NOW)
	testutil.Must(t, err)
	if !call(t, tools(t, a.db, turn), "read_chat", `{"chat":"`+a.finGroup.ID+`"}`).B("ok") || !call(t, tools(t, a.db, turn), "read_chat", `{"chat":"`+a.family.ID+`"}`).B("ok") {
		t.Fatal("not handed back")
	}
}

func TestPerAgentWithoutAgentIDUsesTheAgentOfTheTurnsChat(t *testing.T) {
	a := withAgents(t)
	set := tools(t, a.db, connectors.TurnInfo{RunID: "run_1", ChatID: a.finDm.ID, Channel: "whatsapp", Trigger: "schedule", TZ: WITA, Now: NOW})
	if !call(t, set, "read_chat", `{"chat":"`+a.finGroup.ID+`"}`).B("ok") || call(t, set, "read_chat", `{"chat":"`+a.dm.ID+`"}`).B("ok") {
		t.Fatal("wrong agent")
	}
}

func TestPerAgentAnUnknownChatIDOnTheTurnSeesNothing(t *testing.T) {
	a := withAgents(t)
	set := tools(t, a.db, connectors.TurnInfo{RunID: "source_1", ChatID: "", Trigger: "schedule", TZ: WITA, Now: NOW})
	if call(t, set, "search_chats", `{}`).N("count") != 0 {
		t.Fatal("saw chats")
	}
}

func TestParseTimeReadsWallClockTimeInTheZoneOrAnInstantWithAnOffset(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"2026-11-10T10:00", NOW, true},
		{"2026-11-10", NOW - 10*3600_000, true},
		{"2026-11-10T02:00:00Z", NOW, true},
		{"nanti", 0, false},
	}
	for _, c := range cases {
		got, ok := chatsconn.ParseTime(c.in, WITA)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatal(c.in, got, ok)
		}
	}
}

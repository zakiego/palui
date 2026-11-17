package recall_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/recall"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const downNow = int64(1794276000000)

type memEnv struct {
	f                            *fake
	d                            *db.DB
	running                      *connectors.RunningConnector
	palui, club, shop            db.Agent
	ownerDM, web, paluiGroup     db.Chat
	clubGroup, clubDM, shopGroup db.Chat
	turn                         connectors.TurnInfo
}

func memSetup(t *testing.T, o fakeOpts) memEnv {
	t.Helper()
	f := fakeRecall(t, o)
	d := testutil.DB(t)
	// The owner's memory: the memory connector's container.
	ownerCfg, err := jsonx.ParseObject([]byte(`{"container":"owner-mem"}`))
	testutil.Must(t, err)
	testutil.SeedConnector(t, d, "recall", "memory", testutil.SeedOptions{Config: ownerCfg}, downNow)
	_, err = owner.SetWhatsAppOwner(d, "6281200000001", downNow)
	testutil.Must(t, err)
	palui, err := agents.GetDefault(d, downNow)
	testutil.Must(t, err)
	yes, no := true, false
	club, err := agents.Create(d, agents.Input{Slug: "club", Name: testutil.Ptr("Book Club"), Persona: testutil.Ptr("p"), RecallPerChat: &yes}, downNow)
	testutil.Must(t, err)
	shop, err := agents.Create(d, agents.Input{Slug: "shop", Name: testutil.Ptr("Shop"), Persona: testutil.Ptr("p"), RecallPerChat: &no, RecallReadContainers: &[]string{"kb-shop"}}, downNow)
	testutil.Must(t, err)
	ensure := func(in chats.EnsureInput, at int64) db.Chat {
		c, err := chats.Ensure(d, in, at)
		testutil.Must(t, err)
		_, err = d.Exec("UPDATE chats SET last_message_at = ? WHERE id = ?", at, c.ID)
		testutil.Must(t, err)
		return c
	}
	e := memEnv{
		f: f, d: d, palui: palui, club: club, shop: shop,
		ownerDM:    ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281200000001@s.whatsapp.net", Title: "Owner"}, downNow-9),
		web:        ensure(chats.EnsureInput{Channel: "web", ExternalID: "web", Title: "Web"}, downNow-9),
		paluiGroup: ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000001@g.us", Title: "Palui group", Kind: "group"}, downNow-9),
		clubGroup:  ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000002@g.us", Title: "Book club", Kind: "group", AgentID: &club.ID}, downNow-2),
		clubDM:     ensure(chats.EnsureInput{Channel: "telegram", ExternalID: "700000001", Title: "Club member", AgentID: &club.ID}, downNow-3),
		shopGroup:  ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000003@g.us", Title: "Shop orders", Kind: "group", AgentID: &shop.ID}, downNow-4),
	}
	ctx := conntest.NewCtx(t, d, "recall", cfg(t, f.base, ""), map[string]string{"apiKey": KEY})
	running, err := recall.Runtime.Start(ctx.Context)
	testutil.Must(t, err)
	t.Cleanup(func() { running.Stop(context.Background()) })
	e.running = running
	e.turn = connectors.TurnInfo{RunID: "run_1", ChatID: e.ownerDM.ID, Channel: "whatsapp", Trigger: "message", Origin: "message", Mode: "owner", TZ: "Asia/Makassar", Now: downNow, AgentID: palui.ID, Memory: scope("owner-mem", nil)}
	return e
}

func (e memEnv) tools(t *testing.T) *connectors.ToolSet { return toolsFor(t, e.running, e.turn) }

// sortedArgs is what recall got, order-free (containers are read at once).
func sortedArgs(f *fake) string {
	var out []string
	for _, a := range f.Args() {
		out = append(out, argsJSON([]map[string]any{a}))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestAgentMemoryToolsAreOfferedOnlyToPaluiInTheOwnersChats(t *testing.T) {
	e := memSetup(t, fakeOpts{})
	cases := []struct {
		name string
		mut  func(*connectors.TurnInfo)
		want bool
	}{
		{"owner DM", func(*connectors.TurnInfo) {}, true},
		{"web chat", func(x *connectors.TurnInfo) { x.ChatID, x.Channel = e.web.ID, "web" }, true},
		{"non-default agent", func(x *connectors.TurnInfo) {
			x.ChatID, x.Channel, x.AgentID, x.Memory = e.clubDM.ID, "telegram", e.club.ID, scope("agent-club", nil)
		}, false},
		{"another agent's group", func(x *connectors.TurnInfo) { x.ChatID, x.AgentID, x.Mode = e.clubGroup.ID, e.club.ID, "outside" }, false},
		{"a group Palui answers in", func(x *connectors.TurnInfo) { x.ChatID = e.paluiGroup.ID }, false},
		{"hook", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "hook", "hook" }, false},
		{"flow", func(x *connectors.TurnInfo) { x.Trigger, x.Origin, x.FlowID = "flow", "flow", "flw_1" }, false},
		{"outside mode", func(x *connectors.TurnInfo) { x.Mode = "outside" }, false},
	}
	for _, c := range cases {
		turn := e.turn
		c.mut(&turn)
		set := toolsFor(t, e.running, turn)
		_, s := set.Get("search_agent_memory")
		_, l := set.Get("list_agent_memory")
		if s != c.want || l != c.want {
			t.Errorf("%s: search %v list %v", c.name, s, l)
		}
		if _, ok := set.Get("search_memories"); !ok {
			t.Errorf("%s: lost its own tools", c.name)
		}
	}
	for _, name := range []string{"search_agent_memory", "list_agent_memory"} {
		pt, _ := e.tools(t).Get(name)
		if pt.Class != "read" || !pt.Untrusted || pt.Data != connectors.DataShared {
			t.Fatalf("%s: %+v", name, pt)
		}
	}
}

func TestSearchAgentMemoryReadsTheAgentsOwnAndPerChatContainers(t *testing.T) {
	var groupMem, dmMem string
	e := memSetup(t, fakeOpts{containers: func() []string { return []string{"owner-mem", "agent-club", dmMem, groupMem, "agent-shop"} }})
	groupMem = agents.ChatContainer("agent-club", e.clubGroup.ID)
	dmMem = agents.ChatContainer("agent-club", e.clubDM.ID)
	res := exec(t, e.tools(t), "search_agent_memory", `{"agent":"club","query":"next book","limit":5}`)
	if !res.B("ok") || res.S("agent.slug") != "club" || res.Len("results") != 3 {
		t.Fatal(res)
	}
	// The agent's own first, then its chats, newest conversation first.
	if got := strings.Join(res.Strs("results", "container"), ","); got != "agent-club,"+groupMem+","+dmMem {
		t.Fatal(got)
	}
	if res.Has("results.0.chat") || res.S("results.1.chat.id") != e.clubGroup.ID || res.S("results.1.chat.title") != "Book club" || res.S("results.2.chat.id") != e.clubDM.ID {
		t.Fatal(res)
	}
	if !res.Has("results.0.result") || res.Has("results.0.error") {
		t.Fatal(res.JSON("results.0"))
	}
	want := []string{
		`[{"container":"agent-club","limit":5,"query":"next book","tool":"search_memories"}]`,
		`[{"container":"` + groupMem + `","limit":5,"query":"next book","tool":"search_memories"}]`,
		`[{"container":"` + dmMem + `","limit":5,"query":"next book","tool":"search_memories"}]`,
	}
	sort.Strings(want)
	if got := sortedArgs(e.f); got != strings.Join(want, ",") {
		t.Fatal(got)
	}
}

func TestSearchAgentMemorySkipsPerChatContainersRecallDoesntHave(t *testing.T) {
	e := memSetup(t, fakeOpts{containers: func() []string { return []string{"agent-club"} }})
	groupMem := agents.ChatContainer("agent-club", e.clubGroup.ID)
	if got := strings.Join(exec(t, e.tools(t), "search_agent_memory", `{"agent":"club","query":"x"}`).Strs("results", "container"), ","); got != "agent-club" {
		t.Fatal(got)
	}
	// A named chat is read even when recall doesn't list it yet.
	res := exec(t, e.tools(t), "search_agent_memory", `{"agent":"club","query":"x","chat":"`+e.clubGroup.ID+`"}`)
	if got := strings.Join(res.Strs("results", "container"), ","); got != groupMem || res.S("results.0.chat.id") != e.clubGroup.ID {
		t.Fatal(res)
	}
}

func TestSearchAgentMemoryWithoutPerChatMemoryReadsOnlyItsContainerNeverItsReadList(t *testing.T) {
	e := memSetup(t, fakeOpts{})
	for _, in := range []string{`{"agent":"shop","query":"boxes"}`, `{"agent":"shop","query":"boxes","chat":"` + e.shopGroup.ID + `"}`} {
		res := exec(t, e.tools(t), "search_agent_memory", in)
		if got := strings.Join(res.Strs("results", "container"), ","); got != "agent-shop" || res.Has("results.0.chat") {
			t.Fatal(in, res)
		}
	}
	for _, a := range e.f.Args() {
		if a["container"] != "agent-shop" {
			t.Fatal(a)
		}
	}
}

func TestAgentMemoryRefusesPaluiTheOwnersContainerAndOtherAgentsChats(t *testing.T) {
	e := memSetup(t, fakeOpts{})
	set := e.tools(t)
	for _, ref := range []string{"palui", e.palui.ID, "nobody"} {
		if r := exec(t, set, "search_agent_memory", `{"agent":"`+ref+`","query":"x"}`); r.B("ok") || !strings.Contains(r.S("error"), "No other agent") {
			t.Fatal(ref, r)
		}
	}
	for _, c := range []db.Chat{e.shopGroup, e.ownerDM, e.paluiGroup} {
		if r := exec(t, set, "list_agent_memory", `{"agent":"club","chat":"`+c.ID+`"}`); r.B("ok") || !strings.Contains(r.S("error"), "isn't one of club's chats") {
			t.Fatal(c.Title, r)
		}
	}
	// An agent pointed at the owner's container (set before that was refused)
	// reads nothing at all.
	_, err := e.d.Exec("UPDATE agents SET recall_container = 'owner-mem', recall_per_chat = 0 WHERE id = ?", e.shop.ID)
	testutil.Must(t, err)
	if r := exec(t, e.tools(t), "search_agent_memory", `{"agent":"shop","query":"x"}`); r.B("ok") || r.S("error") != "shop has no memory Palui may read." {
		t.Fatal(r)
	}
	for _, a := range e.f.Args() {
		if a["container"] == "owner-mem" {
			t.Fatal("read the owner's container")
		}
	}
	if len(e.f.Args()) != 0 {
		t.Fatal(e.f.Args())
	}
}

func TestListAgentMemoryPassesTheCursorOnlyForOneContainer(t *testing.T) {
	var groupMem string
	e := memSetup(t, fakeOpts{containers: func() []string { return []string{"agent-club", groupMem} }})
	groupMem = agents.ChatContainer("agent-club", e.clubGroup.ID)
	set := e.tools(t)
	if r := exec(t, set, "list_agent_memory", `{"agent":"club","cursor":"c1"}`); r.B("ok") || !strings.Contains(r.S("error"), "cursor only works on one container") {
		t.Fatal(r)
	}
	res := exec(t, set, "list_agent_memory", `{"agent":"club","chat":"`+e.clubGroup.ID+`","cursor":"c1","limit":20}`)
	if !res.B("ok") || res.Len("results") != 1 {
		t.Fatal(res)
	}
	if got := argsJSON(e.f.Args()); got != `[{"container":"`+groupMem+`","cursor":"c1","limit":20,"tool":"list_memories"}]` {
		t.Fatal(got)
	}
}

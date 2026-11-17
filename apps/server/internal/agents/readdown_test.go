package agents_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type readDownEnv struct {
	d                                 *db.DB
	palui, club                       db.Agent
	ownerDM, web, group, guestDM, bot db.Chat
}

func readDownSetup(t *testing.T) readDownEnv {
	t.Helper()
	d := testutil.DB(t)
	_, err := owner.SetWhatsAppOwner(d, "6281200000001", 1)
	testutil.Must(t, err)
	palui := def(t, d)
	club := create(t, d, agents.Input{Slug: "club", Name: s("Club"), Persona: s("p")})
	ensure := func(in chats.EnsureInput) db.Chat {
		c, err := chats.Ensure(d, in, 1)
		testutil.Must(t, err)
		return c
	}
	return readDownEnv{
		d: d, palui: palui, club: club,
		ownerDM: ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281200000001@s.whatsapp.net", Title: "Owner"}),
		web:     ensure(chats.EnsureInput{Channel: "web", ExternalID: "web", Title: "Web"}),
		group:   ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000001@g.us", Title: "Book club", Kind: "group", AgentID: &club.ID}),
		guestDM: ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281200000002@s.whatsapp.net", Title: "Guest"}),
		bot:     ensure(chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000002@g.us", Title: "Palui group", Kind: "group"}),
	}
}

func TestCanReadDownOnlyForPaluiInTheOwnersOwnChats(t *testing.T) {
	e := readDownSetup(t)
	base := connectors.TurnInfo{ChatID: e.ownerDM.ID, Channel: "whatsapp", Trigger: "message", Origin: "message", Mode: "owner", AgentID: e.palui.ID}
	cases := []struct {
		name string
		mut  func(*connectors.TurnInfo)
		want bool
	}{
		{"owner DM", func(*connectors.TurnInfo) {}, true},
		{"web chat", func(x *connectors.TurnInfo) { x.ChatID, x.Channel = e.web.ID, "web" }, true},
		{"mode left empty", func(x *connectors.TurnInfo) { x.Mode = "" }, true},
		{"agent from the chat", func(x *connectors.TurnInfo) { x.AgentID = "" }, true},
		{"approval resume of a message", func(x *connectors.TurnInfo) { x.Trigger = "approval" }, true},
		{"owner's scheduled job", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "schedule", "schedule" }, true},
		{"another agent", func(x *connectors.TurnInfo) { x.AgentID = e.club.ID }, false},
		{"another agent's group", func(x *connectors.TurnInfo) { x.ChatID, x.AgentID = e.group.ID, e.club.ID }, false},
		{"a group Palui answers in", func(x *connectors.TurnInfo) { x.ChatID = e.bot.ID }, false},
		{"a guest's DM", func(x *connectors.TurnInfo) { x.ChatID = e.guestDM.ID }, false},
		{"outside audience", func(x *connectors.TurnInfo) { x.Mode = "outside" }, false},
		{"legacy shared approval", func(x *connectors.TurnInfo) { x.Mode = "sharedOwner" }, false},
		{"unknown mode", func(x *connectors.TurnInfo) { x.Mode = "members" }, false},
		{"hook", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "hook", "hook" }, false},
		{"flow", func(x *connectors.TurnInfo) { x.Trigger, x.Origin, x.FlowID = "flow", "flow", "flw_1" }, false},
		{"flow id on a message", func(x *connectors.TurnInfo) { x.FlowID = "flw_1" }, false},
		{"reminder", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "reminder", "reminder" }, false},
		{"approval resume of a hook", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "approval", "hook" }, false},
		{"unknown trigger", func(x *connectors.TurnInfo) { x.Trigger, x.Origin = "poll", "" }, false},
		{"no chat", func(x *connectors.TurnInfo) { x.ChatID = "" }, false},
		{"missing chat", func(x *connectors.TurnInfo) { x.ChatID = "chat_missing" }, false},
		{"missing agent", func(x *connectors.TurnInfo) { x.AgentID = "agt_missing" }, false},
	}
	for _, c := range cases {
		turn := base
		c.mut(&turn)
		got, err := agents.CanReadDown(e.d, turn, 1)
		testutil.Must(t, err)
		if got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestCanReadDownIsFalseOnceTheOwnersChatMovedToAnotherAgent(t *testing.T) {
	e := readDownSetup(t)
	// /agent club in the owner's DM while Palui's approval waits: the resume
	// still carries Palui's id.
	_, err := e.d.Exec("UPDATE chats SET agent_id = ? WHERE id = ?", e.club.ID, e.ownerDM.ID)
	testutil.Must(t, err)
	for _, agentID := range []string{e.palui.ID, ""} {
		turn := connectors.TurnInfo{ChatID: e.ownerDM.ID, Channel: "whatsapp", Trigger: "approval", Origin: "message", Mode: "owner", AgentID: agentID}
		got, err := agents.CanReadDown(e.d, turn, 1)
		testutil.Must(t, err)
		if got {
			t.Fatalf("agent %q: read down in a chat club answers in", agentID)
		}
	}
}

func TestOthersAndOtherLeaveOutTheDefaultAgent(t *testing.T) {
	e := readDownSetup(t)
	shop := create(t, e.d, agents.Input{Slug: "shop", Name: s("Shop"), Persona: s("p")})
	list, err := agents.Others(e.d)
	testutil.Must(t, err)
	var slugs []string
	for _, a := range list {
		slugs = append(slugs, a.Slug)
	}
	if strings.Join(slugs, ",") != "club,shop" {
		t.Fatal(slugs)
	}
	for _, ref := range []string{"shop", "SHOP", shop.ID} {
		if a, ok, err := agents.Other(e.d, ref); err != nil || !ok || a.ID != shop.ID {
			t.Fatal(ref, ok, err)
		}
	}
	for _, ref := range []string{"palui", e.palui.ID, "nobody", ""} {
		if _, ok, err := agents.Other(e.d, ref); err != nil || ok {
			t.Fatal(ref, ok, err)
		}
	}
}

func TestMemoryContainersAreTheAgentsOwnAndNeverTheOwners(t *testing.T) {
	e := readDownSetup(t)
	yes := true
	got, err := agents.MemoryContainers(e.d, e.club, []string{e.group.ID})
	testutil.Must(t, err)
	if strings.Join(got, ",") != "agent-club" {
		t.Fatal(got)
	}
	perChat := update(t, e.d, e.club.ID, agents.Input{RecallPerChat: &yes})
	got, err = agents.MemoryContainers(e.d, perChat, []string{e.group.ID, e.group.ID})
	testutil.Must(t, err)
	if strings.Join(got, ",") != "agent-club,"+agents.ChatContainer("agent-club", e.group.ID) {
		t.Fatal(got)
	}
	got, err = agents.MemoryContainers(e.d, e.palui, []string{e.ownerDM.ID})
	testutil.Must(t, err)
	if len(got) != 0 {
		t.Fatal(got)
	}
	// An agent whose container was set to the owner's before that was refused.
	_, err = e.d.Exec("UPDATE agents SET recall_container = 'default', recall_per_chat = 0 WHERE id = ?", e.club.ID)
	testutil.Must(t, err)
	club, _, err := agents.Get(e.d, e.club.ID)
	testutil.Must(t, err)
	testutil.SeedConnector(t, e.d, "recall", "memory", testutil.SeedOptions{}, 1)
	got, err = agents.MemoryContainers(e.d, club, nil)
	testutil.Must(t, err)
	if len(got) != 0 {
		t.Fatal(got)
	}
}

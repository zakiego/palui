package chat_test

// Who can read a chat (PLAN 19.4): one case per row of the audience table.
// Fake numbers only.

import (
	"errors"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	ptools "github.com/zakiego/palui/apps/server/internal/tools"
)

const (
	waOwnerPhone = "6281200000009"
	waOwner      = "6281200000009@s.whatsapp.net"
	waOwnerLID   = "100000000000009@lid"
	waAni        = "6281200000001@s.whatsapp.net"
	waAniLID     = "100000000000001@lid"
	waBudi       = "6281200000002@s.whatsapp.net"
	waStranger   = "6281200000003@s.whatsapp.net"
	waGroup      = "120363000000000001@g.us"
	tgAni        = "4242"
)

type audienceWorld struct {
	h    *chattest.Harness
	wa   *conntest.FakeChannel
	chef db.Agent
	cs   db.Agent
	ani  members.View
}

// audienceSetup: the owner on Telegram (42) and WhatsApp, agent chef with
// member Ani (a WhatsApp phone and a Telegram id) and Budi, agent cs with no
// members. lister false runs WhatsApp as a channel that can't list groups.
func audienceSetup(t *testing.T, lister bool) *audienceWorld {
	t.Helper()
	return audienceSetupWith(t, lister, nil, nil)
}

func audienceSetupWith(t *testing.T, lister bool, reg map[string]*connectors.Runtime, rows []chattest.Row) *audienceWorld {
	t.Helper()
	d := testutil.DB(t)
	chattest.OwnTelegram(t, d)
	_, err := owner.SetWhatsAppOwner(d, waOwnerPhone, 1)
	testutil.Must(t, err)
	_, err = d.Exec("UPDATE identities SET alt_id = ? WHERE channel = 'whatsapp'", waOwnerLID)
	testutil.Must(t, err)
	wa := conntest.NewFakeChannel("whatsapp")
	var handle connectors.ChannelHandle = wa
	if lister {
		handle = wa.Lister()
	}
	all := map[string]*connectors.Runtime{"whatsapp": conntest.ChannelRuntime("whatsapp", handle)}
	for k, v := range reg {
		all[k] = v
	}
	h := chattest.New(t, chattest.Options{DB: d, Registry: all, Rows: append([]chattest.Row{{"whatsapp", "channel"}}, rows...)})
	chef := need(agents.Create(d, agents.Input{Slug: "chef", Name: sp("Chef"), Persona: sp("p")}, 1))
	cs := need(agents.Create(d, agents.Input{Slug: "cs", Name: sp("CS"), Persona: sp("p")}, 1))
	ani := need(members.Create(d, "Ani", []members.AccountInput{{Channel: "whatsapp", Address: waAni}, {Channel: "telegram", Address: tgAni}}, 1))
	budi := need(members.Create(d, "Budi", []members.AccountInput{{Channel: "whatsapp", Address: waBudi}}, 1))
	testutil.Must(t, members.Link(d, chef.ID, ani.ID, 1))
	testutil.Must(t, members.Link(d, chef.ID, budi.ID, 1))
	return &audienceWorld{h: h, wa: wa, chef: chef, cs: cs, ani: ani}
}

func (w *audienceWorld) chat(t *testing.T, channel, externalID, kind string) db.Chat {
	t.Helper()
	return need(chats.Ensure(w.h.DB, chats.EnsureInput{Channel: channel, ExternalID: externalID, Kind: kind}, 1))
}

func (w *audienceWorld) audience(t *testing.T, c db.Chat, a db.Agent) string {
	t.Helper()
	got, err := chat.Audience(w.h.Engine.Ctx, c, a)
	testutil.Must(t, err)
	return got
}

func TestAudience(t *testing.T) {
	t.Run("the owner's DM and the web chat are the owner's", func(t *testing.T) {
		w := audienceSetup(t, true)
		eq(t, w.audience(t, w.chat(t, "telegram", "42", "dm"), w.chef), ptools.AudienceOwner)
		eq(t, w.audience(t, w.chat(t, "whatsapp", waOwner, "dm"), w.chef), ptools.AudienceOwner)
		eq(t, w.audience(t, need(chats.EnsureWebChat(w.h.DB, 1)), w.chef), ptools.AudienceOwner)
	})

	t.Run("a member's DM is members only on their own agent", func(t *testing.T) {
		w := audienceSetup(t, true)
		eq(t, w.audience(t, w.chat(t, "whatsapp", waAni, "dm"), w.chef), ptools.AudienceMembers)
		eq(t, w.audience(t, w.chat(t, "telegram", tgAni, "dm"), w.chef), ptools.AudienceMembers)
		// cs has no members at all, so Ani is a stranger there.
		eq(t, w.audience(t, w.chat(t, "whatsapp", waAni, "dm"), w.cs), ptools.AudienceOutside)
		// A member of another agent is a stranger too.
		lone := need(members.Create(w.h.DB, "Lone", []members.AccountInput{{Channel: "telegram", Address: "5151"}}, 1))
		testutil.Must(t, members.Link(w.h.DB, w.cs.ID, lone.ID, 1))
		eq(t, w.audience(t, w.chat(t, "whatsapp", waAni, "dm"), w.cs), ptools.AudienceOutside)
		eq(t, w.audience(t, w.chat(t, "telegram", "5151", "dm"), w.chef), ptools.AudienceOutside)
	})

	t.Run("a stranger's DM is outside", func(t *testing.T) {
		w := audienceSetup(t, true)
		eq(t, w.audience(t, w.chat(t, "whatsapp", waStranger, "dm"), w.chef), ptools.AudienceOutside)
		eq(t, w.audience(t, w.chat(t, "telegram", "7777", "dm"), w.chef), ptools.AudienceOutside)
	})

	t.Run("an agent with no members never asks the channel", func(t *testing.T) {
		w := audienceSetup(t, true)
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waOwner}}}
		eq(t, w.audience(t, w.chat(t, "whatsapp", waGroup, "group"), w.cs), ptools.AudienceOutside)
		eq(t, w.wa.ParticipantCalls(), 0)
	})

	t.Run("the default agent is outside even with a forced member link", func(t *testing.T) {
		w := audienceSetup(t, true)
		palui := need(agents.GetDefault(w.h.DB, 1))
		_, err := w.h.DB.Exec("INSERT INTO agent_members (agent_id, member_id, created_at) VALUES (?, ?, 1)", palui.ID, w.ani.ID)
		testutil.Must(t, err)
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waAni}}}
		eq(t, w.audience(t, w.chat(t, "whatsapp", waAni, "dm"), palui), ptools.AudienceOutside)
		eq(t, w.audience(t, w.chat(t, "whatsapp", waGroup, "group"), palui), ptools.AudienceOutside)
		eq(t, w.wa.ParticipantCalls(), 0)
	})

	t.Run("a WhatsApp group of the owner and members is members only", func(t *testing.T) {
		w := audienceSetup(t, true)
		g := w.chat(t, "whatsapp", waGroup, "group")
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waOwnerLID, AltID: waOwner}, {ExternalID: waAni}, {ExternalID: waBudi}}}
		eq(t, w.audience(t, g, w.chef), ptools.AudienceMembers)
		// Plus one stranger: outside.
		w.wa.Participants[waGroup] = append(w.wa.Participants[waGroup], connectors.Participant{ExternalID: waStranger})
		eq(t, w.audience(t, g, w.chef), ptools.AudienceOutside)
		// The same group on an agent the members aren't in.
		lone := need(members.Create(w.h.DB, "Lone", []members.AccountInput{{Channel: "telegram", Address: "5151"}}, 1))
		testutil.Must(t, members.Link(w.h.DB, w.cs.ID, lone.ID, 1))
		w.wa.Participants[waGroup] = []connectors.Participant{{ExternalID: waOwner}, {ExternalID: waAni}}
		eq(t, w.audience(t, g, w.cs), ptools.AudienceOutside)
	})

	t.Run("an unreadable list is outside", func(t *testing.T) {
		w := audienceSetup(t, true)
		g := w.chat(t, "whatsapp", waGroup, "group")
		// Not in the list at all.
		eq(t, w.audience(t, g, w.chef), ptools.AudienceOutside)
		// An error.
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waAni}}}
		w.wa.ParticipantsErr = errors.New("rate limited")
		eq(t, w.audience(t, g, w.chef), ptools.AudienceOutside)
		// Slower than the timeout.
		w.wa.ParticipantsErr = nil
		w.wa.ParticipantsDelay = 500 * time.Millisecond
		w.h.Engine.Ctx.GroupLookupTimeout = 20 * time.Millisecond
		start := time.Now()
		eq(t, w.audience(t, g, w.chef), ptools.AudienceOutside)
		if time.Since(start) > 400*time.Millisecond {
			t.Fatal("waited past the timeout")
		}
		// An empty list.
		w.wa.ParticipantsDelay = 0
		w.wa.Participants[waGroup] = nil
		eq(t, w.audience(t, g, w.chef), ptools.AudienceOutside)
	})

	t.Run("a channel that can't list groups is outside", func(t *testing.T) {
		w := audienceSetup(t, false)
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waAni}}}
		eq(t, w.audience(t, w.chat(t, "whatsapp", waGroup, "group"), w.chef), ptools.AudienceOutside)
		eq(t, w.wa.ParticipantCalls(), 0)
		// A member's DM still works there: it needs no list.
		eq(t, w.audience(t, w.chat(t, "whatsapp", waAni, "dm"), w.chef), ptools.AudienceMembers)
	})

	t.Run("every Telegram group is outside", func(t *testing.T) {
		w := audienceSetup(t, true)
		eq(t, w.audience(t, w.chat(t, "telegram", "-100777", "group"), w.chef), ptools.AudienceOutside)
	})

	t.Run("a member matched only through a learned alt id", func(t *testing.T) {
		w := audienceSetup(t, true)
		testutil.Must(t, members.Learn(w.h.DB, members.Sender{Channel: "whatsapp", ExternalID: waAniLID, AltID: waAni}))
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waOwner}, {ExternalID: waAniLID}}}
		eq(t, w.audience(t, w.chat(t, "whatsapp", waGroup, "group"), w.chef), ptools.AudienceMembers)
		eq(t, w.audience(t, w.chat(t, "whatsapp", waAniLID, "dm"), w.chef), ptools.AudienceMembers)
	})
}

// /tools in a WhatsApp group lists the agent's own tools only while everyone
// there is the owner or a member.
func TestToolsCommandInMembersGroup(t *testing.T) {
	w := audienceSetupWith(t, true, memberRegistry(&executedLog{}), memberRows)
	need(connectorsSetOwner(w.h.DB, "crm", w.chef.ID))
	g := need(chats.Ensure(w.h.DB, chats.EnsureInput{Channel: "whatsapp", ExternalID: waGroup, Kind: "group", AgentID: &w.chef.ID}, 1))
	w.wa.Participants = map[string][]connectors.Participant{waGroup: {{ExternalID: waOwner}, {ExternalID: waAni}}}
	text, err := chat.ToolsText(w.h.Engine.Ctx, g, nil)
	testutil.Must(t, err)
	contains(t, text, "crm (")
	contains(t, text, "rem (")
	notContains(t, text, "glob")
	notContains(t, text, "alpha")
	contains(t, text, "tanpa minta izin")

	w.wa.Participants[waGroup] = append(w.wa.Participants[waGroup], connectors.Participant{ExternalID: waStranger})
	text, err = chat.ToolsText(w.h.Engine.Ctx, g, nil)
	testutil.Must(t, err)
	notContains(t, text, "crm")
	contains(t, text, "rem (")
}

package chat_test

// Who the agent can @mention in a WhatsApp group (ADR 0002). Fake names and
// numbers only.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	lidAni  = "100000000000001@lid"
	lidBudi = "100000000000002@lid"
	lidCita = "100000000000004@lid"
)

func addSender(t *testing.T, q db.Q, chatID, id, senderID, name string, at int64) {
	t.Helper()
	_, err := q.Exec("INSERT INTO messages (id, chat_id, role, content, text, sender_id, sender_name, from_owner, status, created_at) VALUES (?, ?, 'user', '{}', 'hi', ?, ?, 0, 'handled', ?)", id, chatID, senderID, name, at)
	testutil.Must(t, err)
}

func TestMentionsFor(t *testing.T) {
	t.Run("lists people in the group by the name they last wrote under, newest writer first", func(t *testing.T) {
		w := audienceSetup(t, true)
		g := w.chat(t, "whatsapp", waGroup, "group")
		// A LID group: participants are LIDs, one also known by phone.
		w.wa.Participants = map[string][]connectors.Participant{waGroup: {
			{ExternalID: lidAni, AltID: waAni},
			{ExternalID: lidBudi},
			{ExternalID: lidCita},
		}}
		addSender(t, w.h.DB, g.ID, "m1", lidAni, "Ani Old", 1)
		addSender(t, w.h.DB, g.ID, "m2", lidBudi, "Budi [Owner]", 2)
		addSender(t, w.h.DB, g.ID, "m3", lidAni, "Ani", 3)
		// Wrote under the phone JID: still matched to the LID participant, once.
		addSender(t, w.h.DB, g.ID, "m4", waAni, "Ani Phone", 0)
		// Left the group: never listed.
		addSender(t, w.h.DB, g.ID, "m5", "100000000000003@lid", "Gone", 4)
		got, err := chat.MentionsFor(w.h.Engine.Ctx, g)
		testutil.Must(t, err)
		eq(t, fmt.Sprint(got.People), fmt.Sprint([]chat.MentionTarget{
			{Name: "Ani", Token: "100000000000001"},
			{Name: "Budi Owner", Token: "100000000000002"},
		}))
	})

	t.Run("a group whose list can't be read gets nobody", func(t *testing.T) {
		w := audienceSetup(t, true)
		g := w.chat(t, "whatsapp", waGroup, "group")
		addSender(t, w.h.DB, g.ID, "m1", lidAni, "Ani", 1)
		w.wa.ParticipantsErr = errors.New("not connected")
		got, err := chat.MentionsFor(w.h.Engine.Ctx, g)
		testutil.Must(t, err)
		eq(t, got != nil && len(got.People) == 0, true)
	})

	t.Run("DMs and other channels have no mention list", func(t *testing.T) {
		w := audienceSetup(t, true)
		for _, c := range []db.Chat{w.chat(t, "whatsapp", waAni, "dm"), w.chat(t, "telegram", "-1001", "group")} {
			got, err := chat.MentionsFor(w.h.Engine.Ctx, c)
			testutil.Must(t, err)
			eq(t, got == nil, true)
		}
	})
}

func TestMentionTargetsCap(t *testing.T) {
	var people []connectors.Participant
	names := map[string]string{}
	var order []string
	for i := 0; i < chat.MaxMentionPeople+5; i++ {
		id := fmt.Sprintf("1000000000%05d@lid", i)
		people = append(people, connectors.Participant{ExternalID: id})
		names[id] = "Person"
		order = append(order, id)
	}
	eq(t, len(chat.MentionTargets(people, names, order)), chat.MaxMentionPeople)
}

// rpt_0mwrwosczfzc3p58slq: asked whether it could tag people in a WhatsApp
// group, the agent said it could only write names as plain text. The prompt
// now says it can mention, how, and whom.
func TestSystemPromptMentions(t *testing.T) {
	base := chat.SystemInput{Persona: "p", Channel: "whatsapp", Chat: &chat.SystemChat{ID: "chat_1", ExternalID: waGroup, Kind: "group"}}

	with := base
	with.Mentions = &chat.MentionInfo{People: []chat.MentionTarget{{Name: "Ani", Token: "100000000000001"}, {Name: "Budi", Token: "100000000000002"}}}
	got := chat.BuildSystem(with)
	for _, want := range []string{
		"You can @mention people in this group",
		"Ani: @100000000000001; Budi: @100000000000002.",
		"At most 10 people per message",
		"never make up an id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, got)
		}
	}

	empty := base
	empty.Mentions = &chat.MentionInfo{}
	if got := chat.BuildSystem(empty); !strings.Contains(got, "Right now nobody can be mentioned") {
		t.Fatalf("empty list not explained:\n%s", got)
	}

	if got := chat.BuildSystem(base); strings.Contains(got, "@mention people") {
		t.Fatalf("mention text outside a WhatsApp group:\n%s", got)
	}
}

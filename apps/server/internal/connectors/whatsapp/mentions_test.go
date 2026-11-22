package whatsapp

// Outgoing @mentions (ADR 0002). Fake numbers only.

import (
	"fmt"
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/zakiego/palui/apps/server/internal/connectors"
)

const (
	tAniLID  = "100000000000001@lid"
	tAniPN   = "6281200000001@s.whatsapp.net"
	tBudiLID = "100000000000002@lid"
	tCitaPN  = "6281200000003@s.whatsapp.net"
)

var tPeople = []connectors.Participant{
	{ExternalID: tAniLID, AltID: tAniPN},
	{ExternalID: tBudiLID},
	{ExternalID: tCitaPN},
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestResolveMentions(t *testing.T) {
	t.Run("a LID token mentions the LID", func(t *testing.T) {
		same(t, ResolveMentions("@100000000000002 cek ini", tPeople, MaxMentions), []string{tBudiLID})
	})
	t.Run("a phone token mentions the phone JID, so text and JID line up", func(t *testing.T) {
		same(t, ResolveMentions("halo @6281200000003 dan @6281200000001", tPeople, MaxMentions), []string{tCitaPN, tAniPN})
	})
	t.Run("someone known by both forms is matched by either", func(t *testing.T) {
		same(t, ResolveMentions("@100000000000001", tPeople, MaxMentions), []string{tAniLID})
	})
	t.Run("an id that isn't in the group stays plain text", func(t *testing.T) {
		same(t, ResolveMentions("@100000000000009 sama @Budi", tPeople, MaxMentions), nil)
	})
	t.Run("emails, words and longer numbers aren't tokens", func(t *testing.T) {
		same(t, ResolveMentions("a@100000000000002 x.@100000000000002 @100000000000002abc @1000000000000021", tPeople, MaxMentions), nil)
	})
	t.Run("each person once, in order, punctuation after the token is fine", func(t *testing.T) {
		same(t, ResolveMentions("(@100000000000002), @100000000000001. @100000000000002!", tPeople, MaxMentions), []string{tBudiLID, tAniLID})
	})
	t.Run("past the cap tokens stay plain text", func(t *testing.T) {
		var people []connectors.Participant
		text := ""
		for i := 0; i < MaxMentions+3; i++ {
			people = append(people, connectors.Participant{ExternalID: fmt.Sprintf("1000000000%05d@lid", i)})
			text += fmt.Sprintf("@1000000000%05d ", i)
		}
		got := ResolveMentions(text, people, MaxMentions)
		if len(got) != MaxMentions || got[0] != "100000000000000@lid" {
			t.Fatalf("got %d mentions: %q", len(got), got)
		}
	})
}

func TestMentionsIn(t *testing.T) {
	jids := []string{tAniLID, tBudiLID}
	same(t, MentionsIn("only @100000000000002 here", jids), []string{tBudiLID})
	same(t, MentionsIn("nobody", jids), nil)
	same(t, MentionsIn("@100000000000001", nil), nil)
}

func TestBuildTextMessage(t *testing.T) {
	t.Run("no quote and no mentions stays a plain conversation", func(t *testing.T) {
		m := buildTextMessage("halo", withMentions(nil, nil))
		if m.GetConversation() != "halo" || m.ExtendedTextMessage != nil {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("mentions go in ContextInfo.MentionedJID of an extended text", func(t *testing.T) {
		m := buildTextMessage("@100000000000002 halo", withMentions(nil, []string{tBudiLID}))
		ext := m.GetExtendedTextMessage()
		if m.Conversation != nil || ext.GetText() != "@100000000000002 halo" {
			t.Fatalf("got %v", m)
		}
		same(t, ext.GetContextInfo().GetMentionedJID(), []string{tBudiLID})
	})
	t.Run("a quote keeps its fields next to the mentions", func(t *testing.T) {
		quote := &waE2E.ContextInfo{StanzaID: proto.String("IN1"), Participant: proto.String(tAniLID)}
		ci := buildTextMessage("@6281200000003", withMentions(quote, []string{tCitaPN})).GetExtendedTextMessage().GetContextInfo()
		if ci.GetStanzaID() != "IN1" || ci.GetParticipant() != tAniLID {
			t.Fatalf("quote lost: %v", ci)
		}
		same(t, ci.GetMentionedJID(), []string{tCitaPN})
	})
}

package chat_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	ptools "github.com/zakiego/palui/apps/server/internal/tools"
)

// collectedWith is a tool set: each entry is connector id, label, tool names.
func collectedWith(conns ...[]string) *ptools.Collected {
	c := &ptools.Collected{Tools: jsonx.NewMap[connectors.Tool](), Meta: map[string]*ptools.ToolMeta{}}
	for _, conn := range conns {
		id, label := conn[0], conn[1]
		for _, t := range conn[2:] {
			name := id + "__" + t
			c.Tools.Set(name, connectors.Tool{})
			c.Meta[name] = &ptools.ToolMeta{Name: name, ConnectorID: id, Tool: t, Label: label}
		}
	}
	return c
}

var (
	reminders = []string{"reminders", "Reminders", "add_reminder", "list_reminders"}
	finansial = []string{"mcp-server", "Finansial", "create_transaction", "list_accounts"}
)

func snap(audience string, c *ptools.Collected) chat.SetupSnapshot {
	return chat.TakeSetupSnapshot(chat.SetupInput{Version: "0.10.0", Commit: "aaaaaaa1", Audience: audience, Tools: c, Persona: "p", Notes: "n"})
}

func TestSetupChangesNothingChanged(t *testing.T) {
	a := snap(ptools.AudienceOwner, collectedWith(reminders, finansial))
	b := snap(ptools.AudienceOwner, collectedWith(finansial, reminders))
	if got := chat.SetupChanges(a, b, nil); got != nil {
		t.Fatalf("no change should give no lines, got %q", got)
	}
	if chat.SetupNote(nil) != "" {
		t.Fatal("no lines should give no note")
	}
}

// The report: a WhatsApp group with only the owner counted as outside, so
// Finansial was missing; after the fix it counts as the owner's chat.
func TestSetupChangesConnectorAndAudience(t *testing.T) {
	before := snap(ptools.AudienceOutside, collectedWith(reminders))
	after := snap(ptools.AudienceOwner, collectedWith(reminders, finansial))
	note := chat.SetupNote(chat.SetupChanges(before, after, nil))
	for _, want := range []string{
		"Since your last reply in this conversation, your setup changed:",
		"This chat now counts as the owner's own chat (before, it counted as a chat with people who aren't the owner)",
		"- Now available in this chat: Finansial (2 tools).",
		"not as your mistake",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note misses %q:\n%s", want, note)
		}
	}
	back := chat.SetupChanges(after, snap(ptools.AudienceOwner, collectedWith(reminders)), nil)
	if len(back) != 1 || back[0] != "No longer available in this chat: Finansial." {
		t.Fatalf("removed connector: %q", back)
	}
}

func TestSetupChangesToolsVersionAndText(t *testing.T) {
	before := snap(ptools.AudienceOwner, collectedWith(finansial))
	more := append(append([]string{}, finansial...), "delete_transaction")
	after := chat.TakeSetupSnapshot(chat.SetupInput{Version: "0.11.0", Commit: "bbbbbbb2", Audience: ptools.AudienceOwner, Tools: collectedWith(more), Persona: "p", Notes: "n2", ChatRules: "r"})
	got := chat.SetupChanges(before, after, []string{"fix(server): count a WhatsApp group of only the owner as the owner's chat"})
	want := []string{
		"Palui was updated to version 0.11.0 (it was 0.10.0). Changelog: https://palui.zakiego.com/docs/changelog",
		"What the update changed: fix(server): count a WhatsApp group of only the owner as the owner's chat.",
		"New Finansial tools: delete_transaction.",
		"Your standing instructions or preferences were edited.",
		"This chat's rules were edited.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	sameVersion := after
	sameVersion.Version, sameVersion.Commit = "0.10.0", "ccccccc3"
	sameVersion.Connectors, sameVersion.Notes, sameVersion.ChatRules = before.Connectors, before.Notes, before.ChatRules
	got = chat.SetupChanges(before, sameVersion, nil)
	if len(got) != 1 || got[0] != "Palui was updated (version 0.10.0, new build ccccccc)." {
		t.Fatalf("new build on the same version: %q", got)
	}
}

func TestSetupChangesCapsLongLists(t *testing.T) {
	before := snap(ptools.AudienceOwner, collectedWith(finansial))
	more := append([]string{}, finansial...)
	for _, n := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"} {
		more = append(more, n)
	}
	got := chat.SetupChanges(before, snap(ptools.AudienceOwner, collectedWith(more)), nil)
	if len(got) != 1 || got[0] != "New Finansial tools: t1, t2, t3, t4, t5, t6 and 2 more." {
		t.Fatalf("got %q", got)
	}
}

// The note shows on the first turn after a change and not again.
func TestSessionSetupNoteShowsOnce(t *testing.T) {
	d := testutil.DB(t)
	c := need(chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "1@g.us", Kind: "group"}, testutil.NOW))
	load := func() *db.Session {
		s, _, err := chats.GetSession(d, *c.SessionID)
		testutil.Must(t, err)
		return &s
	}
	turn := func(s chat.SetupSnapshot) string {
		note, err := chat.SessionSetupNote(d, load(), s, func(string) []string { return nil })
		testutil.Must(t, err)
		return note
	}
	without := snap(ptools.AudienceOutside, collectedWith(reminders))
	with := snap(ptools.AudienceOwner, collectedWith(reminders, finansial))
	if note := turn(without); note != "" {
		t.Fatalf("a session's first turn has nothing to compare: %q", note)
	}
	if note := turn(without); note != "" {
		t.Fatalf("same setup: %q", note)
	}
	note := turn(with)
	if !strings.Contains(note, "Finansial (2 tools)") {
		t.Fatalf("the change turn misses the note: %q", note)
	}
	if note := turn(with); note != "" {
		t.Fatalf("the note repeated: %q", note)
	}
	ctx := chat.BuildTurnContext(chat.TurnContextInput{Now: testutil.NOW, TZ: "Asia/Makassar", SetupChanges: note})
	if !strings.Contains(ctx, "Since your last reply") || !strings.HasPrefix(ctx, chat.TurnContextTag) {
		t.Fatalf("turn context:\n%s", ctx)
	}
	if strings.Contains(chat.BuildTurnContext(chat.TurnContextInput{Now: testutil.NOW, TZ: "Asia/Makassar"}), "Since your last reply") {
		t.Fatal("no note without changes")
	}
}

package registry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatsconn "github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	"github.com/zakiego/palui/apps/server/internal/connectors/recall"
	"github.com/zakiego/palui/apps/server/internal/connectors/t3code"
	"github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type helpersGolden struct {
	GcalRanges []struct {
		From, To *string
		TZ       string `json:"tz"`
		Now      int64
		Out      json.RawMessage
	} `json:"gcalRanges"`
	GcalEvents []struct {
		Event struct {
			ID, CalendarID, Title, Start, End, Location string
			AllDay                                      bool
		}
		TZ  string `json:"tz"`
		Out string
	} `json:"gcalEvents"`
	RecallProfiles []struct {
		Profile recall.Profile
		About   *string
		Out     *string
	} `json:"recallProfiles"`
	RecallShould []struct {
		Q   string
		Out bool
	} `json:"recallShould"`
	RecallSourceIDs []struct {
		ID, Out string
	} `json:"recallSourceIds"`
	RecallContainers []struct {
		Asked any
		Cls   string
		Scope struct {
			Container string
			Read      []string
			Locked    bool
		}
		Out json.RawMessage
	} `json:"recallContainers"`
	T3Finished []struct {
		Status string
		Reply  *string
		Out    string
	} `json:"t3Finished"`
	T3Waiting   []string `json:"t3Waiting"`
	T3ThreadIDs []struct {
		In, Out string
	} `json:"t3ThreadIds"`
	T3Environments []struct {
		In  string
		Out *string
	} `json:"t3Environments"`
	T3Models []struct {
		In  string
		Out json.RawMessage
	} `json:"t3Models"`
	T3Match []struct {
		Q   string
		Out json.RawMessage
	} `json:"t3Match"`
	T3Fingerprints []struct {
		In, Out string
	} `json:"t3Fingerprints"`
	ChatTimes []struct {
		In  string
		Out *int64
	} `json:"chatTimes"`
	ChatClips []struct {
		In  string
		Max int
		Out string
	} `json:"chatClips"`
	WebfetchPages map[string]struct {
		Title, Text string
	} `json:"webfetchPages"`
}

func loadHelpers(t *testing.T) helpersGolden {
	raw, err := os.ReadFile("../../../testdata/connectors.json")
	testutil.Must(t, err)
	var g struct {
		Helpers helpersGolden `json:"helpers"`
	}
	testutil.Must(t, json.Unmarshal(raw, &g))
	return g.Helpers
}

func TestGcalHelpersMatchLuxon(t *testing.T) {
	h := loadHelpers(t)
	for _, c := range h.GcalRanges {
		r, err := gcal.ResolveRange(c.From, c.To, c.TZ, c.Now)
		var got any
		if err != nil {
			got = map[string]any{"error": err.Error()}
		} else {
			got = map[string]any{"min": r.Min, "max": r.Max, "fromIso": gcal.ISO(r.Min, c.TZ, true), "toIso": gcal.ISO(r.Max, c.TZ, true)}
		}
		if w, g := jsonx.Canonical(conntest.Of(json.RawMessage(c.Out)).V), jsonx.Canonical(conntest.Of(got).V); w != g {
			t.Errorf("range %v %v %s: want %s got %s", c.From, c.To, c.TZ, w, g)
		}
	}
	for _, c := range h.GcalEvents {
		e := gcal.Event{ID: c.Event.ID, CalendarID: c.Event.CalendarID, Title: c.Event.Title, Start: c.Event.Start, End: c.Event.End, AllDay: c.Event.AllDay, Location: c.Event.Location}
		if got := jsonx.Stringify(gcal.PresentEvent(e, c.TZ)); got != c.Out {
			t.Errorf("event %s %s:\n want %s\n got  %s", e.ID, c.TZ, c.Out, got)
		}
	}
}

func TestRecallHelpersMatch(t *testing.T) {
	h := loadHelpers(t)
	for _, c := range h.RecallProfiles {
		about := ""
		if c.About != nil {
			about = *c.About
		}
		text, ok := recall.ProfileText(c.Profile, about)
		if (c.Out == nil) == ok || (ok && text != *c.Out) {
			t.Errorf("profile: want %v got %q", c.Out, text)
		}
	}
	for _, c := range h.RecallShould {
		if recall.ShouldRecall(c.Q) != c.Out {
			t.Errorf("shouldRecall %q", c.Q)
		}
	}
	for _, c := range h.RecallSourceIDs {
		if got := recall.NormalizeSourceID(c.ID); got != c.Out {
			t.Errorf("sourceId %q: %q", c.ID, got)
		}
	}
	for _, c := range h.RecallContainers {
		got, err := recall.ResolveContainer(c.Asked, c.Cls, recall.Scope{Container: c.Scope.Container, Read: c.Scope.Read, Locked: c.Scope.Locked})
		var v any = got
		if err != nil {
			v = map[string]any{"error": err.Error()}
		}
		if w, g := jsonx.Canonical(conntest.Of(json.RawMessage(c.Out)).V), jsonx.Canonical(conntest.Of(v).V); w != g {
			t.Errorf("container %v %s: want %s got %s", c.Asked, c.Cls, w, g)
		}
	}
}

func TestT3CodeHelpersMatch(t *testing.T) {
	h := loadHelpers(t)
	watch := t3code.Watch{ThreadID: "t", RunID: testutil.Ptr("r"), ChatID: "c", Title: "Fix bug", Link: "https://app.t3.codes/threads/e/t"}
	for _, c := range h.T3Finished {
		reply := ""
		if c.Reply != nil {
			reply = *c.Reply
		}
		if got := t3code.FinishedText(watch, c.Status, reply); got != c.Out {
			t.Errorf("finished %s:\n want %q\n got  %q", c.Status, c.Out, got)
		}
	}
	noLink := watch
	noLink.Link = ""
	if got := []string{t3code.WaitingText(watch), t3code.WaitingText(noLink)}; strings.Join(got, "|") != strings.Join(h.T3Waiting, "|") {
		t.Errorf("waiting %q", got)
	}
	for _, c := range h.T3ThreadIDs {
		if got := t3code.ThreadIDOf(c.In); got != c.Out {
			t.Errorf("threadId %q: %q want %q", c.In, got, c.Out)
		}
	}
	for _, c := range h.T3Environments {
		got := t3code.EnvironmentIDOf(c.In)
		if (c.Out == nil && got != "") || (c.Out != nil && got != *c.Out) {
			t.Errorf("env %q: %q", c.In, got)
		}
	}
	for _, c := range h.T3Models {
		var got any
		if m := t3code.ParseModel(c.In); m != nil {
			got = m
		}
		if w, g := jsonx.Canonical(conntest.Of(json.RawMessage(c.Out)).V), jsonx.Canonical(conntest.Of(got).V); w != g {
			t.Errorf("model %q: want %s got %s", c.In, w, g)
		}
	}
	projects := []t3code.Project{
		{ID: "p1", Title: "palui", WorkspaceRoot: "/Users/z/Code/palui"},
		{ID: "p2", Title: "vorneo", WorkspaceRoot: "/Users/z/Code/vorneo"},
		{ID: "p4", Title: "palui-site", WorkspaceRoot: "/Users/z/Code/palui-site/"},
		{ID: "p6", Title: "Kopi Banjar", WorkspaceRoot: "/Users/z/Code/kopi"},
	}
	for _, c := range h.T3Match {
		p, why := t3code.MatchProject(projects, c.Q)
		var got any = map[string]any{"error": why}
		if p != nil {
			got = map[string]any{"project": map[string]any{"id": p.ID, "title": p.Title, "workspaceRoot": p.WorkspaceRoot}}
		}
		if w, g := jsonx.Canonical(conntest.Of(json.RawMessage(c.Out)).V), jsonx.Canonical(conntest.Of(got).V); w != g {
			t.Errorf("match %q: want %s got %s", c.Q, w, g)
		}
	}
	for _, c := range h.T3Fingerprints {
		if got := t3code.CodeFingerprint(c.In); got != c.Out {
			t.Errorf("fingerprint %q: %s", c.In, got)
		}
	}
}

func TestChatHelpersMatch(t *testing.T) {
	h := loadHelpers(t)
	for _, c := range h.ChatTimes {
		got, ok := chatsconn.ParseTime(c.In, WITA)
		if (c.Out == nil) == ok || (ok && got != *c.Out) {
			t.Errorf("parseTime %q: %v %v want %v", c.In, got, ok, c.Out)
		}
	}
	for _, c := range h.ChatClips {
		if got := chatsconn.Clip(c.In, c.Max); got != c.Out {
			t.Errorf("clip %q %d: %q want %q", c.In, c.Max, got, c.Out)
		}
	}
}

// Webfetch: TS reads pages with Mozilla Readability on linkedom; Go with
// readeck's port on x/net/html. The rule: titles must be equal, and texts
// must be equal byte for byte for the pages in exactPages (every saved page
// today). A page added later that differs only in whitespace at element seams
// can be left out of exactPages; it must still match word for word (wordsOf).
var exactPages = map[string]bool{"tiny.html": true, "blog.html": true, "news.html": true, "docs.html": true, "entities.html": true}

func wordsOf(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestWebfetchExtractionMatchesReadabilityOnSavedPages(t *testing.T) {
	h := loadHelpers(t)
	if len(h.WebfetchPages) < 5 {
		t.Fatal(len(h.WebfetchPages))
	}
	for name, want := range h.WebfetchPages {
		raw, err := os.ReadFile(filepath.Join("../../../testdata/webfetch", name))
		testutil.Must(t, err)
		title, text := webfetch.ExtractReadable(string(raw))
		if title != want.Title {
			t.Errorf("%s: title %q want %q", name, title, want.Title)
		}
		if exactPages[name] && text != want.Text {
			t.Errorf("%s: text\n want %q\n got  %q", name, want.Text, text)
			continue
		}
		if wordsOf(text) != wordsOf(want.Text) {
			t.Errorf("%s: words\n want %q\n got  %q\n %s", name, wordsOf(want.Text), wordsOf(text), testutil.DiffAt(wordsOf(want.Text), wordsOf(text)))
		}
	}
}

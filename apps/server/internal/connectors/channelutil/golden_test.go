package channelutil_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/channelutil"
	"github.com/zakiego/palui/apps/server/internal/db"
)

type golden struct {
	Split []struct {
		Text   string   `json:"text"`
		Limit  int      `json:"limit"`
		Chunks []string `json:"chunks"`
	} `json:"split"`
	Telegram []struct {
		MD  string `json:"md"`
		Out string `json:"out"`
	} `json:"telegram"`
	WhatsApp []struct {
		MD  string `json:"md"`
		Out string `json:"out"`
	} `json:"whatsapp"`
	Actions []struct {
		Actions []db.OutboxAction `json:"actions"`
		Words   []string          `json:"words"`
		Hint    string            `json:"hint"`
		Matches []struct {
			Text string  `json:"text"`
			ID   *string `json:"id"`
		} `json:"matches"`
	} `json:"actions"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/channels.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGoldenSplit(t *testing.T) {
	g := loadGolden(t)
	if len(g.Split) < 100 {
		t.Fatalf("only %d split cases", len(g.Split))
	}
	for i, c := range g.Split {
		got := channelutil.SplitMessage(c.Text, c.Limit)
		if len(got) != len(c.Chunks) {
			t.Errorf("case %d (limit %d): %d chunks, want %d\n got  %q\n want %q", i, c.Limit, len(got), len(c.Chunks), got, c.Chunks)
			continue
		}
		for j := range got {
			if got[j] != c.Chunks[j] {
				t.Errorf("case %d (limit %d) chunk %d:\n got  %q\n want %q", i, c.Limit, j, got[j], c.Chunks[j])
				break
			}
		}
	}
}

func TestGoldenMarkdown(t *testing.T) {
	g := loadGolden(t)
	if len(g.Telegram) < 400 || len(g.WhatsApp) != len(g.Telegram) {
		t.Fatalf("markdown cases %d / %d", len(g.Telegram), len(g.WhatsApp))
	}
	for i, c := range g.Telegram {
		if got := channelutil.MarkdownToTelegramHTML(c.MD); got != c.Out {
			t.Errorf("telegram %d %q:\n got  %q\n want %q", i, c.MD, got, c.Out)
		}
	}
	for i, c := range g.WhatsApp {
		if got := channelutil.MarkdownToWhatsApp(c.MD); got != c.Out {
			t.Errorf("whatsapp %d %q:\n got  %q\n want %q", i, c.MD, got, c.Out)
		}
	}
}

func TestGoldenActions(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.Actions {
		for j, a := range c.Actions {
			if got := channelutil.ActionReplyWord(a); got != c.Words[j] {
				t.Errorf("set %d word %d: %q, want %q", i, j, got, c.Words[j])
			}
		}
		if got := channelutil.ActionsHint(c.Actions); got != c.Hint {
			t.Errorf("set %d hint %q, want %q", i, got, c.Hint)
		}
		for _, m := range c.Matches {
			a, ok := channelutil.MatchActionReply(m.Text, c.Actions)
			switch {
			case m.ID == nil && ok:
				t.Errorf("set %d %q matched %s", i, m.Text, a.ID)
			case m.ID != nil && (!ok || a.ID != *m.ID):
				t.Errorf("set %d %q: got %q %v, want %q", i, m.Text, a.ID, ok, *m.ID)
			}
		}
	}
}

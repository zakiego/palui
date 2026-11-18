package notes_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chatrules"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	notesconn "github.com/zakiego/palui/apps/server/internal/connectors/notes"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestChatRuleTools(t *testing.T) {
	d := testutil.DB(t)
	clock := func() int64 { return 1000 }
	if notesconn.ChatRuleTools(d, clock, connectors.TurnInfo{ChatID: "c", AgentID: "a"}) != nil {
		t.Fatal("no rule setter, no tools")
	}
	id := "mbr_ani"
	tainted := false
	turn := connectors.TurnInfo{ChatID: "c", AgentID: "a", RunID: "run_1", RuleSetter: &connectors.RuleSetter{MemberID: &id, Name: "Ani"}, Tainted: func() bool { return tainted }}
	set := notesconn.ChatRuleTools(d, clock, turn)
	save, ok := set.Get("save_chat_rule")
	if !ok || save.Class != "write" || save.Data != connectors.DataChat {
		t.Fatal(save)
	}
	in := json.RawMessage(`{"title":"Tanggal","body":"Jangan bahas tanggal."}`)

	tainted = true
	out, err := save.Tool.Execute(context.Background(), in, connectors.ToolCallInfo{})
	testutil.Must(t, err)
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), "konten dari luar") {
		t.Fatal(string(b))
	}
	if l, _ := chatrules.List(d, "a", "c"); len(l) != 0 {
		t.Fatal("a tainted turn must not save", l)
	}

	tainted = false
	_, err = save.Tool.Execute(context.Background(), in, connectors.ToolCallInfo{})
	testutil.Must(t, err)
	l, _ := chatrules.List(d, "a", "c")
	if len(l) != 1 || l[0].AuthorName != "Ani" {
		t.Fatal(l)
	}
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM events WHERE kind = 'notes.chat_rule'").Scan(&n))
	if n != 1 {
		t.Fatal(n)
	}
}

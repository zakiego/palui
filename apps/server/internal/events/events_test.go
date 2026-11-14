package events_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// events.ts has no test file of its own (log.test.ts covers redaction).

func TestLogListPruneAndListeners(t *testing.T) {
	d := testutil.DB(t)
	var seen []string
	off := events.OnEvent(func(e db.Event) { seen = append(seen, e.Kind) })
	events.OnEvent(func(db.Event) { panic("a listener must never break the writer") })
	id := "telegram"
	_, err := events.Log(d, events.Input{Kind: "a", Message: "one", ConnectorID: &id}, 1)
	testutil.Must(t, err)
	off()
	e, err := events.Log(d, events.Input{Kind: "b", Message: "two", Level: "error", Data: map[string]any{"n": 1}}, 2)
	testutil.Must(t, err)
	if e.Level != "error" || e.Data == nil {
		t.Fatalf("%+v", e)
	}
	if len(seen) != 1 || seen[0] != "a" {
		t.Fatal(seen)
	}
	all, _ := events.List(d, events.ListOptions{})
	if len(all) != 2 || all[0].Kind != "b" {
		t.Fatalf("%+v", all)
	}
	if l, _ := events.List(d, events.ListOptions{ConnectorID: "telegram"}); len(l) != 1 {
		t.Fatal(l)
	}
	if l, _ := events.List(d, events.ListOptions{Level: "error"}); len(l) != 1 || l[0].Kind != "b" {
		t.Fatal(l)
	}
	if l, _ := events.List(d, events.ListOptions{AfterID: &all[1].ID}); len(l) != 1 {
		t.Fatal(l)
	}
	n, err := events.Prune(d, 10, 12)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

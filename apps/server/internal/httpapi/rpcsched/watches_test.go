package rpcsched_test

import (
	"encoding/json"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/watches"
)

func TestWatchesPageListsAndCancels(t *testing.T) {
	e := apitest.New(t, nil)
	now := e.Now.Load()
	_, err := e.DB.Exec("INSERT INTO chats (id, channel, external_id, title, allowed, created_at) VALUES ('cht_w', 'web', 'w1', 'Kerja', 1, ?)", now)
	testutil.Must(t, err)
	running, err := watches.Create(e.DB, watches.CreateInput{ChatID: "cht_w", Topic: "Laba Danantara", Question: "q", DoneWhen: "d", ExpiresAt: now + 3600_000}, now)
	testutil.Must(t, err)
	stopped, err := watches.Create(e.DB, watches.CreateInput{ChatID: "cht_w", Topic: "Old", Question: "q", DoneWhen: "d", ExpiresAt: now + 3600_000}, now)
	testutil.Must(t, err)
	_, err = watches.Cancel(e.DB, stopped.ID, now)
	testutil.Must(t, err)

	token := e.SignIn()
	status, body := e.Get(token, "getWatches", "")
	if status != 200 {
		t.Fatalf("getWatches: %d %s", status, body)
	}
	var page struct {
		Active []struct {
			ID    string `json:"id"`
			Topic string `json:"topic"`
			Chat  *struct {
				Title string `json:"title"`
			} `json:"chat"`
		} `json:"active"`
		Ended []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"ended"`
		Totals struct{ Active, Ended int } `json:"totals"`
	}
	testutil.Must(t, json.Unmarshal([]byte(body), &page))
	if len(page.Active) != 1 || page.Active[0].ID != running.ID || page.Active[0].Chat == nil || page.Active[0].Chat.Title != "Kerja" {
		t.Fatalf("active: %s", body)
	}
	if len(page.Ended) != 1 || page.Ended[0].Status != "cancelled" || page.Totals.Active != 1 || page.Totals.Ended != 1 {
		t.Fatalf("ended: %s", body)
	}

	if status, body := e.Post(token, "cancelWatchFn", `{"id":"`+running.ID+`"}`); status != 200 || body != `{"status":"cancelled"}` {
		t.Fatalf("cancel: %d %s", status, body)
	}
	// Cancelling again is fine and reports where it stands.
	if status, body := e.Post(token, "cancelWatchFn", `{"id":"`+running.ID+`"}`); status != 200 || body != `{"status":"cancelled"}` {
		t.Fatalf("cancel twice: %d %s", status, body)
	}
	if status, body := e.Post(token, "cancelWatchFn", `{"id":"wat_missing"}`); status == 200 {
		t.Fatalf("cancel missing: %d %s", status, body)
	}
}

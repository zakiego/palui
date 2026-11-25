package rpcsched

import (
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/watches"
)

// The Watches page (PLAN 6e): every chat's watches, running ones first, and a
// way to stop one. Watches are made from chat only, so there is no create.
// Cancel needs no poke: the engine skips a cancelled row on its next sweep.

const watchGoneText = "That watch doesn't exist anymore."

func init() {
	httpapi.Register(httpapi.Handler{Name: "getWatches", Method: http.MethodGet, Guard: httpapi.User, Fn: getWatches})
	httpapi.Register(httpapi.Handler{
		Name: "cancelWatchFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: idInput(),
		Fn: func(c *httpapi.Call) (any, error) {
			q, id := c.DB(), inputID(c)
			if _, ok, err := watches.Get(q, id); err != nil {
				return nil, err
			} else if !ok {
				return nil, httpapi.Fail(watchGoneText)
			}
			// An already ended watch comes back as it is, so the page can say it ended on its own.
			w, err := watches.Cancel(q, id, c.Now())
			if err != nil {
				return nil, friendly(err)
			}
			return obj("status", w.Status), nil
		},
	})
}

type watchView struct {
	ID          string        `json:"id"`
	Topic       string        `json:"topic"`
	Question    string        `json:"question"`
	DoneWhen    string        `json:"doneWhen"`
	EveryMin    int64         `json:"everyMin"`
	ExpiresAt   int64         `json:"expiresAt"`
	StartsAt    *int64        `json:"startsAt"`
	Status      string        `json:"status"`
	NextCheckAt *int64        `json:"nextCheckAt"`
	Checks      int64         `json:"checks"`
	Fails       int64         `json:"fails"`
	LastCheckAt *int64        `json:"lastCheckAt"`
	LastRunID   *string       `json:"lastRunId"`
	LastError   *string       `json:"lastError"`
	LastSummary string        `json:"lastSummary"`
	Result      *string       `json:"result"`
	TokensUsed  int64         `json:"tokensUsed"`
	TokenCap    int64         `json:"tokenCap"`
	EndedAt     *int64        `json:"endedAt"`
	CreatedAt   int64         `json:"createdAt"`
	CreatedBy   string        `json:"createdBy"`
	Chat        *reminderChat `json:"chat"`
}

type watchTotals struct {
	Active int64 `json:"active"`
	Ended  int64 `json:"ended"`
}

const (
	watchRunning = "status IN ('active', 'checking')"
	watchEnded   = "status NOT IN ('active', 'checking')"
)

func getWatches(c *httpapi.Call) (any, error) {
	q := c.DB()
	titles, err := chatRefs(q)
	if err != nil {
		return nil, err
	}
	list := func(where, order string, limit int) ([]watchView, error) {
		rows, err := q.Query("SELECT "+db.WatchCols+" FROM watches WHERE "+where+" ORDER BY "+order+" LIMIT ?", limit)
		all, err := db.All(rows, err, db.ScanWatch)
		if err != nil {
			return nil, err
		}
		out := []watchView{}
		for _, w := range all {
			out = append(out, watchView{
				ID: w.ID, Topic: w.Topic, Question: w.Question, DoneWhen: w.DoneWhen, EveryMin: w.EveryMin, ExpiresAt: w.ExpiresAt, StartsAt: w.StartsAt,
				Status: w.Status, NextCheckAt: w.NextCheckAt, Checks: w.Checks, Fails: w.Fails, LastCheckAt: w.LastCheckAt,
				LastRunID: w.LastRunID, LastError: w.LastError, LastSummary: w.LastSummary, Result: w.Result,
				TokensUsed: w.TokensUsed, TokenCap: w.TokenCap, EndedAt: w.EndedAt, CreatedAt: w.CreatedAt, CreatedBy: w.CreatedBy,
				Chat: titles[w.ChatID],
			})
		}
		return out, nil
	}
	active, err := list(watchRunning, "next_check_at ASC, id", 200)
	if err != nil {
		return nil, err
	}
	ended, err := list(watchEnded, "coalesce(ended_at, updated_at) DESC, id", 100)
	if err != nil {
		return nil, err
	}
	// The lists above are capped; these are how many rows there really are.
	var totals watchTotals
	if err := q.QueryRow("SELECT count(*) FROM watches WHERE " + watchRunning).Scan(&totals.Active); err != nil {
		return nil, err
	}
	if err := q.QueryRow("SELECT count(*) FROM watches WHERE " + watchEnded).Scan(&totals.Ended); err != nil {
		return nil, err
	}
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	return obj("active", active, "ended", ended, "totals", totals, "timezone", tz), nil
}

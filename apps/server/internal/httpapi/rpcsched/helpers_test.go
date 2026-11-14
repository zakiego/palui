package rpcsched_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deps"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcsched"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const testdata = "../../../testdata"

// seedCopy opens a copy of testdata/seeded.db.
func seedCopy(t testing.TB) *db.DB {
	t.Helper()
	src, err := os.Open(filepath.Join(testdata, "seeded.db"))
	testutil.Must(t, err)
	defer src.Close()
	path := filepath.Join(t.TempDir(), "palui.db")
	dst, err := os.Create(path)
	testutil.Must(t, err)
	_, err = io.Copy(dst, src)
	testutil.Must(t, err)
	testutil.Must(t, dst.Close())
	d, err := db.Open(path, db.OpenOptions{})
	testutil.Must(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

// fakeTurn is one scripted runTurn answer.
type fakeTurn struct {
	RunID  string `json:"runId"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

// fakes is the golden's fixed runtime answers.
type fakes struct {
	Turns map[string]fakeTurn `json:"turns"`
	Parse []struct {
		Text   string                   `json:"text"`
		Answer reminders.ParsedReminder `json:"answer"`
	} `json:"parse"`
}

// runtimeEnv is a test server whose runtime is the real engine and job runner
// on d, with runTurn and the reminder parser scripted from f. withModel puts a
// fast model on the "openrouter" connector the seed's fast chain names;
// without it the parse is refused like a runtime with no model.
func runtimeEnv(t testing.TB, d *db.DB, f fakes, withModel bool) *apitest.Env {
	t.Helper()
	parse := func(ctx context.Context, text, tz string, now int64, m models.LanguageModel) (reminders.ParsedReminder, error) {
		for _, p := range f.Parse {
			if p.Text == text {
				a := p.Answer
				a.TZ = tz
				return a, nil
			}
		}
		return reminders.ParsedReminder{}, &httpapi.Error{Status: 500, Message: "no scripted parse for " + text}
	}
	o := chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{}, Deps: chat.Deps{ParseReminder: parse}}
	if withModel {
		o.Registry["openrouter"] = conntest.ModelRuntime("openrouter", func() models.LanguageModel { return models.NewScripted() }, nil)
		o.Rows = []chattest.Row{{"openrouter", "openrouter"}}
	}
	h := chattest.New(t, o)
	var env *apitest.Env
	runner := jobs.NewRunner(jobs.RunnerOptions{DB: d, Log: logx.Silent, Clock: func() int64 { return env.Now.Load() }, RunTurn: func(ctx context.Context, r jobs.TurnRequest) (jobs.TurnResponse, error) {
		turn, ok := f.Turns[r.JobID]
		if !ok {
			return jobs.TurnResponse{}, &httpapi.Error{Message: "no scripted turn for " + r.JobID}
		}
		return jobs.TurnResponse{RunID: turn.RunID, Text: turn.Text, Status: turn.Status}, nil
	}})
	env = apitest.New(t, func(o *httpapi.Options) {
		o.DB = d
		o.Runtime = &httpapi.Runtime{DB: d, Engine: h.Engine, Sup: h.Sup, Built: &deps.Built{Runner: runner}, Bus: h.Bus, Log: logx.Silent, Clock: func() int64 { return env.Now.Load() }}
	})
	return env
}

var goldenID = regexp.MustCompile(`\b([a-z]{2,6})_[0-9a-z]{19}\b`)

// seededIDs is every newId() id in a fresh copy of seeded.db.
func seededIDs(t testing.TB, q db.Q) map[string]bool {
	t.Helper()
	tables, err := q.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	testutil.Must(t, err)
	var names []string
	for tables.Next() {
		var n string
		testutil.Must(t, tables.Scan(&n))
		names = append(names, n)
	}
	tables.Close()
	ids := map[string]bool{}
	for _, n := range names {
		for _, row := range queryRows(t, q, "SELECT * FROM \""+n+"\"") {
			for _, m := range goldenID.FindAllString(jsonx.Stringify(row), -1) {
				ids[m] = true
			}
		}
	}
	return ids
}

// newIDs replaces every id not in the seed with <prefix>#N in order of first
// appearance: ids made during the run are random.
func newIDs(s string, seeded map[string]bool) string {
	seen := map[string]string{}
	return goldenID.ReplaceAllStringFunc(s, func(m string) string {
		if seeded[m] {
			return m
		}
		if r, ok := seen[m]; ok {
			return r
		}
		r := goldenID.FindStringSubmatch(m)[1] + "#" + strconv.Itoa(len(seen)+1)
		seen[m] = r
		return r
	})
}

// queryRows runs a SELECT and returns ordered column -> value objects,
// integers as float64 and blobs as text, like better-sqlite3's rows in JSON.
func queryRows(t testing.TB, q db.Q, sql string) []any {
	t.Helper()
	rows, err := q.Query(sql)
	testutil.Must(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	testutil.Must(t, err)
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		testutil.Must(t, rows.Scan(ptrs...))
		o := jsonx.NewMap[any]()
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if f, ok := v.(int64); ok {
				v = float64(f)
			}
			o.Set(c, v)
		}
		out = append(out, o)
	}
	testutil.Must(t, rows.Err())
	return out
}

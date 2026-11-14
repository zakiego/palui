package rpcsched_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// testdata/rpc-sched.json (testdata/gen/rpc-sched.ts) replayed through the
// real HTTP handlers on a copy of seeded.db with the same clock: each response
// body must be the TS handler's JSON text exactly (ids made during the run
// normalized to <prefix>#N), each thrown message the same "error" text (a
// validator's under code VALIDATION), and the rows after a write the same
// (compared by content: sorted keys, stored JSON parsed, every id numbered).

type goldenRows struct {
	SQL  string          `json:"sql"`
	Rows json.RawMessage `json:"rows"`
}

type goldenCase struct {
	Fn         string       `json:"fn"`
	Method     string       `json:"method"`
	Now        int64        `json:"now"`
	Input      *string      `json:"input"`
	Body       *string      `json:"body"`
	Error      *string      `json:"error"`
	Validation bool         `json:"validation"`
	Rows       []goldenRows `json:"rows"`
}

// setupRow is a row the generator added through the TS services before the
// calls; the Go test inserts it as it is.
type setupRow struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

func insertSetup(t *testing.T, d *db.DB, rows []setupRow) {
	t.Helper()
	for _, r := range rows {
		v, err := jsonx.Parse(r.Row)
		testutil.Must(t, err)
		o := v.(*jsonx.Object)
		var cols, marks []string
		var args []any
		for _, k := range o.Keys() {
			x, _ := o.Get(k)
			if f, ok := x.(float64); ok && f == float64(int64(f)) {
				x = int64(f)
			}
			cols, marks, args = append(cols, `"`+k+`"`), append(marks, "?"), append(args, x)
		}
		_, err = d.Exec("INSERT INTO "+r.Table+" ("+strings.Join(cols, ", ")+") VALUES ("+strings.Join(marks, ", ")+")", args...)
		testutil.Must(t, err)
	}
}

type goldenFile struct {
	Base      int64    `json:"base"`
	Fake      fakes    `json:"fake"`
	NonJSON   []string `json:"nonJSON"`
	Sequences []struct {
		Name    string       `json:"name"`
		Runtime string       `json:"runtime"`
		Setup   []setupRow   `json:"setup"`
		Cases   []goldenCase `json:"cases"`
	} `json:"sequences"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, "rpc-sched.json"))
	testutil.Must(t, err)
	var g goldenFile
	testutil.Must(t, json.Unmarshal(b, &g))
	return g
}

func normRows(t *testing.T, v any) string {
	t.Helper()
	return testutil.NormalizeGolden(testutil.ParseJSONColumns(v, "value", "payload", "data"))
}

func TestGolden(t *testing.T) {
	g := loadGolden(t)
	seeded := seededIDs(t, seedCopy(t))
	calls := 0
	for _, seq := range g.Sequences {
		t.Run(seq.Name, func(t *testing.T) {
			d := seedCopy(t)
			insertSetup(t, d, seq.Setup)
			var e *apitest.Env
			switch seq.Runtime {
			case "down":
				e = apitest.New(t, func(o *httpapi.Options) { o.DB = d })
			case "fake":
				e = runtimeEnv(t, d, g.Fake, true)
			case "nomodel":
				e = runtimeEnv(t, d, g.Fake, false)
			default:
				t.Fatalf("runtime %q", seq.Runtime)
			}
			token := e.SignIn()
			for i, c := range seq.Cases {
				calls++
				e.Now.Store(c.Now)
				input := ""
				if c.Input != nil {
					input = *c.Input
				}
				var status int
				var body string
				if c.Method == http.MethodGet {
					status, body = e.Get(token, c.Fn, input)
				} else {
					status, body = e.Post(token, c.Fn, input)
				}
				where := func() string { return seq.Name + "#" + jsonx.Stringify(float64(i)) + " " + c.Fn + " " + input }
				if c.Body != nil {
					if status != 200 {
						t.Errorf("%s: status %d, body %s", where(), status, body)
						continue
					}
					want, got := newIDs(*c.Body, seeded), newIDs(body, seeded)
					if got != want {
						t.Errorf("%s: body differs %s\n got: %s\nwant: %s", where(), testutil.DiffAt(got, want), got, want)
					}
				} else {
					var res struct {
						Error string `json:"error"`
						Code  string `json:"code"`
					}
					if err := json.Unmarshal([]byte(body), &res); err != nil || status == 200 {
						t.Errorf("%s: status %d, body %s", where(), status, body)
						continue
					}
					if res.Error != *c.Error {
						t.Errorf("%s: error %q, want %q", where(), res.Error, *c.Error)
					}
					if (res.Code == "VALIDATION") != c.Validation {
						t.Errorf("%s: code %s, validation %v", where(), res.Code, c.Validation)
					}
				}
				for _, r := range c.Rows {
					want, err := jsonx.Parse(r.Rows)
					testutil.Must(t, err)
					got, exp := normRows(t, queryRows(t, d, r.SQL)), normRows(t, want)
					if got != exp {
						t.Errorf("%s: rows of %s differ %s\n got: %s\nwant: %s", where(), r.SQL, testutil.DiffAt(got, exp), got, exp)
					}
				}
			}
		})
	}
	if calls < 110 {
		t.Fatalf("only %d calls in the golden", calls)
	}
}

// Every value the TS functions return is plain JSON except runJobNow's
// undefined runId/detail, which JSON leaves out (Go: omitempty).
func TestGoldenNonJSONValues(t *testing.T) {
	g := loadGolden(t)
	want := []string{"runJobNow: undefined at result.detail"}
	if jsonx.Stringify(anySlice(g.NonJSON)) != jsonx.Stringify(anySlice(want)) {
		t.Fatalf("non-JSON values changed: %v", g.NonJSON)
	}
}

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}

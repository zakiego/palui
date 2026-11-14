package rpcpeople_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// testdata/rpc-people.json (from testdata/gen/rpc-people.ts): every case
// replayed through the Go HTTP handlers on its own copy of seeded.db, with
// the same fake runtime answers. Results compare as exact text (key order
// included), errors by their message, runtime calls by their arguments, and
// the rows the case lists after its last step. Normalized on both sides:
// newId() ids (<prefix>#N in order of first appearance) and share tokens and
// their hashes.

type goldenStep struct {
	SQL        string          `json:"sql"`
	Fn         string          `json:"fn"`
	Input      *string         `json:"input"`
	RT         json.RawMessage `json:"rt"`
	Result     *string         `json:"result"`
	Error      *string         `json:"error"`
	Validation bool            `json:"validation"`
	Calls      json.RawMessage `json:"calls"`
}

type goldenCase struct {
	Name  string       `json:"name"`
	Steps []goldenStep `json:"steps"`
	Rows  []struct {
		SQL  string `json:"sql"`
		Rows string `json:"rows"`
	} `json:"rows"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, "rpc-people.json"))
	testutil.Must(t, err)
	var g struct {
		Now    int64        `json:"now"`
		AppURL string       `json:"appUrl"`
		Cases  []goldenCase `json:"cases"`
	}
	testutil.Must(t, json.Unmarshal(b, &g))
	if g.Now != seededNow || g.AppURL != "http://localhost:4890" {
		t.Fatalf("golden clock %d or app URL %s doesn't match the test", g.Now, g.AppURL)
	}
	return g.Cases
}

func TestGoldenRPCPeople(t *testing.T) {
	cases := loadGolden(t)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	calls := 0
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			e, token := seededEnv(t)
			want, got := newNormalizer(), newNormalizer()
			for _, s := range c.Steps {
				if s.SQL != "" {
					_, err := e.DB.Exec(s.SQL)
					testutil.Must(t, err)
					continue
				}
				calls++
				var spec *jsonx.Object
				if len(s.RT) > 0 {
					o, err := jsonx.ParseObject(s.RT)
					testutil.Must(t, err)
					spec = o
				}
				fake := useFake(t, spec)
				input := ""
				if s.Input != nil {
					input = *s.Input
				}
				status, body := call(t, e, token, s.Fn, input)
				label := func() string { return s.Fn + " " + input }
				switch {
				case s.Result != nil:
					w, g := want.text(*s.Result), got.text(body)
					if status != 200 || w != g {
						t.Errorf("%s: status %d\n%s", label(), status, testutil.DiffAt(g, w))
					}
				case s.Error != nil:
					var res struct {
						Error string `json:"error"`
						Code  string `json:"code"`
					}
					if err := json.Unmarshal([]byte(body), &res); err != nil || status == 200 {
						t.Errorf("%s: want error %q, got %d %s", label(), *s.Error, status, body)
						break
					}
					w, g := want.text(*s.Error), got.text(res.Error)
					if w != g {
						t.Errorf("%s: error\n%s", label(), testutil.DiffAt(g, w))
					}
					if s.Validation && (res.Code != "VALIDATION" || status != 400) {
						t.Errorf("%s: want a 400 VALIDATION, got %d %s", label(), status, res.Code)
					}
					if !s.Validation && res.Code == "VALIDATION" {
						t.Errorf("%s: the handler's error came back as a validation error", label())
					}
				}
				wantCalls, err := jsonx.Parse(s.Calls)
				testutil.Must(t, err)
				if w, g := want.text(js(wantCalls)), got.text(js(fake.calls)); w != g {
					t.Errorf("%s: runtime calls\nwant %s\n got %s", label(), w, g)
				}
			}
			for _, r := range c.Rows {
				w, g := want.text(r.Rows), got.text(js(queryRows(t, e.DB, r.SQL)))
				if w != g {
					t.Errorf("rows %s\n%s", r.SQL, testutil.DiffAt(g, w))
				}
			}
		})
	}
	if calls < 150 {
		t.Fatalf("only %d calls in the golden", calls)
	}
}

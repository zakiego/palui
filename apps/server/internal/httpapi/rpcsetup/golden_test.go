package rpcsetup_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// testdata/rpc-setup.json (testdata/gen/rpc-setup.ts): every call replayed
// through the HTTP handler on a copy of seeded.db, the body compared as exact
// text and the rows after writes by content. Normalized on both sides: ids
// made during the run (not in seeded.db) become <prefix>#N per text, the
// viewer's session and user ids, a new hook's slug and secret, and the OAuth
// state and nonce.

type goldenStep struct {
	Fn      string            `json:"fn"`
	Method  string            `json:"method"`
	Input   *string           `json:"input"`
	Result  *string           `json:"result"`
	Error   *string           `json:"error"`
	Issues  *string           `json:"issues"`
	Rows    []goldenRows      `json:"rows"`
	Secrets map[string]string `json:"secrets"`
}

type goldenRows struct {
	SQL  string `json:"sql"`
	Rows string `json:"rows"`
}

type goldenFile struct {
	Now       int64           `json:"now"`
	AppURL    string          `json:"appUrl"`
	MasterKey string          `json:"masterKey"`
	Fake      []FakeConnector `json:"fake"`
	Passkeys  []struct {
		ID         string  `json:"id"`
		Name       *string `json:"name"`
		DeviceType string  `json:"deviceType"`
		BackedUp   int     `json:"backedUp"`
		CreatedAt  int64   `json:"createdAt"`
	} `json:"passkeys"`
	Backups []struct {
		File  string  `json:"file"`
		Bytes int     `json:"bytes"`
		Mtime float64 `json:"mtime"`
	} `json:"backups"`
	Scenarios []struct {
		Name    string       `json:"name"`
		Runtime string       `json:"runtime"`
		Steps   []goldenStep `json:"steps"`
	} `json:"scenarios"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "rpc-setup.json"))
	testutil.Must(t, err)
	var g goldenFile
	testutil.Must(t, json.Unmarshal(b, &g))
	return g
}

var (
	idRe    = regexp.MustCompile(`\b([a-z]{2,6})_[0-9a-z]{19}\b`)
	stateRe = regexp.MustCompile(`state=[^&"\\]*`)
	nonceRe = regexp.MustCompile(`"oauthNonce":"[^"]*"`)
	// The nonce inside a JSON column that is itself a JSON string.
	nonceEscRe = regexp.MustCompile(`\\"oauthNonce\\":\\"[^"\\]*\\"`)
)

// seedIDs are the ids in seeded.db, which are kept as they are.
func seedIDs(t *testing.T) map[string]bool {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "seeded.db"))
	testutil.Must(t, err)
	out := map[string]bool{}
	for _, m := range idRe.FindAllString(string(b), -1) {
		out[m] = true
	}
	return out
}

// normalizer returns the text normalizer; volatile pairs (a new hook's slug
// and secret) are replaced first, in every later text of the scenario.
func normalizer(seed map[string]bool, volatile *[][2]string) func(string) string {
	return func(s string) string {
		for _, p := range *volatile {
			s = strings.ReplaceAll(s, p[0], p[1])
		}
		seen := map[string]string{}
		s = idRe.ReplaceAllStringFunc(s, func(m string) string {
			if seed[m] {
				return m
			}
			if r, ok := seen[m]; ok {
				return r
			}
			r := fmt.Sprintf("%s#%d", idRe.FindStringSubmatch(m)[1], len(seen)+1)
			seen[m] = r
			return r
		})
		s = stateRe.ReplaceAllString(s, "state=<state>")
		s = nonceRe.ReplaceAllString(s, `"oauthNonce":"<nonce>"`)
		return nonceEscRe.ReplaceAllString(s, `\"oauthNonce\":\"<nonce>\"`)
	}
}

// queryJSON runs one SELECT and returns its rows as JSON objects.
func queryJSON(t *testing.T, q db.Q, sql string) string {
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
		row := jsonx.NewMap[any]()
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row.Set(c, v)
		}
		out = append(out, row)
	}
	testutil.Must(t, rows.Err())
	return jsonx.Stringify(out)
}

func canonical(t *testing.T, s string) string {
	t.Helper()
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		t.Fatalf("not JSON: %v: %s", err, s)
	}
	return jsonx.Canonical(v)
}

type rpcErr struct {
	Error  string          `json:"error"`
	Code   string          `json:"code"`
	Issues json.RawMessage `json:"issues"`
}

func TestGoldenRPCSetup(t *testing.T) {
	g := loadGolden(t)
	seed := seedIDs(t)
	calls := 0
	for _, sc := range g.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			dir := t.TempDir()
			d := seededCopy(t, dir)
			for _, b := range g.Backups {
				writeBackup(t, dir, b.File, b.Bytes, b.Mtime)
			}
			var rt = fakeRuntime(t, g.Fake)
			if sc.Runtime == "down" {
				rt = nil
			}
			e, token := env(t, d, dir, rt, g.Now, g.MasterKey)
			var volatile [][2]string
			norm := normalizer(seed, &volatile)
			var userID, sessionID string
			testutil.Must(t, d.QueryRow("SELECT id, user_id FROM session").Scan(&sessionID, &userID))
			for _, p := range g.Passkeys {
				_, err := d.Exec("INSERT INTO passkey (id, name, public_key, user_id, credential_id, counter, device_type, backed_up, transports, created_at, aaguid) VALUES (?, ?, 'pk', ?, ?, 0, ?, ?, '', ?, NULL)", p.ID, p.Name, userID, "cred-"+p.ID, p.DeviceType, p.BackedUp, p.CreatedAt)
				testutil.Must(t, err)
			}
			for i, st := range sc.Steps {
				calls++
				input := ""
				if st.Input != nil {
					input = *st.Input
				}
				var status int
				var body string
				if st.Method == http.MethodGet {
					status, body = e.Get(token, st.Fn, input)
				} else {
					status, body = e.Post(token, st.Fn, input)
				}
				where := fmt.Sprintf("step %d %s(%s)", i, st.Fn, input)
				body = strings.ReplaceAll(strings.ReplaceAll(body, `"sessionId":"`+sessionID+`"`, `"sessionId":"<session>"`), `"id":"`+userID+`"`, `"id":"<user>"`)
				if st.Fn == "createHookFn" && status == 200 {
					var r struct{ Slug, Secret string }
					testutil.Must(t, json.Unmarshal([]byte(body), &r))
					volatile = append(volatile, [2]string{r.Slug, "<slug>"}, [2]string{r.Secret, "<secret>"})
				}
				body = norm(body)
				switch {
				case st.Result != nil:
					if status != 200 {
						t.Errorf("%s: status %d: %s", where, status, body)
					} else if body != *st.Result {
						t.Errorf("%s: %s", where, testutil.DiffAt(body, *st.Result))
					}
				default:
					var re rpcErr
					if status == 200 || json.Unmarshal([]byte(body), &re) != nil {
						t.Errorf("%s: want error %q, got %d %s", where, *st.Error, status, body)
						break
					}
					if re.Error != *st.Error {
						t.Errorf("%s: error %q, want %q", where, re.Error, *st.Error)
					}
					if st.Issues != nil {
						if re.Code != "VALIDATION" || canonical(t, string(re.Issues)) != canonical(t, *st.Issues) {
							t.Errorf("%s: issues %s, want %s", where, re.Issues, *st.Issues)
						}
					} else if re.Code == "VALIDATION" {
						t.Errorf("%s: refused by validation: %s", where, body)
					}
				}
				for _, r := range st.Rows {
					got := canonical(t, norm(queryJSON(t, d, r.SQL)))
					if want := canonical(t, r.Rows); got != want {
						t.Errorf("%s: rows of %s\n%s", where, r.SQL, testutil.DiffAt(got, want))
					}
				}
				for id, want := range st.Secrets {
					row, found, err := connectors.Get(d, id)
					testutil.Must(t, err)
					got := "null"
					if found {
						s, err := connectors.Secrets(row, g.MasterKey)
						testutil.Must(t, err)
						got = jsonx.Stringify(s)
					}
					if canonical(t, got) != canonical(t, want) {
						t.Errorf("%s: secrets of %s: %s, want %s", where, id, got, want)
					}
				}
			}
		})
	}
	if calls < 100 {
		t.Fatalf("only %d calls in the golden", calls)
	}
}

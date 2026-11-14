package rpcpeople_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/httpapi/rpcpeople"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// The fake control client of testdata/gen/rpc-people.ts: fixed answers per
// method from a spec, the same defaults as upRuntime in test-helpers.ts, and
// every call recorded with its arguments as the TS fake saw them.
type fakeRuntime struct {
	spec  *jsonx.Object
	calls []any
}

var defaultAnswers = map[string]string{
	"webSend":      `{"ok":true,"chatId":"chat_0mwfavogqfl8g9qpmbj","messageId":"msg_fixed"}`,
	"approve":      `{"ok":true}`,
	"stop":         `{"ok":true}`,
	"setChatAgent": `{"ok":true}`,
	"agentTools":   `{"tools":[]}`,
	"action":       `{}`,
}

// downErr is what Call.Runtime returns while the runtime is off.
var downErr = &httpapi.Error{Status: 503, Code: "RUNTIME_DOWN", Message: httpapi.RuntimeDown}

func (f *fakeRuntime) answer(name string, args ...any) (*jsonx.Object, error) {
	f.calls = append(f.calls, append([]any{name}, args...))
	var a any
	if f.spec != nil {
		a, _ = f.spec.Get(name)
	}
	if a == nil {
		parsed, err := jsonx.Parse([]byte(defaultAnswers[name]))
		if err != nil {
			panic(err)
		}
		a = parsed
	}
	o := a.(*jsonx.Object)
	if e, ok := o.Get("error"); ok {
		if eo, ok := e.(*jsonx.Object); ok {
			status, _ := eo.Get("status")
			msg, _ := eo.Get("message")
			return nil, &httpapi.Error{Status: int(status.(float64)), Message: msg.(string)}
		}
	}
	// A copy, so a handler can't change the spec.
	b, _ := jsonx.Marshal(o)
	return jsonx.ParseObject(b)
}

func (f *fakeRuntime) WebSend(text string) (httpapi.WebSent, error) {
	o, err := f.answer("webSend", text)
	if err != nil {
		return httpapi.WebSent{}, err
	}
	var out httpapi.WebSent
	b, _ := jsonx.Marshal(o)
	return out, json.Unmarshal(b, &out)
}

func (f *fakeRuntime) DecideApproval(id string, approved bool) bool {
	o, _ := f.answer("approve", id, approved)
	ok, _ := o.Get("ok")
	return ok == true
}

func (f *fakeRuntime) StopChat(chatID string) bool {
	o, _ := f.answer("stop", chatID)
	ok, _ := o.Get("ok")
	return ok == true
}

func (f *fakeRuntime) SwitchAgent(chatID, agentID string) error {
	var agent any
	if agentID != "" {
		agent = agentID
	}
	_, err := f.answer("setChatAgent", chatID, agent)
	return err
}

func (f *fakeRuntime) AgentTools(_ context.Context, agentID string) ([]tools.CatalogTool, error) {
	o, err := f.answer("agentTools", agentID)
	if err != nil {
		return nil, err
	}
	v, _ := o.Get("tools")
	b, _ := jsonx.Marshal(v)
	var out []tools.CatalogTool
	return out, json.Unmarshal(b, &out)
}

func (f *fakeRuntime) Action(_ context.Context, id, name string, input *jsonx.Object) (*jsonx.Object, error) {
	args := []any{id, name}
	if input != nil {
		args = append(args, input)
	}
	return f.answer("action", args...)
}

// useFake installs a fake runtime for the test (down: Call.Runtime's error).
func useFake(t *testing.T, spec *jsonx.Object) *fakeRuntime {
	f := &fakeRuntime{spec: spec, calls: []any{}}
	down := false
	if spec != nil {
		v, _ := spec.Get("down")
		down = v == true
	}
	restore := rpcpeople.SetRuntimeOf(func(*httpapi.Call) (rpcpeople.Runtime, error) {
		if down {
			return nil, downErr
		}
		return f, nil
	})
	t.Cleanup(restore)
	return f
}

const testdata = "../../../testdata"

var seededNow = func() int64 {
	var s struct {
		Now int64 `json:"now"`
	}
	b, err := os.ReadFile(filepath.Join(testdata, "seeded.json"))
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		panic(err)
	}
	return s.Now
}()

// seededEnv is a server on a scratch copy of testdata/seeded.db at the seed's
// clock, with the owner signed in.
func seededEnv(t *testing.T) (*apitest.Env, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "palui.db")
	src, err := os.Open(filepath.Join(testdata, "seeded.db"))
	testutil.Must(t, err)
	dst, err := os.Create(file)
	testutil.Must(t, err)
	_, err = io.Copy(dst, src)
	testutil.Must(t, err)
	src.Close()
	testutil.Must(t, dst.Close())
	d, err := db.Open(file, db.OpenOptions{})
	testutil.Must(t, err)
	t.Cleanup(func() { d.Close() })
	e := apitest.New(t, func(o *httpapi.Options) { o.DB = d })
	e.Now.Store(seededNow)
	return e, e.SignIn()
}

// memoryEnv is a server on a fresh migrated database.
func memoryEnv(t *testing.T) (*apitest.Env, string) {
	t.Helper()
	e := apitest.New(t, nil)
	return e, e.SignIn()
}

// call runs one function the way the dashboard does (GET or POST by its
// registration); input "" is undefined.
func call(t *testing.T, e *apitest.Env, token, name, input string) (int, string) {
	t.Helper()
	for _, h := range httpapi.Handlers() {
		if h.Name != name {
			continue
		}
		if h.Method == http.MethodGet {
			return e.Get(token, name, input)
		}
		return e.Post(token, name, input)
	}
	t.Fatalf("no handler %s", name)
	return 0, ""
}

// mustOK calls a function and decodes its 200 answer.
func mustOK(t *testing.T, e *apitest.Env, token, name, input string) *jsonx.Object {
	t.Helper()
	status, body := call(t, e, token, name, input)
	if status != 200 {
		t.Fatalf("%s %s: %d %s", name, input, status, body)
	}
	o, err := jsonx.ParseObject([]byte(body))
	testutil.Must(t, err)
	return o
}

// errorOf calls a function that must fail and returns its status and message.
func errorOf(t *testing.T, e *apitest.Env, token, name, input string) (int, string) {
	t.Helper()
	status, body := call(t, e, token, name, input)
	if status == 200 {
		t.Fatalf("%s %s: want an error, got %s", name, input, body)
	}
	var out struct {
		Error string `json:"error"`
	}
	testutil.Must(t, json.Unmarshal([]byte(body), &out))
	return status, out.Error
}

// at reads a dotted path off a decoded answer (array indices as numbers).
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			o, ok := v.(*jsonx.Object)
			if !ok {
				return nil
			}
			v, _ = o.Get(k)
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func js(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// queryRows runs a SELECT and returns its rows as ordered objects, integers
// as JSON numbers (better-sqlite3's .all()).
func queryRows(t *testing.T, q db.Q, query string) []any {
	t.Helper()
	rows, err := q.Query(query)
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
			if n, ok := v.(int64); ok {
				v = float64(n)
			}
			o.Set(c, v)
		}
		out = append(out, o)
	}
	testutil.Must(t, rows.Err())
	return out
}

var (
	shareLink = regexp.MustCompile(`/api/share/[A-Za-z0-9_-]{20,64}`)
	tokenHash = regexp.MustCompile(`"token_hash":"[0-9a-f]{64}"`)
)

// normalizer maps every newId() id to <prefix>#N in order of first
// appearance (kept across one case), and share tokens and their hashes to
// placeholders. Text order is untouched, so key order still counts.
type normalizer struct{ seen map[string]string }

func newNormalizer() *normalizer { return &normalizer{seen: map[string]string{}} }

func (n *normalizer) text(s string) string {
	s = shareLink.ReplaceAllString(s, "/api/share/<token>")
	s = tokenHash.ReplaceAllString(s, `"token_hash":"<hash>"`)
	return testutil.GoldenID.ReplaceAllStringFunc(s, func(m string) string {
		if r, ok := n.seen[m]; ok {
			return r
		}
		r := fmt.Sprintf("%s#%d", testutil.GoldenID.FindStringSubmatch(m)[1], len(n.seen)+1)
		n.seen[m] = r
		return r
	})
}

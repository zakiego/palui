package rpcall_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/fixtures"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Paths that the dashboard's types allow to be an array or null.
var nullableArrays = map[string]bool{
	"getAgentToolsFn.tools":    true, // CatalogTool[] | null
	"getJobs.jobs[].toolScope": true, // string[] | null
}

// A Go nil slice is written as null, where the TS server (drizzle's .all(),
// array literals) always sent []. The dashboard then crashed on
// `null.length` (Home and Activity > Usage on an empty database). Every GET
// function is called on databases filled with each fixture and with nothing,
// and a path that is an array in one answer must never be null in another.
func TestNoNullWhereAnArrayBelongs(t *testing.T) {
	kinds := map[string]map[string]string{} // path -> kind -> where
	for _, name := range []string{"", "empty", "one", "demo", "worst"} {
		label := name
		if label == "" {
			label = "nothing"
		}
		d := testutil.DB(t)
		h := chattest.New(t, chattest.Options{DB: d})
		e := apitest.New(t, func(o *httpapi.Options) {
			o.DB = d
			o.Runtime = &httpapi.Runtime{DB: h.DB, Engine: h.Engine, Sup: h.Sup, Bus: h.Bus, Clock: h.Clock.Now}
		})
		token := e.SignIn()
		if name != "" {
			testutil.Must(t, fixtures.Apply(d, name, fixtures.Options{Now: e.Now.Load(), MasterKey: testutil.TestKey, Wall: e.Now.Load}))
		}
		for _, c := range getCalls(t, d) {
			status, body := e.Get(token, c.name, c.input)
			if status != 200 {
				continue
			}
			var v any
			testutil.Must(t, json.Unmarshal([]byte(body), &v))
			walkKinds(c.name, v, func(path, kind string) {
				if kinds[path] == nil {
					kinds[path] = map[string]string{}
				}
				if _, ok := kinds[path][kind]; !ok {
					kinds[path][kind] = label + " " + c.name + c.input
				}
			})
		}
	}
	var bad []string
	for path, k := range kinds {
		if _, arr := k["array"]; arr && !nullableArrays[path] {
			if where, null := k["null"]; null {
				bad = append(bad, fmt.Sprintf("%s is null on %s, an array on %s", path, where, k["array"]))
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("null where the dashboard expects an array:\n%s", strings.Join(bad, "\n"))
	}
	if len(kinds) < 200 {
		t.Errorf("only %d paths were seen; are the functions answering?", len(kinds))
	}
}

type getCall struct{ name, input string }

// getCalls is every GET function with an input that finds something.
func getCalls(t *testing.T, d *db.DB) []getCall {
	t.Helper()
	first := func(sql string) string {
		var s string
		_ = d.R.QueryRow(sql).Scan(&s)
		return s
	}
	q := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	calls := []getCall{
		{"getEvents", "{}"}, {"getRuns", ""}, {"getUsage", ""}, {"getAgentsPage", ""}, {"getAttachments", ""},
		{"getChats", ""}, {"getConnectorsPage", ""}, {"getFlows", ""}, {"getShell", ""}, {"getHome", ""},
		{"getStatusSummary", ""}, {"getJobs", ""}, {"getNotes", "{}"}, {"getOutbound", ""}, {"getReminders", ""},
		{"getSession", ""}, {"listPasskeys", ""}, {"getSettingsPage", ""}, {"getFixtureState", ""},
		{"getCommandsPage", ""},
	}
	for _, slug := range col(t, d, "SELECT slug FROM agents") {
		calls = append(calls, getCall{"getAgentPage", q(map[string]string{"slug": slug})})
	}
	for _, id := range col(t, d, "SELECT id FROM agents") {
		calls = append(calls, getCall{"getAgentToolsFn", q(map[string]string{"id": id})}, getCall{"getAgentMemoryFn", q(map[string]string{"id": id})}, getCall{"getNotes", q(map[string]string{"agentId": id})})
	}
	for _, id := range col(t, d, "SELECT id FROM runs") {
		calls = append(calls, getCall{"getRun", q(map[string]string{"id": id})})
	}
	for _, id := range col(t, d, "SELECT id FROM saved_attachments") {
		calls = append(calls, getCall{"getAttachmentShares", q(map[string]string{"id": id})})
	}
	for _, id := range col(t, d, "SELECT id FROM chats") {
		calls = append(calls, getCall{"getTranscript", q(map[string]string{"chatId": id})})
	}
	for _, id := range col(t, d, "SELECT id FROM connectors") {
		calls = append(calls, getCall{"getConnectorDetail", q(map[string]string{"id": id})})
	}
	for _, id := range col(t, d, "SELECT id FROM flows") {
		calls = append(calls, getCall{"getFlowFn", q(map[string]string{"id": id})})
	}
	if chat := first("SELECT id FROM chats LIMIT 1"); chat != "" {
		for _, p := range []string{"dailyBriefing", "heartbeat"} {
			calls = append(calls, getCall{"getJobPreset", q(map[string]string{"preset": p, "chatId": chat})})
		}
	}
	return calls
}

func col(t *testing.T, d *db.DB, sql string) []string {
	t.Helper()
	rows, err := d.R.Query(sql)
	list, err := db.All(rows, err, func(s db.Scanner) (string, error) {
		var v string
		return v, s.Scan(&v)
	})
	testutil.Must(t, err)
	return list
}

// walkKinds reports the JSON kind at every path; array items share one path
// (`[]`) and a record's keys are not part of the path when they are ids.
func walkKinds(path string, v any, report func(path, kind string)) {
	switch x := v.(type) {
	case nil:
		report(path, "null")
	case []any:
		report(path, "array")
		for _, it := range x {
			walkKinds(path+"[]", it, report)
		}
	case map[string]any:
		report(path, "object")
		for k, it := range x {
			walkKinds(path+"."+k, it, report)
		}
	default:
		report(path, "scalar")
	}
}

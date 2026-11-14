package rpcsched_test

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
)

// apps/app/src/lib/fns/guards.test.ts for outbound.ts, jobs.ts, reminders.ts
// and flows.ts: every function checks the session, every POST checks the
// origin, and reminder and job writes go through the bridge (here: poke the
// runtime after the write).

var functions = map[string]string{
	"getOutbound": "GET", "setOutboundSwitch": "POST", "saveOutboundLimits": "POST", "addContactFn": "POST", "setContactPolicy": "POST", "removeContactFn": "POST", "cancelOutboundFn": "POST",
	"getJobs": "GET", "runJobNow": "POST", "setJobEnabledFn": "POST", "saveJob": "POST", "deleteJobFn": "POST", "getJobPreset": "GET",
	"getReminders": "GET", "parseReminder": "POST", "createReminderFn": "POST", "cancelReminderFn": "POST", "snoozeReminderFn": "POST",
	"getFlows": "GET", "getFlowFn": "GET",
}

func TestEveryFunctionIsRegisteredWithItsGuard(t *testing.T) {
	got := map[string]httpapi.Handler{}
	for _, h := range httpapi.Handlers() {
		got[h.Name] = h
	}
	for name, method := range functions {
		h, ok := got[name]
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		want := httpapi.User
		if method == http.MethodPost {
			want = httpapi.Writer
		}
		if h.Method != method || h.Guard != want {
			t.Errorf("%s: %s %s, want %s %s", name, h.Method, h.Guard, method, want)
		}
	}
	if len(functions) != 20 {
		t.Fatalf("%d functions", len(functions))
	}
}

func TestEveryFunctionChecksTheSession(t *testing.T) {
	e := apitest.New(t, nil)
	for name, method := range functions {
		var status int
		var body string
		if method == http.MethodGet {
			status, body = e.Get("", name, `{"id":"x","preset":"heartbeat","chatId":"c"}`)
		} else {
			status, body = e.Post("", name, `{"id":"x"}`)
		}
		if status != 401 || body != `{"error":"UNAUTHORIZED","code":"UNAUTHORIZED"}` {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
}

func TestEveryPostChecksTheOrigin(t *testing.T) {
	e := apitest.New(t, nil)
	token := e.SignIn()
	for name, method := range functions {
		if method != http.MethodPost {
			continue
		}
		for _, opts := range [][]apitest.ReqOpt{
			{apitest.Header("origin", "https://evil.example")},
			{apitest.Header("sec-fetch-site", "cross-site"), apitest.SameOrigin()},
			{},
		} {
			res := e.Do(http.MethodPost, "/api/rpc/"+name, `{"id":"x"}`, append(opts, apitest.Cookie(token))...)
			if body := apitest.Body(res); res.StatusCode != 403 || body != `{"error":"cross-origin request refused","code":"FORBIDDEN"}` {
				t.Errorf("%s: %d %s", name, res.StatusCode, body)
			}
		}
		// Without a session a cross-origin write is still refused first.
		res := e.Do(http.MethodPost, "/api/rpc/"+name, `{"id":"x"}`, apitest.Header("origin", "https://evil.example"))
		if res.StatusCode != 403 {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}
}

// The bridges' rule as Go: a handler that writes reminders or jobs pokes the
// runtime after the write.
func TestReminderAndJobWritesPokeTheRuntime(t *testing.T) {
	writes := regexp.MustCompile(`\b(reminders\.(Create|Cancel|SnoozeReminder|Update|MarkDone)|jobs\.(Create|Update|SetEnabled|Delete))\(`)
	checked := 0
	for _, file := range []string{"jobs.go", "reminders.go", "outbound.go", "flows.go"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		// One chunk per function literal or declaration.
		for _, chunk := range regexp.MustCompile(`(?m)^func |Fn: func`).Split(string(b), -1) {
			if !writes.MatchString(chunk) {
				continue
			}
			checked++
			if !strings.Contains(chunk, "poke(c)") {
				t.Errorf("%s: a reminder or job write without poke(c):\n%s", file, chunk)
			}
		}
	}
	if checked < 6 {
		t.Fatalf("only %d writes found", checked)
	}
}

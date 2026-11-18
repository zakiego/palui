package rpcsetup_test

import (
	"net/http"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
)

// The 25 server functions of connectors.ts, settings.ts, home.ts and
// activity.ts, with their methods.
var mine = map[string]string{
	"getConnectorsPage": "GET", "getConnectorDetail": "GET", "pauseConnectorFn": "POST", "resumeConnectorFn": "POST",
	"removeConnectorFn": "POST", "saveConnectorDraft": "POST", "connectConnectorFn": "POST", "testConnectorFn": "POST",
	"connectorActionFn": "POST", "setWhatsAppOwnerFn": "POST", "setConnectorOwnerFn": "POST", "setConnectorDataFn": "POST", "duplicateConnectorFn": "POST",
	"getSettingsPage": "GET", "listConnectorModels": "GET", "saveSettings": "POST", "createHookFn": "POST",
	"getShell": "GET", "getHome": "GET", "dismissChecklist": "POST", "getStatusSummary": "GET",
	"getEvents": "GET", "getRuns": "GET", "getRun": "GET", "getUsage": "GET",
}

func TestEveryFunctionIsRegisteredWithItsGuard(t *testing.T) {
	got := map[string]httpapi.Handler{}
	for _, h := range httpapi.Handlers() {
		got[h.Name] = h
	}
	for name, method := range mine {
		h, ok := got[name]
		switch {
		case !ok:
			t.Errorf("%s isn't registered", name)
		case h.Method != method:
			t.Errorf("%s: %s, want %s", name, h.Method, method)
		case method == http.MethodGet && h.Guard != httpapi.User:
			t.Errorf("%s: guard %s, want user", name, h.Guard)
		case method == http.MethodPost && h.Guard != httpapi.Writer:
			t.Errorf("%s: guard %s, want writer", name, h.Guard)
		}
	}
	if len(mine) != 25 {
		t.Fatal(len(mine))
	}
}

func TestEveryFunctionNeedsASession(t *testing.T) {
	e := apitest.New(t, nil)
	for name, method := range mine {
		var status int
		var body string
		if method == http.MethodGet {
			status, body = e.Get("", name, `{"id":"x"}`)
		} else {
			status, body = e.Post("", name, `{"id":"x"}`)
		}
		if status != 401 || body != `{"error":"UNAUTHORIZED","code":"UNAUTHORIZED"}` {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
}

func TestEveryWriteNeedsTheSameOrigin(t *testing.T) {
	e := apitest.New(t, nil)
	token := e.SignIn()
	for name, method := range mine {
		if method != http.MethodPost {
			continue
		}
		for _, opts := range [][]apitest.ReqOpt{
			{apitest.Header("origin", "https://evil.example")},
			{apitest.Header("origin", apitest.AppURL), apitest.Header("sec-fetch-site", "cross-site")},
			{},
		} {
			res := e.Do(http.MethodPost, "/api/rpc/"+name, `{"id":"x"}`, append(opts, apitest.Cookie(token))...)
			if body := apitest.Body(res); res.StatusCode != 403 || body != `{"error":"cross-origin request refused","code":"FORBIDDEN"}` {
				t.Errorf("%s: %d %s", name, res.StatusCode, body)
			}
		}
	}
	// A GET is a read: no origin needed.
	status, _ := e.Get(token, "getStatusSummary", "")
	if status != 200 {
		t.Fatal(status)
	}
}

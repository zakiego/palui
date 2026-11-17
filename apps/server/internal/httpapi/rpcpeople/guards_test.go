package rpcpeople_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
)

// apps/app/src/lib/fns/guards.test.ts for this package's functions: each one
// checks the session, and every POST checks the origin first.

var mine = map[string]string{
	// agents.ts
	"getAgentsPage": "GET", "getAgentPage": "GET", "getAgentToolsFn": "GET", "getAgentMemoryFn": "GET",
	"saveAgentFn": "POST", "deleteAgentFn": "POST", "setAgentToolPermissionsFn": "POST", "setChatAgentFn": "POST",
	"setChatReplyModeFn": "POST", "setChatAckFn": "POST", "setChatAllowedFn": "POST", "setChatDeployNoticeFn": "POST",
	// members (agents.ts)
	"getAgentMembersFn": "GET", "saveMemberFn": "POST", "setAgentMemberFn": "POST",
	// chats.ts
	"getChats": "GET", "getTranscript": "GET", "sendWebMessage": "POST", "decideApproval": "POST", "stopTurn": "POST",
	// notes.ts
	"getNotes": "GET", "saveInstructionFn": "POST", "setInstructionEnabledFn": "POST", "deleteInstructionFn": "POST",
	"setPreferenceFn": "POST", "deletePreferenceFn": "POST",
	// commands.ts
	"getCommandsPage": "GET", "saveCommandFn": "POST", "setCommandEnabledFn": "POST", "deleteCommandFn": "POST",
	// attachments.ts
	"getAttachments": "GET", "deleteAttachmentFn": "POST", "getAttachmentShares": "GET",
	"createAttachmentShareFn": "POST", "revokeAttachmentShareFn": "POST",
}

func names() []string {
	out := make([]string, 0, len(mine))
	for n := range mine {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestEveryFunctionIsRegisteredWithItsMethodAndGuard(t *testing.T) {
	if len(mine) != 35 {
		t.Fatalf("%d names, want 35", len(mine))
	}
	got := map[string]httpapi.Handler{}
	for _, h := range httpapi.Handlers() {
		got[h.Name] = h
	}
	for _, name := range names() {
		h, ok := got[name]
		if !ok {
			t.Errorf("%s isn't registered", name)
			continue
		}
		want := httpapi.User
		if mine[name] == http.MethodPost {
			want = httpapi.Writer
		}
		if h.Method != mine[name] || h.Guard != want {
			t.Errorf("%s: %s %s, want %s %s", name, h.Method, h.Guard, mine[name], want)
		}
	}
}

func TestEveryFunctionNeedsASession(t *testing.T) {
	e, _ := memoryEnv(t)
	useFake(t, nil)
	for _, name := range names() {
		var status int
		var body string
		if mine[name] == http.MethodGet {
			status, body = e.Get("", name, `{"id":"x","slug":"x","chatId":"x"}`)
		} else {
			status, body = e.Post("", name, `{}`)
		}
		if status != 401 || !strings.Contains(body, `"code":"UNAUTHORIZED"`) {
			t.Errorf("%s without a session: %d %s", name, status, body)
		}
	}
}

func TestEveryPostChecksTheOrigin(t *testing.T) {
	e, token := memoryEnv(t)
	useFake(t, nil)
	for _, name := range names() {
		if mine[name] != http.MethodPost {
			continue
		}
		for _, opts := range [][]apitest.ReqOpt{
			{apitest.Cookie(token)},
			{apitest.Cookie(token), apitest.Header("origin", "https://evil.example.com")},
			{apitest.Cookie(token), apitest.SameOrigin(), apitest.Header("sec-fetch-site", "cross-site")},
		} {
			res := e.Do(http.MethodPost, "/api/rpc/"+name, `{}`, opts...)
			body := apitest.Body(res)
			if res.StatusCode != 403 || !strings.Contains(body, `"code":"FORBIDDEN"`) {
				t.Errorf("%s from another origin: %d %s", name, res.StatusCode, body)
			}
		}
	}
}

func TestGetsRefuseAPostAndPostsAGet(t *testing.T) {
	e, token := memoryEnv(t)
	for _, name := range names() {
		var res *http.Response
		if mine[name] == http.MethodGet {
			res = e.Do(http.MethodPost, "/api/rpc/"+name, `{}`, apitest.Cookie(token), apitest.SameOrigin())
		} else {
			res = e.Do(http.MethodGet, "/api/rpc/"+name, "", apitest.Cookie(token))
		}
		if res.StatusCode != 405 {
			t.Errorf("%s with the wrong method: %d", name, res.StatusCode)
		}
	}
}

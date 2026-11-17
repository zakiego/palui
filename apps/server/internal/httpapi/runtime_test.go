package httpapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/notes"
	"github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/models"
	rem "github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// apps/runtime/src/control.test.ts, as in-process calls: the control API's
// routes are Runtime methods now, with the same status codes on failure.

func runtimeFor(t *testing.T, deps chat.Deps) (*httpapi.Runtime, *chattest.Harness) {
	h := chattest.New(t, chattest.Options{
		Registry: map[string]*connectors.Runtime{"web": web.New(chat.NewBus()), "reminders": reminders.Runtime, "notes": notes.Runtime},
		Rows:     []chattest.Row{{"web", "channel"}, {"reminders", "skill"}, {"notes", "skill"}},
		Deps:     deps,
	})
	return &httpapi.Runtime{DB: h.DB, Engine: h.Engine, Sup: h.Sup, Bus: h.Bus, Log: nil, StartedAt: 7, Clock: h.Clock.Now}, h
}

func statusOf(err error) int {
	var e *httpapi.Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func TestRuntimeStatus(t *testing.T) {
	rt, _ := runtimeFor(t, chat.Deps{})
	st, err := rt.Status()
	testutil.Must(t, err)
	if !st.OK || st.StartedAt != 7 || st.TurnsPaused.Until != nil || st.TurnsPaused.Reason != nil || len(st.ActiveTurns) != 0 {
		t.Fatalf("%+v", st)
	}
	found := false
	for _, c := range st.Connectors {
		if c.ID == "web" && c.Running && c.Status == "on" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", st.Connectors)
	}
}

func TestRuntimeTakesWebChatMessages(t *testing.T) {
	rt, _ := runtimeFor(t, chat.Deps{})
	res, err := rt.WebSend("halo")
	testutil.Must(t, err)
	if !res.OK || !strings.HasPrefix(res.ChatID, "chat_") || res.MessageID == "" {
		t.Fatalf("%+v", res)
	}
	if _, err := rt.WebSend("  "); statusOf(err) != 400 {
		t.Fatal(err)
	}
}

func TestRuntimeRoutesApprovalsStopAndAgents(t *testing.T) {
	rt, h := runtimeFor(t, chat.Deps{})
	if rt.DecideApproval("apr_missing", true) {
		t.Fatal("approved a missing approval")
	}
	if rt.StopChat("chat_x") {
		t.Fatal("stopped a missing chat")
	}
	if err := rt.SwitchAgent("chat_x", ""); statusOf(err) != 404 {
		t.Fatal(err)
	}
	if _, err := rt.AgentTools(context.Background(), "agt_missing"); statusOf(err) != 404 {
		t.Fatal(err)
	}
	palui, err := agents.GetDefault(h.DB, h.Clock.Now())
	testutil.Must(t, err)
	list, err := rt.AgentTools(context.Background(), palui.ID)
	testutil.Must(t, err)
	ok, noteSeen := false, false
	for _, tool := range list {
		// A shared chat gets the chat's reminders, never the owner's notes.
		if tool.Name == "reminders__set_reminder" && tool.Class == "write" && tool.GuestSafe {
			ok = true
		}
		if tool.ConnectorID == "notes" {
			noteSeen = true
			if tool.GuestSafe {
				t.Fatalf("%+v", tool)
			}
		}
	}
	if !ok || !noteSeen {
		t.Fatalf("%+v", list)
	}
	if r := rt.Remove(context.Background(), "nope"); r.OK {
		t.Fatal("removed nothing")
	}
	if _, err := rt.Action(context.Background(), "web", "", nil); statusOf(err) != 400 {
		t.Fatal(err)
	}
	if _, err := rt.Action(context.Background(), "nope", "pair", nil); statusOf(err) != 409 {
		t.Fatal(err)
	}
	if _, err := rt.Action(context.Background(), "web", "pair", nil); statusOf(err) != 404 {
		t.Fatal(err)
	}
}

func TestRuntimeAnswers501ForMissingPartsAnd503WithoutAModel(t *testing.T) {
	rt, _ := runtimeFor(t, chat.Deps{})
	if _, err := rt.RunJob("job_1"); statusOf(err) != 501 || !strings.Contains(err.Error(), "Jobs") {
		t.Fatal(err)
	}
	if _, err := rt.ParseReminder(context.Background(), "besok jam 9", ""); statusOf(err) != 501 {
		t.Fatal(err)
	}
	parse := func(ctx context.Context, text, tz string, now int64, m models.LanguageModel) (rem.ParsedReminder, error) {
		return rem.ParsedReminder{OK: true, Text: "x", TZ: "Asia/Makassar"}, nil
	}
	rt2, _ := runtimeFor(t, chat.Deps{ParseReminder: parse})
	_, err := rt2.ParseReminder(context.Background(), "besok jam 9", "")
	if statusOf(err) != 503 || !strings.Contains(err.Error(), "Model lagi off") {
		t.Fatal(err)
	}
}

func TestAdminReconcileWithTheRuntime(t *testing.T) {
	rt, _ := runtimeFor(t, chat.Deps{})
	token, _ := secret.ControlToken(testutil.TestKey)
	e := newEnv(t, func(o *httpapi.Options) { o.Runtime = rt; o.AdminToken = token })
	res := e.doFrom("127.0.0.1:40000", "POST", "/api/admin/reconcile", header(httpapi.AdminHeader, token))
	if res.StatusCode != 200 || readBody(res) != `{"ok":true}` {
		t.Fatal(res.StatusCode)
	}
}

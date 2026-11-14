package hooks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	hooksconn "github.com/zakiego/palui/apps/server/internal/connectors/hooks"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const KEY = "test-master-key-0123456789-abcdefghijklmnop"

func TestIsABuiltinSkillWithTheAgreedSummary(t *testing.T) {
	m := hooksconn.Manifest
	if m.ID != "hooks" || m.Kind != "skill" || !m.Builtin || m.Icon != "webhook" || m.Summary != "Forward events from other apps to you" {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(m.PauseNote, "503") {
		t.Fatal(m.PauseNote)
	}
}

func TestHasNoToolsAndReportsHookCounts(t *testing.T) {
	c := conntest.Ctx(t, nil, "hooks", nil, nil)
	turn := connectors.TurnInfo{RunID: "r", ChatID: "c", Channel: "web", Trigger: "hook", TZ: "Asia/Makassar"}
	res, err := hooksconn.Runtime.Test(c)
	testutil.Must(t, err)
	if !res.OK || !strings.Contains(res.Detail, "Create a hook") {
		t.Fatalf("%+v", res)
	}
	running, err := hooksconn.Runtime.Start(c)
	testutil.Must(t, err)
	set, err := running.Tools(context.Background(), turn)
	testutil.Must(t, err)
	if set.Len() != 0 {
		t.Fatal("tools offered")
	}
	if got := conntest.Of(running.Live()).String(); got != `{"hooks":0,"enabled":0,"lastAt":null}` {
		t.Fatal(got)
	}
	chat, err := chats.Ensure(c.DB, chats.EnsureInput{Channel: "web", ExternalID: "web"}, 1)
	testutil.Must(t, err)
	created, err := hooks.Create(c.DB, hooks.CreateInput{Name: "Uptime", Mode: "summarize", ChatID: chat.ID}, KEY, 2)
	testutil.Must(t, err)
	h, _, err := hooks.GetBySlug(c.DB, created.Hook.Slug)
	testutil.Must(t, err)
	_, err = hooks.Ingest(c.DB, h, `{"status":"down"}`, 99)
	testutil.Must(t, err)
	if got := conntest.Of(running.Live()).String(); got != `{"hooks":1,"enabled":1,"lastAt":99}` {
		t.Fatal(got)
	}
	res, _ = hooksconn.Runtime.Test(c)
	if res.Detail != "1 hook set up" {
		t.Fatal(res.Detail)
	}
}

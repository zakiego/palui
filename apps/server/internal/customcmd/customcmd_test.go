package customcmd_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/customcmd"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

var reserved = []string{"/report", "/help", "/start"}

func refused(t *testing.T, err error, want string) {
	t.Helper()
	if !customcmd.IsError(err) || !strings.Contains(err.Error(), want) {
		t.Fatalf("want refusal with %q, got %v", want, err)
	}
}

func TestCreateFindAndScopes(t *testing.T) {
	kedua := "Kedua"
	d := testutil.DB(t)
	def, err := agents.GetDefault(d, 1)
	testutil.Must(t, err)
	other, err := agents.Create(d, agents.Input{Slug: "kedua", Name: &kedua, Persona: &kedua}, 1)
	testutil.Must(t, err)

	global, err := customcmd.Create(d, customcmd.Input{Name: "/Report-Fix", Description: "  fix   it ", Prompt: "Fix: {{args}}", AttachReport: true, Enabled: true, Reserved: reserved}, 1)
	testutil.Must(t, err)
	if global.Name != "report-fix" || global.AgentID != nil || global.Description != "fix it" || !global.AttachReport {
		t.Fatalf("%+v", global)
	}
	// The same name for one agent is allowed and wins for that agent only.
	own, err := customcmd.Create(d, customcmd.Input{AgentID: other.ID, Name: "report-fix", Prompt: "Other", Enabled: true, Reserved: reserved}, 2)
	testutil.Must(t, err)

	got, ok, err := customcmd.Find(d, other.ID, "report-fix")
	testutil.Must(t, err)
	if !ok || got.ID != own.ID {
		t.Fatalf("other agent: %+v %v", got, ok)
	}
	got, ok, err = customcmd.Find(d, def.ID, "report-fix")
	testutil.Must(t, err)
	if !ok || got.ID != global.ID {
		t.Fatalf("default agent: %+v %v", got, ok)
	}
	list, err := customcmd.ForAgent(d, other.ID)
	testutil.Must(t, err)
	if len(list) != 1 || list[0].ID != own.ID {
		t.Fatalf("ForAgent: %+v", list)
	}

	// Off means not found.
	_, err = customcmd.SetEnabled(d, own.ID, false, 3)
	testutil.Must(t, err)
	got, _, err = customcmd.Find(d, other.ID, "report-fix")
	testutil.Must(t, err)
	if got.ID != global.ID {
		t.Fatalf("disabled own command should fall back to global, got %+v", got)
	}

	// Deleting the agent deletes its commands.
	_, err = agents.Delete(d, other.ID, 4)
	testutil.Must(t, err)
	all, err := customcmd.List(d)
	testutil.Must(t, err)
	if len(all) != 1 || all[0].ID != global.ID {
		t.Fatalf("after agent delete: %+v", all)
	}
}

func TestRefusals(t *testing.T) {
	d := testutil.DB(t)
	in := func(name, prompt string) customcmd.Input {
		return customcmd.Input{Name: name, Prompt: prompt, Enabled: true, Reserved: reserved}
	}
	_, err := customcmd.Create(d, in("/report", "x"), 1)
	refused(t, err, "built-in")
	_, err = customcmd.Create(d, in("bad name", "x"), 1)
	refused(t, err, "lowercase")
	_, err = customcmd.Create(d, in("-dash", "x"), 1)
	refused(t, err, "lowercase")
	_, err = customcmd.Create(d, in("ok", "  "), 1)
	refused(t, err, "prompt")
	_, err = customcmd.Create(d, in("ok", strings.Repeat("a", customcmd.MaxPrompt+1)), 1)
	refused(t, err, "under")
	_, err = customcmd.Create(d, customcmd.Input{AgentID: "agt_missing", Name: "ok", Prompt: "x", Reserved: reserved}, 1)
	refused(t, err, "agent")

	first, err := customcmd.Create(d, in("ok", "x"), 1)
	testutil.Must(t, err)
	_, err = customcmd.Create(d, in("/OK", "y"), 1)
	refused(t, err, "already exists")
	second, err := customcmd.Create(d, in("two", "y"), 1)
	testutil.Must(t, err)
	_, err = customcmd.Update(d, second.ID, in("ok", "y"), 2)
	refused(t, err, "already exists")
	// Saving a command under its own name is fine.
	_, err = customcmd.Update(d, first.ID, in("ok", "z"), 2)
	testutil.Must(t, err)
	_, err = customcmd.Update(d, "cmd_missing", in("ok", "z"), 2)
	refused(t, err, "doesn't exist")

	for i := 0; i < customcmd.MaxCommands-2; i++ {
		_, err := customcmd.Create(d, in("c"+strings.Repeat("x", i%30)+string(rune('a'+i/30)), "x"), 3)
		testutil.Must(t, err)
	}
	_, err = customcmd.Create(d, in("onemore", "x"), 4)
	refused(t, err, "already")
}

func TestExpand(t *testing.T) {
	cmd := db.CustomCommand{Name: "fix", Prompt: "Fix this: {{args}}"}
	if got := customcmd.Expand(cmd, "  the bug ", ""); got != "Fix this: the bug" {
		t.Fatal(got)
	}
	if got := customcmd.Expand(cmd, "", ""); got != "Fix this:" {
		t.Fatal(got)
	}
	plain := db.CustomCommand{Name: "fix", Prompt: "Fix it."}
	if got := customcmd.Expand(plain, "now", "REPORT"); got != "Fix it.\n\nnow\n\n<debug_report>\nREPORT\n</debug_report>" {
		t.Fatal(got)
	}
}

package notes_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/notes/service.test.ts.

var NOW = time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC).UnixMilli()

func TestInstructionsCreateUpdateListDelete(t *testing.T) {
	d := testutil.DB(t)
	a, err := notes.CreateInstruction(d, notes.InstructionInput{Title: "Jadwal sholat", Body: "Pakai server jadwal-sholat.\n\n\n\nAtau API-nya."}, NOW)
	testutil.Must(t, err)
	if a.Body != "Pakai server jadwal-sholat.\n\nAtau API-nya." {
		t.Fatal(a.Body)
	}
	b, err := notes.UpdateInstruction(d, a.ID, notes.InstructionPatch{Body: testutil.Ptr("Pakai mcp-jadwal-sholat.")}, NOW+1)
	testutil.Must(t, err)
	if b.Title != "Jadwal sholat" || b.Body != "Pakai mcp-jadwal-sholat." || b.UpdatedAt != NOW+1 {
		t.Fatalf("%+v", b)
	}
	if list, _ := notes.ListInstructions(d, notes.ListOptions{}, NOW); len(list) != 1 {
		t.Fatal(len(list))
	}
	if ok, _ := notes.DeleteInstruction(d, a.ID); !ok {
		t.Fatal("delete")
	}
	if ok, _ := notes.DeleteInstruction(d, a.ID); ok {
		t.Fatal("deleted twice")
	}
}

func TestInstructionsRefuseEmptyOversizedAndTooMany(t *testing.T) {
	d := testutil.DB(t)
	if _, err := notes.CreateInstruction(d, notes.InstructionInput{Title: " ", Body: "x"}, NOW); !notes.IsError(err) {
		t.Fatal(err)
	}
	if _, err := notes.CreateInstruction(d, notes.InstructionInput{Title: "t", Body: strings.Repeat("x", 1001)}, NOW); !notes.IsError(err) {
		t.Fatal(err)
	}
	for i := 0; i < notes.MaxInstructions; i++ {
		_, err := notes.CreateInstruction(d, notes.InstructionInput{Title: "t" + string(rune('a'+i)), Body: "x"}, NOW)
		testutil.Must(t, err)
	}
	if _, err := notes.CreateInstruction(d, notes.InstructionInput{Title: "one more", Body: "x"}, NOW); err == nil || !strings.Contains(err.Error(), "20 instruksi") {
		t.Fatal(err)
	}
	if _, err := notes.UpdateInstruction(d, "ins_missing", notes.InstructionPatch{Body: testutil.Ptr("x")}, NOW); !notes.IsError(err) {
		t.Fatal(err)
	}
}

func TestPreferencesNormalizeKeysAndUpsert(t *testing.T) {
	d := testutil.DB(t)
	_, err := notes.SetPreference(d, "Default City", "Banjarmasin", NOW)
	testutil.Must(t, err)
	_, err = notes.SetPreference(d, "default_city", "Martapura", NOW+1)
	testutil.Must(t, err)
	if v, _, _ := notes.GetPreference(d, "default-city"); v != "Martapura" {
		t.Fatal(v)
	}
	if list, _ := notes.ListPreferences(d); len(list) != 1 {
		t.Fatal(len(list))
	}
	if ok, _ := notes.DeletePreference(d, "DEFAULT_CITY"); !ok {
		t.Fatal("delete")
	}
	if _, ok, _ := notes.GetPreference(d, "default_city"); ok {
		t.Fatal("still there")
	}
}

func TestPreferencesRefuseBadKeysAndEmptyOrLongValues(t *testing.T) {
	d := testutil.DB(t)
	for _, c := range [][2]string{{"1city", "x"}, {"city!", "x"}, {"city", "  "}, {"city", strings.Repeat("x", 301)}} {
		if _, err := notes.SetPreference(d, c[0], c[1], NOW); !notes.IsError(err) {
			t.Errorf("%q=%q: %v", c[0], c[1], err)
		}
	}
}

func TestPromptIsEmptyWithNothingSaved(t *testing.T) {
	d := testutil.DB(t)
	if p, err := notes.DefaultPrompt(d, NOW); err != nil || p != "" {
		t.Fatal(p, err)
	}
}

func TestPromptListsEnabledInstructionsAndEveryPreference(t *testing.T) {
	d := testutil.DB(t)
	_, err := notes.CreateInstruction(d, notes.InstructionInput{Title: "Jadwal sholat", Body: "Pakai mcp-jadwal-sholat."}, NOW)
	testutil.Must(t, err)
	_, err = notes.CreateInstruction(d, notes.InstructionInput{Title: "Off", Body: "hidden", Enabled: testutil.Ptr(false)}, NOW)
	testutil.Must(t, err)
	_, err = notes.SetPreference(d, "default_city", "Banjarmasin", NOW)
	testutil.Must(t, err)
	p, err := notes.DefaultPrompt(d, NOW)
	testutil.Must(t, err)
	for _, want := range []string{"- Jadwal sholat: Pakai mcp-jadwal-sholat.", "- default_city: Banjarmasin", "Standing instructions from the owner", "Owner preferences"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(p, "hidden") {
		t.Fatal("disabled instruction shown")
	}
	// The notes block lands in the system prompt.
	system := chat.BuildSystem(chat.SystemInput{Persona: "P", Channel: "telegram", Notes: p, Trigger: "message"})
	for _, want := range []string{"Standing instructions from the owner", "Owner preferences"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

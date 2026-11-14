package notes_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	notesconn "github.com/zakiego/palui/apps/server/internal/connectors/notes"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestClassesInstructionWritesAreDestructivePreferenceWritesAreWrite(t *testing.T) {
	set, err := notesconn.Tools(testutil.DB(t), nil, "")
	testutil.Must(t, err)
	want := "save_instruction:destructive,list_instructions:read,delete_instruction:destructive,save_preference:write,get_preferences:read,delete_preference:write"
	if got := strings.Join(conntest.Classes(set), ","); got != want {
		t.Fatalf("classes %s", got)
	}
}

func TestSavesUpdatesAndDeletesAnInstruction(t *testing.T) {
	d := testutil.DB(t)
	set, err := notesconn.Tools(d, func() int64 { return 1000 }, "")
	testutil.Must(t, err)
	saved := conntest.Call(t, set, "save_instruction", `{"title":"Jadwal sholat","body":"Pakai mcp-jadwal-sholat."}`)
	if !saved.B("ok") {
		t.Fatalf("save: %s", saved)
	}
	id := saved.S("instruction.id")
	updated := conntest.Call(t, set, "save_instruction", `{"id":"`+id+`","title":"Jadwal sholat","body":"Pakai API v1."}`)
	if !strings.Contains(updated.S("message"), "diperbarui") {
		t.Fatalf("update: %s", updated)
	}
	rows, err := notes.ListInstructions(d, notes.ListOptions{}, 1000)
	testutil.Must(t, err)
	if len(rows) != 1 || rows[0].Body != "Pakai API v1." {
		t.Fatalf("rows %+v", rows)
	}
	if !conntest.Call(t, set, "delete_instruction", `{"id":"`+id+`"}`).B("ok") {
		t.Fatal("first delete failed")
	}
	if conntest.Call(t, set, "delete_instruction", `{"id":"`+id+`"}`).B("ok") {
		t.Fatal("second delete succeeded")
	}
}

func TestSetsAndReadsPreferencesWhichShowUpInThePrompt(t *testing.T) {
	d := testutil.DB(t)
	set, err := notesconn.Tools(d, nil, "")
	testutil.Must(t, err)
	if !conntest.Call(t, set, "save_preference", `{"key":"default_city","value":"Banjarmasin"}`).B("ok") {
		t.Fatal("save failed")
	}
	if got := conntest.Call(t, set, "get_preferences", `{}`).JSON("preferences"); got != `{"default_city":"Banjarmasin"}` {
		t.Fatalf("prefs %s", got)
	}
	prompt, err := notes.DefaultPrompt(d, testutil.NOW)
	testutil.Must(t, err)
	if !strings.Contains(prompt, "default_city: Banjarmasin") {
		t.Fatalf("prompt %q", prompt)
	}
	if conntest.Call(t, set, "save_preference", `{"key":"bad key!","value":"x"}`).B("ok") {
		t.Fatal("bad key saved")
	}
	if !conntest.Call(t, set, "delete_preference", `{"key":"default_city"}`).B("ok") {
		t.Fatal("delete failed")
	}
	if conntest.Call(t, set, "delete_preference", `{"key":"default_city"}`).B("ok") {
		t.Fatal("second delete succeeded")
	}
}

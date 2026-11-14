package agents_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestGetDefaultCreatesOnceAndAdoptsOldInstructions(t *testing.T) {
	d := testutil.DB(t)
	_, err := d.Exec("INSERT INTO instructions (id, title, body, created_at, updated_at) VALUES ('ins_old', 't', 'b', 1, 1)")
	testutil.Must(t, err)
	testutil.Must(t, settings.Set(d, "persona", "Persona lama", 1))
	a, err := agents.GetDefault(d, 5)
	testutil.Must(t, err)
	if !a.IsDefault || a.Slug != agents.DefaultSlug || a.Name != agents.DefaultName || a.Persona != "Persona lama" || a.CreatedAt != 5 {
		t.Fatalf("%+v", a)
	}
	b, _ := agents.GetDefault(d, 6)
	if b.ID != a.ID {
		t.Fatal("second default agent")
	}
	var owner string
	testutil.Must(t, d.QueryRow("SELECT agent_id FROM instructions WHERE id = 'ins_old'").Scan(&owner))
	if owner != a.ID {
		t.Fatal(owner)
	}
	if got, ok, _ := agents.Get(d, a.ID); !ok || got.ID != a.ID {
		t.Fatal("get")
	}
}

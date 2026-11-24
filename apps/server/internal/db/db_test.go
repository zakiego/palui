package db_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
)

// Ports packages/db/src/db.test.ts "schema".

func TestAppliesEveryMigrationToAFreshDatabase(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	st, err := db.GetSchemaState(d)
	if err != nil {
		t.Fatal(err)
	}
	list := db.Migrations()
	if !st.OK || st.Version != list[len(list)-1].Name {
		t.Fatalf("%+v", st)
	}
}

func TestReportsADatabaseMigratedByANewerBuild(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	if _, err := d.Exec("INSERT INTO _migrations (name, applied_at) VALUES (?, ?)", "9999_future", 1); err != nil {
		t.Fatal(err)
	}
	st, _ := db.GetSchemaState(d)
	if st.OK || st.Reason != "newer" || !reflect.DeepEqual(st.Unknown, []string{"9999_future"}) {
		t.Fatalf("%+v", st)
	}
}

func TestReportsADatabaseBehindTheCode(t *testing.T) {
	d, err := db.Open(":memory:", db.OpenOptions{NoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st, _ := db.GetSchemaState(d)
	if st.OK || st.Reason != "behind" || len(st.Missing) != 28 {
		t.Fatalf("%+v", st)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	if err := db.Migrate(d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = d.QueryRow("SELECT count(*) FROM _migrations").Scan(&n)
	if n != 28 {
		t.Fatal(n)
	}
}

// The four pragmas hold on the write connection and on every read connection.
func TestPragmasOnEveryConnection(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "sub", "palui.db"), db.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, pool := range []struct {
		name string
		row  func(string) (string, error)
	}{
		{"write", func(p string) (string, error) {
			var v string
			err := d.W.QueryRow("PRAGMA " + p).Scan(&v)
			return v, err
		}},
		{"read", func(p string) (string, error) {
			var v string
			err := d.R.QueryRow("PRAGMA " + p).Scan(&v)
			return v, err
		}},
	} {
		want := map[string]string{"journal_mode": "wal", "synchronous": "1", "foreign_keys": "1", "busy_timeout": "5000"}
		for p, w := range want {
			got, err := pool.row(p)
			if err != nil || strings.ToLower(got) != w {
				t.Errorf("%s %s = %q (%v), want %q", pool.name, p, got, err, w)
			}
		}
	}
	if d.W.Stats().MaxOpenConnections != 1 {
		t.Fatal("write pool must be capped at one connection")
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	err := d.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("INSERT INTO preferences (key, value, updated_at) VALUES ('a', 'b', 1)"); err != nil {
			return err
		}
		return os.ErrClosed
	})
	if err != os.ErrClosed {
		t.Fatal(err)
	}
	var n int
	_ = d.QueryRow("SELECT count(*) FROM preferences").Scan(&n)
	if n != 0 {
		t.Fatal("not rolled back")
	}
}

// 0024 turned voice notes on only for agents that existed next to a voice
// connector, so one added right after the upgrade left the owner's agent off
// (rpt_0mwmyqranb28lw1ivey). 0026 turns the default agent on when a voice
// connector is on, and leaves other agents alone.
func TestDefaultAgentVoiceNotesMigration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		voice   string // "" no voice connector, else its enabled value
		wantDef int
	}{
		{"voice connector on", "1", 1},
		{"voice connector off", "0", 0},
		{"no voice connector", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := db.Open(":memory:", db.OpenOptions{NoMigrate: true})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			all := db.Migrations()
			upTo := 0
			for i, m := range all {
				if m.Name == "0025_session_setup_snapshot" {
					upTo = i + 1
				}
			}
			if err := db.Migrate(d, all[:upTo]); err != nil {
				t.Fatal(err)
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := d.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec("INSERT INTO agents (id, slug, name, persona, is_default, created_at, updated_at) VALUES ('agt_def', 'palui', 'Palui', 'p', 1, 1, 1), ('agt_other', 'other', 'Other', 'p', 0, 1, 1)")
			if tc.voice != "" {
				exec("INSERT INTO connectors (id, type, kind, label, enabled, status, config, created_at, updated_at) VALUES ('voice', 'voice', 'skill', 'Voice', ?, 'on', '{}', 2, 2)", tc.voice)
			}
			if err := db.Migrate(d, all); err != nil {
				t.Fatal(err)
			}
			var def, other int
			_ = d.QueryRow("SELECT transcribe_voice FROM agents WHERE id = 'agt_def'").Scan(&def)
			_ = d.QueryRow("SELECT transcribe_voice FROM agents WHERE id = 'agt_other'").Scan(&other)
			if def != tc.wantDef || other != 0 {
				t.Fatalf("default %d (want %d), other %d (want 0)", def, tc.wantDef, other)
			}
		})
	}
}

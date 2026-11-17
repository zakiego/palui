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
	if st.OK || st.Reason != "behind" || len(st.Missing) != 18 {
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
	if n != 18 {
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

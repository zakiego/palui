package db

import (
	"embed"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one SQL file: its basename without .sql, and its text.
type Migration struct {
	Name string
	SQL  string
}

// Migrations are the bundled files in name order.
func Migrations() []Migration {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		panic(err)
	}
	var out []Migration
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			panic(err)
		}
		out = append(out, Migration{Name: strings.TrimSuffix(e.Name(), ".sql"), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

const createMigrations = `CREATE TABLE IF NOT EXISTS _migrations (
    name TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
  )`

func appliedNames(q Q, ordered bool) ([]string, error) {
	query := "SELECT name FROM _migrations"
	if ordered {
		query += " ORDER BY name"
	}
	rows, err := q.WQuery(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// Migrate applies every migration in list whose name isn't in _migrations.
// Each runs in one transaction; statements are split on "--> statement-breakpoint".
func Migrate(q Q, list []Migration) error {
	if _, err := q.Exec(createMigrations); err != nil {
		return err
	}
	names, err := appliedNames(q, false)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for _, n := range names {
		applied[n] = true
	}
	for _, m := range list {
		if applied[m.Name] {
			continue
		}
		err := q.Tx(func(tx Q) error {
			for _, stmt := range strings.Split(m.SQL, "--> statement-breakpoint") {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			_, err := tx.Exec("INSERT INTO _migrations (name, applied_at) VALUES (?, ?)", m.Name, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// UnknownMigrations are names in _migrations this build doesn't ship. Non-empty
// means a newer build migrated this file and an older one runs on it now.
func UnknownMigrations(q Q, list []Migration) ([]string, error) {
	known := map[string]bool{}
	for _, m := range list {
		known[m.Name] = true
	}
	names, err := appliedNames(q, true)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if !known[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

// SchemaState says whether this build's code matches the database schema.
type SchemaState struct {
	OK bool `json:"ok"`
	// Set when OK: the last bundled migration.
	Version string `json:"version,omitempty"`
	// "behind" (migrations haven't run) or "newer" (a newer image migrated the file).
	Reason  string   `json:"reason,omitempty"`
	Missing []string `json:"missing,omitempty"`
	Unknown []string `json:"unknown,omitempty"`
}

// GetSchemaState compares _migrations with the bundled list.
func GetSchemaState(q Q) (SchemaState, error) {
	if _, err := q.Exec("CREATE TABLE IF NOT EXISTS _migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)"); err != nil {
		return SchemaState{}, err
	}
	list := Migrations()
	unknown, err := UnknownMigrations(q, list)
	if err != nil {
		return SchemaState{}, err
	}
	if len(unknown) > 0 {
		return SchemaState{Reason: "newer", Unknown: unknown}, nil
	}
	names, err := appliedNames(q, false)
	if err != nil {
		return SchemaState{}, err
	}
	applied := map[string]bool{}
	for _, n := range names {
		applied[n] = true
	}
	var missing []string
	for _, m := range list {
		if !applied[m.Name] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		return SchemaState{Reason: "behind", Missing: missing}, nil
	}
	version := "none"
	if len(list) > 0 {
		version = list[len(list)-1].Name
	}
	return SchemaState{OK: true, Version: version}, nil
}

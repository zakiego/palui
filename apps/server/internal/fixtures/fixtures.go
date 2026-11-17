// Package fixtures is apps/app/src/dev/fixtures: named datasets for the
// dashboard, swapped at the data boundary (the SQLite file every page reads).
// Apply clears the product tables and fills them again, so flipping between
// "Demo" and "Worst case" changes what the queries return and nothing else.
// Sign-in rows are left alone, so the session survives a swap.
//
// Dev only: the RPC functions refuse outside dev mode, and `palui dev seed`
// refuses in prod mode.
package fixtures

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
)

// Names are the toggle's segments, in order (src/components/dev/fixture-toggle.tsx).
var Names = []string{"demo", "worst", "empty", "one", "huge"}

var fillers = map[string]func(k *Kit){
	"demo":  demo,
	"worst": worst,
	// A fresh install: no connectors, no chats, nothing to list.
	"empty": func(*Kit) {},
	"one":   one,
	"huge":  huge,
}

// Valid reports whether name is a fixture.
func Valid(name string) bool {
	_, ok := fillers[name]
	return ok
}

// MarkerKey is the `settings` row that remembers which fixture is loaded. Not
// one of the typed settings, so the app's own settings code never reads it.
const MarkerKey = "devFixture"

var productTables = []string{
	"connectors", "identities", "claims", "chats", "sessions", "messages", "runs", "tool_calls", "pending_approvals", "reminders",
	"contacts", "outbound_messages", "jobs", "routines", "routine_occurrences", "outbox", "events", "reports", "hooks", "custom_commands", "instructions",
	"agents", "preferences", "attachment_shares", "saved_attachments", "usage", "settings", "flow_step_runs", "flow_runs", "flow_triggers",
	"flow_versions", "flows", "agent_members", "member_accounts", "members",
}

// RealDataError is the refusal on a database a fixture didn't fill.
var RealDataError = errors.New("This database has data that didn't come from a fixture. Point DATA_DIR at a scratch directory, or pass --force to replace it.")

func clearProductData(q db.Q) error {
	for _, t := range productTables {
		if _, err := q.Exec("DELETE FROM " + t); err != nil {
			return err
		}
	}
	for _, t := range []string{"passkey", "mcp_grants", "oauth_clients"} {
		if _, err := q.Exec("DELETE FROM "+t+" WHERE id LIKE ?", FixturePasskeyPrefix+"%"); err != nil {
			return err
		}
	}
	return nil
}

// Current is currentFixture: the loaded fixture, or "" when a fixture didn't
// fill this database.
func Current(q db.Q) (string, error) {
	var raw string
	err := q.QueryRow("SELECT value FROM settings WHERE key = ?", MarkerKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var name string
	if json.Unmarshal([]byte(raw), &name) != nil || !Valid(name) {
		return "", nil
	}
	return name, nil
}

// HoldsRealData is true when the database has connectors or messages that no
// fixture put there: someone's working dev database, with pairings and
// history worth keeping.
func HoldsRealData(q db.Q) (bool, error) {
	if cur, err := Current(q); err != nil || cur != "" {
		return false, err
	}
	for _, t := range []string{"connectors", "messages"} {
		var n int64
		if err := q.QueryRow("SELECT count(*) FROM " + t).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

// Options is applyFixture's opts plus the master key the TS kit read from
// config.
type Options struct {
	// Now defaults to the current time.
	Now   int64
	Force bool
	// MasterKey encrypts connector secrets, hook secrets and attachments.
	MasterKey string
	// Wall is the clock the default agent's timestamps come from (the TS kit
	// calls getDefaultAgent without a time). Defaults to the current time.
	Wall func() int64
}

// Apply is applyFixture. Destructive: every product row in this database is
// replaced. It refuses a database that holds real data unless Force says the
// caller means it.
func Apply(d db.Q, name string, o Options) (err error) {
	fn, ok := fillers[name]
	if !ok {
		return fmt.Errorf("unknown fixture %q", name)
	}
	if !o.Force {
		real, err := HoldsRealData(d)
		if err != nil {
			return err
		}
		if real {
			return RealDataError
		}
	}
	now := o.Now
	if now == 0 {
		now = time.Now().UnixMilli()
	}
	wall := o.Wall
	if wall == nil {
		wall = func() int64 { return time.Now().UnixMilli() }
	}
	err = d.Tx(func(tx db.Q) (err error) {
		defer func() {
			if r := recover(); r != nil {
				fe, ok := r.(fixtureErr)
				if !ok {
					panic(r)
				}
				err = fe.err
			}
		}()
		if err := clearProductData(tx); err != nil {
			return err
		}
		k, err := newKit(tx, now, o.MasterKey, wall)
		if err != nil {
			return err
		}
		fn(k)
		_, err = tx.Exec("INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)", MarkerKey, str(name), now)
		return err
	})
	if err == nil {
		notifyApplied(d)
	}
	return err
}

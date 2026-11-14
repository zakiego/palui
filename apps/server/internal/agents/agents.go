// Package agents ports the part of packages/core/src/agents.ts that M1's notes
// need: the default agent, made on first use. M2 ports the rest of the file here.
package agents

import (
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
)

const (
	DefaultSlug = "palui"
	DefaultName = "Palui"
)

func findDefault(q db.Q) (db.Agent, bool, error) {
	return db.One(q.WQueryRow("SELECT "+db.AgentCols+" FROM agents WHERE is_default = 1"), db.ScanAgent)
}

// GetDefault returns the default agent, creating it (with the persona setting)
// when there is none. Instructions saved before agents existed become its own.
func GetDefault(q db.Q, now int64) (db.Agent, error) {
	if a, ok, err := findDefault(q); err != nil || ok {
		return a, err
	}
	var out db.Agent
	err := q.Tx(func(tx db.Q) error {
		again, ok, err := findDefault(tx)
		if err != nil {
			return err
		}
		if ok {
			out = again
			return nil
		}
		persona, err := settings.Persona(tx)
		if err != nil {
			return err
		}
		row, err := db.ScanAgent(tx.WQueryRow("INSERT INTO agents (id, slug, name, persona, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, 1, ?, ?) RETURNING "+db.AgentCols,
			secret.NewID("agt"), DefaultSlug, DefaultName, persona, now, now))
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE instructions SET agent_id = ? WHERE agent_id IS NULL", row.ID); err != nil {
			return err
		}
		out = row
		return nil
	})
	return out, err
}

// Get returns an agent by id.
func Get(q db.Q, id string) (db.Agent, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.AgentCols+" FROM agents WHERE id = ?", id), db.ScanAgent)
}

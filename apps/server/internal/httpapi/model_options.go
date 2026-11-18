package httpapi

import (
	"github.com/zakiego/palui/apps/server/internal/connectors"
	connregistry "github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
)

// ModelOption is one model connector a chain step can pick.
type ModelOption struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Status string  `json:"status"`
	Icon   *string `json:"icon,omitempty"`
}

// ModelOptions lists the model connectors, for the model chain editors on the
// Settings page and an agent's page.
func ModelOptions(q db.Q) ([]ModelOption, error) {
	rows, err := connectors.List(q)
	if err != nil {
		return nil, err
	}
	out := []ModelOption{}
	for _, r := range rows {
		if r.Kind != "model" {
			continue
		}
		m := ModelOption{ID: r.ID, Label: r.Label, Status: r.Status}
		if man, ok := connregistry.Manifest(r.Type); ok {
			icon := man.Icon
			m.Icon = &icon
		}
		out = append(out, m)
	}
	return out, nil
}

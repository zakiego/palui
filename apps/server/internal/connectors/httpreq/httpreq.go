package httpreq

import (
	"context"

	"github.com/zakiego/palui/apps/server/internal/connectors"
)

// Detail is the Test result's sentence for a valid config.
func Detail(cfg Config) string {
	s := "Ready. Calls go to " + joinDomains(cfg.Domains) + " only, "
	switch cfg.Auth {
	case "header":
		s += "with the key in the " + cfg.HeaderName + " header."
	case "query":
		s += "with the key in the " + cfg.QueryParam + " parameter."
	default:
		s += "with no key."
	}
	if cfg.Policy == "allow" {
		return s + " Reads run on their own."
	}
	return s + " Every call asks first."
}

// NewRuntime builds the runtime; tests inject the network.
func NewRuntime(o Options) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		// No network call: there's no URL that is safe to hit on every API.
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			cfg, err := ConfigFrom(c.Config, c.Secret("value"))
			if err != nil {
				return connectors.TestResult{OK: false, Detail: err.Error()}, nil
			}
			return connectors.TestResult{OK: true, Detail: Detail(cfg)}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			secret := c.Secret("value")
			cfg, err := ConfigFrom(c.Config, secret)
			if err != nil {
				return nil, err
			}
			return &connectors.RunningConnector{
				Stop: func(context.Context) error { return nil },
				Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
					// The label is read each turn: a rename doesn't restart the connector.
					name := c.ID
					if c.DB != nil {
						if row, ok, err := connectors.Get(c.DB, c.ID); err == nil && ok && row.Label != "" {
							name = row.Label
						}
					}
					return Tools(cfg, name, secret, o), nil
				},
			}, nil
		},
	}
}

var Runtime = NewRuntime(Options{})

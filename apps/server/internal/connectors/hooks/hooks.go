// Package hooks is the hooks connector (connectors/src/hooks): no tools. The
// HTTP layer verifies and ingests requests itself (internal/hooks); the
// connector exists so hooks can be paused like any other connector.
package hooks

import (
	"context"
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:        "hooks",
	Kind:      "skill",
	Name:      "Webhooks",
	Icon:      "webhook",
	Summary:   "Forward events from other apps to you",
	Builtin:   true,
	Trust:     "external",
	Setup:     []connectors.SetupStep{},
	PauseNote: "While paused, hook URLs answer 503.",
	Docs: &connectors.Docs{
		Commands: []string{
			`TS=$(date +%s); curl -X POST $URL -H "x-palui-timestamp: $TS" -H "x-palui-signature: sha256=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | cut -d' ' -f2)" -d "$BODY"`,
		},
		Notes: []string{"The secret is shown once when the hook is created or rotated.", "Bodies over 256 KB are refused; the text is capped at 8,000 characters."},
	},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		rows, err := hooks.List(c.DB)
		if err != nil {
			return connectors.TestResult{}, err
		}
		n := len(rows)
		if n == 0 {
			return connectors.TestResult{OK: true, Detail: "Ready. Create a hook to get its URL and secret."}, nil
		}
		return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d hook%s set up", n, kit.Plural(n))}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
				return connectors.NewToolSet(), nil
			},
			Live: func() *jsonx.Object { return Live(c) },
		}, nil
	},
}

// Live is { hooks, enabled, lastAt }.
func Live(c connectors.Context) *jsonx.Object {
	rows, err := hooks.List(c.DB)
	if err != nil {
		return kit.O("hooks", float64(0), "enabled", float64(0), "lastAt", nil)
	}
	enabled := 0
	var last *int64
	for _, h := range rows {
		if h.Enabled {
			enabled++
		}
		if h.LastAt != nil && *h.LastAt != 0 && (last == nil || *h.LastAt > *last) {
			v := *h.LastAt
			last = &v
		}
	}
	var lastAt any
	if last != nil {
		lastAt = float64(*last)
	}
	return kit.O("hooks", float64(len(rows)), "enabled", float64(enabled), "lastAt", lastAt)
}

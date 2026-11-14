package rpcdev

// apps/app/src/lib/fns/dev-fixtures.ts: the server side of the fixture toggle
// (src/components/dev). Both functions do nothing outside dev mode
// (NODE_ENV=production is prod mode; TS checked for a Vite dev build).

import (
	"errors"
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/fixtures"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// fixtureState: Current is null when the database wasn't filled by a
// fixture, which is also what hides the toggle: it only shows on a database a
// fixture made.
type fixtureState struct {
	Current *string `json:"current"`
}

// ErrNotDev is setFixture's refusal outside dev mode.
var ErrNotDev = errors.New("Fixtures only exist in a dev build.")

func devMode(c *httpapi.Call) bool { return c.Mode() != "prod" }

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getFixtureState",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Fn: func(c *httpapi.Call) (any, error) {
			if !devMode(c) {
				return fixtureState{}, nil
			}
			cur, err := fixtures.Current(c.DB())
			if err != nil || cur == "" {
				return fixtureState{}, err
			}
			return fixtureState{Current: &cur}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "setFixture",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("name", zod.Enum(fixtures.Names...))),
		Fn: func(c *httpapi.Call) (any, error) {
			if !devMode(c) {
				return nil, ErrNotDev
			}
			name, _ := c.Obj().Get("name")
			n := name.(string)
			if err := fixtures.Apply(c.DB(), n, fixtures.Options{Now: c.Now(), MasterKey: c.MasterKey()}); err != nil {
				return nil, err
			}
			return fixtureState{Current: &n}, nil
		},
	})
}

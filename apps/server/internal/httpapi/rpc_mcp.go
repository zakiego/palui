package httpapi

import (
	"errors"
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/mcpaccess"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// The dashboard side of MCP access: the Settings card that lists, creates and
// revokes personal tokens and OAuth connections, and the consent page an
// OAuth client sends the owner to.

type mcpAccessPage struct {
	URL    string                `json:"url"`
	Grants []mcpaccess.GrantView `json:"grants"`
}

type createdToken struct {
	Token string              `json:"token"`
	Grant mcpaccess.GrantView `json:"grant"`
}

type oauthDecision struct {
	RedirectTo string `json:"redirectTo"`
}

func mcpFail(err error) error {
	var me *mcpaccess.Error
	switch {
	case errors.As(err, &me):
		return Fail("%s", me.Message)
	case errors.Is(err, mcpaccess.ErrNotFound):
		return &Error{Status: 404, Code: "NOT_FOUND", Message: "That token or connection is gone already."}
	case errors.Is(err, mcpaccess.ErrRequestGone):
		return &Error{Status: 410, Code: "GONE", Message: mcpaccess.ErrRequestGone.Error()}
	}
	return err
}

func init() {
	Register(Handler{
		Name:   "getMcpAccess",
		Method: http.MethodGet,
		Guard:  User,
		Fn: func(c *Call) (any, error) {
			grants, err := mcpaccess.List(c.DB(), c.Viewer.User.ID, c.Now())
			if err != nil {
				return nil, err
			}
			return mcpAccessPage{URL: mcpaccess.ResourceURL(c.AppURL()), Grants: grants}, nil
		},
	})

	Register(Handler{
		Name:   "createMcpToken",
		Method: http.MethodPost,
		Guard:  Writer,
		Input:  zod.Object(zod.F("name", zod.String().Trim().Min(1).Max(mcpaccess.MaxNameLen))),
		Fn: func(c *Call) (any, error) {
			name, _ := c.Obj().Get("name")
			token, g, err := mcpaccess.CreateToken(c.DB(), c.Viewer.User.ID, name.(string), c.Now())
			if err != nil {
				return nil, mcpFail(err)
			}
			return createdToken{Token: token, Grant: mcpaccess.GrantView{
				ID: g.ID, Kind: g.Kind, Name: g.Name, TokenHint: g.TokenHint, CreatedAt: g.CreatedAt,
			}}, nil
		},
	})

	Register(Handler{
		Name:   "revokeMcpAccess",
		Method: http.MethodPost,
		Guard:  Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100))),
		Fn: func(c *Call) (any, error) {
			id, _ := c.Obj().Get("id")
			if err := mcpaccess.Revoke(c.DB(), c.Viewer.User.ID, id.(string)); err != nil {
				return nil, mcpFail(err)
			}
			return map[string]bool{"ok": true}, nil
		},
	})

	Register(Handler{
		Name:   "getOAuthRequest",
		Method: http.MethodGet,
		Guard:  User,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100))),
		Fn: func(c *Call) (any, error) {
			id, _ := c.Obj().Get("id")
			v, err := mcpaccess.GetRequest(c.DB(), id.(string), c.Now())
			if err != nil {
				return nil, mcpFail(err)
			}
			return v, nil
		},
	})

	Register(Handler{
		Name:   "decideOAuthRequest",
		Method: http.MethodPost,
		Guard:  Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("allow", zod.Bool())),
		Fn: func(c *Call) (any, error) {
			id, _ := c.Obj().Get("id")
			allow, _ := c.Obj().Get("allow")
			to, err := mcpaccess.Decide(c.DB(), id.(string), c.Viewer.User.ID, allow.(bool), c.AppURL(), c.Now())
			if err != nil {
				return nil, mcpFail(err)
			}
			return oauthDecision{RedirectTo: to}, nil
		},
	})
}

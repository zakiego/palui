package httpapi

import (
	"errors"
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/session.ts: session, owner invite and passkey server
// functions. The worked example of Register for M8b.

var inviteToken = zod.String().Min(10).Max(200)

type sessionState struct {
	Viewer      *auth.Viewer `json:"viewer"`
	Configured  bool         `json:"configured"`
	OwnerExists bool         `json:"ownerExists"`
}

type acceptedInvite struct {
	OK      bool `json:"ok"`
	Created bool `json:"created"`
}

func init() {
	Register(Handler{
		Name:   "getSession",
		Method: http.MethodGet,
		Guard:  Public,
		Fn: func(c *Call) (any, error) {
			exists, err := auth.OwnerExists(c.DB())
			if err != nil {
				return nil, err
			}
			// configured: Better Auth needed BETTER_AUTH_SECRET; Go sessions
			// need no secret, so it's always true.
			return sessionState{Viewer: c.Viewer, Configured: true, OwnerExists: exists}, nil
		},
	})

	Register(Handler{
		Name:   "getInvite",
		Method: http.MethodGet,
		Guard:  Public,
		Input:  zod.Object(zod.F("token", inviteToken)),
		Fn: func(c *Call) (any, error) {
			token, _ := c.Obj().Get("token")
			return auth.InspectInvite(c.DB(), token.(string), c.Now())
		},
	})

	// Accepting the invite signs the owner in: the session cookie is set on
	// this response. The page then asks for a passkey.
	Register(Handler{
		Name:   "acceptInvite",
		Method: http.MethodPost,
		Guard:  Public,
		Input:  zod.Object(zod.F("token", inviteToken)),
		Fn: func(c *Call) (any, error) {
			token, _ := c.Obj().Get("token")
			res, err := auth.AcceptOwnerInvite(c.DB(), token.(string), c.Now())
			var ie *auth.InviteError
			if errors.As(err, &ie) {
				return nil, Fail("%s", ie.Msg)
			}
			if err != nil {
				return nil, err
			}
			sessionToken, _, err := auth.CreateSession(c.DB(), res.UserID, auth.ClientIP(c.R.Header), c.R.UserAgent(), c.Now())
			if err != nil {
				// The link is used up by now, so the only way forward is a new one.
				return nil, &Error{Status: 500, Code: "ERROR", Message: "Signing in failed after the link was used. Run `palui owner invite` on the server for a new link."}
			}
			http.SetCookie(c.W, auth.SessionCookie(c.AppURL(), sessionToken))
			return acceptedInvite{OK: true, Created: res.Created}, nil
		},
	})

	Register(Handler{
		Name:   "listPasskeys",
		Method: http.MethodGet,
		Guard:  User,
		Fn: func(c *Call) (any, error) {
			return auth.ListPasskeys(c.DB(), c.Viewer.User.ID)
		},
	})

	Register(Handler{
		Name:   "removePasskey",
		Method: http.MethodPost,
		Guard:  Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(200))),
		Fn: func(c *Call) (any, error) {
			id, _ := c.Obj().Get("id")
			if err := auth.RemovePasskey(c.DB(), c.Viewer.User.ID, id.(string)); err != nil {
				return nil, Fail("%s", err.Error())
			}
			return map[string]bool{"ok": true}, nil
		},
	})
}

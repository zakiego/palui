package rpcpeople

import (
	"errors"
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/customcmd"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// The Commands page (PLAN 8.3): the owner's custom chat commands, for every
// agent or one agent.

func rethrowCommand(err error) error {
	var ce *customcmd.Error
	if errors.As(err, &ce) {
		return httpapi.Fail("%s", ce.Msg)
	}
	return err
}

type commandsPage struct {
	Commands []db.CustomCommand `json:"commands"`
	Agents   []agentRef         `json:"agents"`
	// The built-in names, for the form's own check.
	Builtins []string `json:"builtins"`
}

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getCommandsPage",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Fn: func(c *httpapi.Call) (any, error) {
			q, now := c.DB(), c.Now()
			cmds, err := customcmd.List(q)
			if err != nil {
				return nil, err
			}
			if cmds == nil {
				cmds = []db.CustomCommand{}
			}
			all, err := agents.List(q, now)
			if err != nil {
				return nil, err
			}
			refs := make([]agentRef, 0, len(all))
			for _, a := range all {
				refs = append(refs, agentRef{ID: a.ID, Name: a.Name})
			}
			return commandsPage{Commands: cmds, Agents: refs, Builtins: chat.BuiltinCommands}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "saveCommandFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input: zod.Object(
			zod.F("id", zod.Optional(zod.String().Min(1).Max(100))),
			zod.F("agentId", zod.Nullable(zod.String().Min(1).Max(100))),
			zod.F("name", zod.String().Trim().Min(1).Max(customcmd.MaxName+1)),
			zod.F("description", zod.String().Max(customcmd.MaxDescription*2)),
			zod.F("prompt", zod.String().Trim().Min(1).Max(customcmd.MaxPrompt*2)),
			zod.F("attachReport", zod.Bool()),
			zod.F("enabled", zod.Bool()),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				ID           *string `json:"id"`
				AgentID      *string `json:"agentId"`
				Name         string  `json:"name"`
				Description  string  `json:"description"`
				Prompt       string  `json:"prompt"`
				AttachReport bool    `json:"attachReport"`
				Enabled      bool    `json:"enabled"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			input := customcmd.Input{Name: in.Name, Description: in.Description, Prompt: in.Prompt, AttachReport: in.AttachReport, Enabled: in.Enabled, Reserved: chat.BuiltinCommands}
			if in.AgentID != nil {
				input.AgentID = *in.AgentID
			}
			var row db.CustomCommand
			var err error
			if in.ID != nil {
				row, err = customcmd.Update(c.DB(), *in.ID, input, c.Now())
			} else {
				row, err = customcmd.Create(c.DB(), input, c.Now())
			}
			if err != nil {
				return nil, rethrowCommand(err)
			}
			return struct {
				ID string `json:"id"`
			}{row.ID}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "setCommandEnabledFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("enabled", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("id")
			enabled, _ := o.Get("enabled")
			ok, err := customcmd.SetEnabled(c.DB(), id.(string), enabled.(bool), c.Now())
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail("That command doesn't exist anymore.")
			}
			return okResult{OK: true}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "deleteCommandFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			ok, err := customcmd.Delete(c.DB(), id.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail("That command doesn't exist anymore.")
			}
			return okResult{OK: true}, nil
		},
	})
}

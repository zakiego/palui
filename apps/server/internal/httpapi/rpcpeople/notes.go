package rpcpeople

import (
	"errors"
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/notes.ts: the owner's standing instructions (per
// agent, PLAN 19.1) and preferences. Writes go straight to the tables.

// rethrowNotes: a NotesError's message is shown as is.
func rethrowNotes(err error) error {
	var ne *notes.Error
	if errors.As(err, &ne) {
		return httpapi.Fail("%s", ne.Msg)
	}
	return err
}

type agentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type notesPage struct {
	Agent        chatAgent        `json:"agent"`
	Agents       []agentRef       `json:"agents"`
	Instructions []db.Instruction `json:"instructions"`
	Preferences  []db.Preference  `json:"preferences"`
}

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getNotes",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("agentId", zod.Optional(zod.String().Min(1).Max(100)))),
		Fn: func(c *httpapi.Call) (any, error) {
			q, now := c.DB(), c.Now()
			var agent db.Agent
			found := false
			if v, ok := c.Obj().Get("agentId"); ok {
				a, ok, err := agents.Get(q, v.(string))
				if err != nil {
					return nil, err
				}
				agent, found = a, ok
			}
			if !found {
				a, err := agents.GetDefault(q, now)
				if err != nil {
					return nil, err
				}
				agent = a
			}
			all, err := agents.List(q, now)
			if err != nil {
				return nil, err
			}
			refs := make([]agentRef, 0, len(all))
			for _, a := range all {
				refs = append(refs, agentRef{ID: a.ID, Name: a.Name})
			}
			ins, err := notes.ListInstructions(q, notes.ListOptions{AgentID: agent.ID}, now)
			if err != nil {
				return nil, err
			}
			prefs, err := notes.ListPreferences(q)
			if err != nil {
				return nil, err
			}
			if prefs == nil {
				prefs = []db.Preference{}
			}
			return notesPage{Agent: chatAgent{ID: agent.ID, Name: agent.Name, IsDefault: agent.IsDefault}, Agents: refs, Instructions: instructionsOrEmpty(ins), Preferences: prefs}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "saveInstructionFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input: zod.Object(
			zod.F("id", zod.Optional(zod.String().Min(1).Max(100))),
			zod.F("agentId", zod.Optional(zod.String().Min(1).Max(100))),
			zod.F("title", zod.String().Trim().Min(1).Max(notes.MaxInstructionTitle)),
			zod.F("body", zod.String().Trim().Min(1).Max(notes.MaxInstructionBody)),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				ID      *string `json:"id"`
				AgentID *string `json:"agentId"`
				Title   string  `json:"title"`
				Body    string  `json:"body"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			var row db.Instruction
			var err error
			if in.ID != nil {
				row, err = notes.UpdateInstruction(c.DB(), *in.ID, notes.InstructionPatch{Title: &in.Title, Body: &in.Body}, c.Now())
			} else {
				agent := ""
				if in.AgentID != nil {
					agent = *in.AgentID
				}
				row, err = notes.CreateInstruction(c.DB(), notes.InstructionInput{Title: in.Title, Body: in.Body, AgentID: agent}, c.Now())
			}
			if err != nil {
				return nil, rethrowNotes(err)
			}
			return struct {
				ID string `json:"id"`
			}{row.ID}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "setInstructionEnabledFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("enabled", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("id")
			enabled, _ := o.Get("enabled")
			on := enabled.(bool)
			if _, err := notes.UpdateInstruction(c.DB(), id.(string), notes.InstructionPatch{Enabled: &on}, c.Now()); err != nil {
				return nil, rethrowNotes(err)
			}
			return okResult{OK: true}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "deleteInstructionFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			ok, err := notes.DeleteInstruction(c.DB(), id.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail("That instruction doesn't exist anymore.")
			}
			return okResult{OK: true}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "setPreferenceFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("key", zod.String().Trim().Min(1).Max(40)), zod.F("value", zod.String().Trim().Min(1).Max(notes.MaxPreferenceValue))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			key, _ := o.Get("key")
			value, _ := o.Get("value")
			row, err := notes.SetPreference(c.DB(), key.(string), value.(string), c.Now())
			if err != nil {
				return nil, rethrowNotes(err)
			}
			return struct {
				Key string `json:"key"`
			}{row.Key}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "deletePreferenceFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("key", zod.String().Min(1).Max(40))),
		Fn: func(c *httpapi.Call) (any, error) {
			key, _ := c.Obj().Get("key")
			if _, err := notes.DeletePreference(c.DB(), key.(string)); err != nil {
				return nil, err
			}
			return okResult{OK: true}, nil
		},
	})
}

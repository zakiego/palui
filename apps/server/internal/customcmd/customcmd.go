// Package customcmd holds the owner's custom chat commands (PLAN 8.3): a
// prompt saved under a /name in the dashboard, for every agent or one agent.
// Typing the name in a chat runs a normal turn with that prompt, so the
// command can do anything the agent's tools can (start a T3 Code thread, look
// something up). Built-in commands and flow chat triggers are matched first.
package customcmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	MaxCommands    = 50
	MaxName        = 32
	MaxDescription = 120
	MaxPrompt      = 4000
	// ArgsPlaceholder is where the text typed after the command goes. A prompt
	// without it gets that text appended.
	ArgsPlaceholder = "{{args}}"
)

// NameRe is a command name without its slash: lowercase letters, digits,
// "_" and "-", starting with a letter or digit.
var NameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Error is a refusal whose text the dashboard shows as is.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func fail(format string, args ...any) error { return &Error{Msg: fmt.Sprintf(format, args...)} }

// IsError reports whether err is a refusal.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// NormalizeName lowercases, trims and drops a leading slash.
func NormalizeName(name string) string {
	return strings.TrimPrefix(strings.ToLower(strx.Trim(name)), "/")
}

// List is every command, global ones first, then by name.
func List(q db.Q) ([]db.CustomCommand, error) {
	rows, err := q.Query("SELECT " + db.CustomCommandCols + " FROM custom_commands ORDER BY agent_id IS NOT NULL, agent_id, name")
	return db.All(rows, err, db.ScanCustomCommand)
}

func Get(q db.Q, id string) (db.CustomCommand, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.CustomCommandCols+" FROM custom_commands WHERE id = ?", id), db.ScanCustomCommand)
}

// Find is the enabled command a chat served by agentID runs for name: the
// agent's own first, then the global one.
func Find(q db.Q, agentID, name string) (db.CustomCommand, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.CustomCommandCols+" FROM custom_commands WHERE name = ? AND enabled = 1 AND (agent_id = ? OR agent_id IS NULL) ORDER BY agent_id IS NULL LIMIT 1", name, agentID), db.ScanCustomCommand)
}

// ForAgent is every enabled command agentID's chats can run, by name, an
// agent's own command hiding a global one with the same name.
func ForAgent(q db.Q, agentID string) ([]db.CustomCommand, error) {
	rows, err := q.Query("SELECT "+db.CustomCommandCols+" FROM custom_commands WHERE enabled = 1 AND (agent_id = ? OR agent_id IS NULL) ORDER BY name, agent_id IS NULL", agentID)
	all, err := db.All(rows, err, db.ScanCustomCommand)
	if err != nil {
		return nil, err
	}
	var out []db.CustomCommand
	for _, c := range all {
		if n := len(out); n > 0 && out[n-1].Name == c.Name {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// Input is a command to create or the full new state of one to update.
// AgentID "" is every agent. Reserved are the built-in names, with slashes.
type Input struct {
	AgentID      string
	Name         string
	Description  string
	Prompt       string
	AttachReport bool
	Enabled      bool
	Reserved     []string
}

type clean struct {
	agentID     *string
	name        string
	description string
	prompt      string
}

func check(q db.Q, in Input, selfID string) (clean, error) {
	name := NormalizeName(in.Name)
	if name == "" {
		return clean{}, fail("Give the command a name.")
	}
	if !NameRe.MatchString(name) {
		return clean{}, fail("Use up to %d lowercase letters, digits, - or _ for the name, like /report-fix.", MaxName)
	}
	for _, r := range in.Reserved {
		if r == "/"+name {
			return clean{}, fail("/%s is a built-in command. Pick another name.", name)
		}
	}
	description := strings.Join(strings.Fields(strx.Trim(in.Description)), " ")
	if strx.Len16(description) > MaxDescription {
		return clean{}, fail("Keep the description under %d characters.", MaxDescription)
	}
	prompt := strx.Trim(strx.NormalizeNewlines(in.Prompt))
	if prompt == "" {
		return clean{}, fail("Write the prompt the command sends.")
	}
	if strx.Len16(prompt) > MaxPrompt {
		return clean{}, fail("Keep the prompt under %d characters.", MaxPrompt)
	}
	var agentID *string
	if in.AgentID != "" {
		if _, ok, err := agents.Get(q, in.AgentID); err != nil {
			return clean{}, err
		} else if !ok {
			return clean{}, fail("That agent doesn't exist anymore.")
		}
		id := in.AgentID
		agentID = &id
	}
	var taken int
	if err := q.QueryRow("SELECT count(*) FROM custom_commands WHERE ifnull(agent_id, '') = ? AND name = ? AND id != ?", in.AgentID, name, selfID).Scan(&taken); err != nil {
		return clean{}, err
	}
	if taken > 0 {
		where := "for every agent"
		if agentID != nil {
			where = "for this agent"
		}
		return clean{}, fail("/%s already exists %s.", name, where)
	}
	return clean{agentID: agentID, name: name, description: description, prompt: prompt}, nil
}

func Create(q db.Q, in Input, now int64) (db.CustomCommand, error) {
	c, err := check(q, in, "")
	if err != nil {
		return db.CustomCommand{}, err
	}
	var n int
	if err := q.QueryRow("SELECT count(*) FROM custom_commands").Scan(&n); err != nil {
		return db.CustomCommand{}, err
	}
	if n >= MaxCommands {
		return db.CustomCommand{}, fail("There are already %d commands. Delete one first.", MaxCommands)
	}
	return db.ScanCustomCommand(q.WQueryRow("INSERT INTO custom_commands (id, agent_id, name, description, prompt, attach_report, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.CustomCommandCols,
		secret.NewID("cmd"), c.agentID, c.name, c.description, c.prompt, db.Bool(in.AttachReport), db.Bool(in.Enabled), now, now))
}

func Update(q db.Q, id string, in Input, now int64) (db.CustomCommand, error) {
	if _, ok, err := Get(q, id); err != nil {
		return db.CustomCommand{}, err
	} else if !ok {
		return db.CustomCommand{}, fail("That command doesn't exist anymore.")
	}
	c, err := check(q, in, id)
	if err != nil {
		return db.CustomCommand{}, err
	}
	return db.ScanCustomCommand(q.WQueryRow("UPDATE custom_commands SET agent_id = ?, name = ?, description = ?, prompt = ?, attach_report = ?, enabled = ?, updated_at = ? WHERE id = ? RETURNING "+db.CustomCommandCols,
		c.agentID, c.name, c.description, c.prompt, db.Bool(in.AttachReport), db.Bool(in.Enabled), now, id))
}

func SetEnabled(q db.Q, id string, enabled bool, now int64) (bool, error) {
	n, err := db.Changes(q.Exec("UPDATE custom_commands SET enabled = ?, updated_at = ? WHERE id = ?", db.Bool(enabled), now, id))
	return n > 0, err
}

func Delete(q db.Q, id string) (bool, error) {
	n, err := db.Changes(q.Exec("DELETE FROM custom_commands WHERE id = ?", id))
	return n > 0, err
}

// Expand is the prompt the turn sees: args in place of {{args}} (or appended
// when the prompt has no placeholder), then the report snapshot when there is
// one, fenced as data.
func Expand(cmd db.CustomCommand, args, report string) string {
	args = strx.Trim(args)
	prompt := cmd.Prompt
	if strings.Contains(prompt, ArgsPlaceholder) {
		prompt = strx.Trim(strings.ReplaceAll(prompt, ArgsPlaceholder, args))
	} else if args != "" {
		prompt += "\n\n" + args
	}
	if report != "" {
		prompt += "\n\n<debug_report>\n" + report + "\n</debug_report>"
	}
	return prompt
}

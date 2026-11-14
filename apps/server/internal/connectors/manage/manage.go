// Package manage ports the connector actions of apps/app/src/server/connectors.ts
// that write desired state: the CLI (`palui connectors ...`) and the dashboard's
// connector endpoints (M8b) share them. Each writes through the connectors
// store; the caller pokes the supervisor afterwards (sup.Poke in process). The
// write is the source of truth: the supervisor reconciles on start and every
// 30 s anyway.
package manage

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// Error is ConnectorError: a message for the owner plus per-field problems the
// form points at.
type Error struct {
	Message string
	Fields  *jsonx.Map[string]
}

func (e *Error) Error() string { return e.Message }

func fail(msg string) error { return &Error{Message: msg, Fields: jsonx.NewMap[string]()} }

// FieldsText is the CLI's "<message> k: v; k: v".
func (e *Error) FieldsText() string {
	if e.Fields == nil || e.Fields.Len() == 0 {
		return e.Message
	}
	out := e.Message + " "
	for i, k := range e.Fields.Keys() {
		v, _ := e.Fields.Get(k)
		if i > 0 {
			out += "; "
		}
		out += k + ": " + v
	}
	return out
}

// IsError reports whether err is an *Error.
func IsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// MustExist is mustExist.
func MustExist(q db.Q, id string) (db.Connector, error) {
	row, found, err := connectors.Get(q, id)
	if err != nil {
		return db.Connector{}, err
	}
	if !found {
		return db.Connector{}, fail(fmt.Sprintf("No connector %q. Run `palui connectors list` to see them.", id))
	}
	return row, nil
}

// Pause is pauseConnectorAction without the poke.
func Pause(q db.Q, id, by string, now int64) error {
	row, err := MustExist(q, id)
	if err != nil {
		return err
	}
	if row.Status == "not_set_up" {
		return fail(row.Label + " isn't set up yet, so there's nothing to pause.")
	}
	return connectors.Pause(q, id, by, now)
}

// Resume is resumeConnectorAction without the poke.
func Resume(q db.Q, id, by string, now int64) error {
	row, err := MustExist(q, id)
	if err != nil {
		return err
	}
	if row.Status == "not_set_up" {
		return fail(row.Label + " isn't set up yet. Open it on the Connectors page and connect it first.")
	}
	return connectors.Resume(q, id, by, now)
}

// DraftInput is DraftInput. Config values are jsonx values; a key the
// manifest doesn't know is ignored.
type DraftInput struct {
	ID    string
	Type  string
	Label *string
	// Keys in order; "" or nil clears the stored value.
	Config *jsonx.Object
	// Only keys sent are replaced; "" deletes that secret.
	Secrets *jsonx.Map[string]
	Trust   *string
	// The agent that owns it (nil: global) when AgentIDSet; left out, it's kept.
	AgentID    *string
	AgentIDSet bool
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func issueText(err error) string {
	var ze *zod.Error
	if errors.As(err, &ze) && len(ze.Issues) > 0 {
		return ze.Issues[0].Message
	}
	return "Invalid value"
}

func configField(m connectors.Manifest, key string) zod.Schema {
	if m.ConfigZod == nil {
		return nil
	}
	return m.ConfigZod.Field(key)
}

func secretField(m connectors.Manifest, key string) zod.Schema {
	for _, f := range m.SecretZod {
		if f.Key == key {
			return f.Schema
		}
	}
	return nil
}

// SaveDraft is saveDraft: wizard progress as a `not_set_up` draft, or new
// config on a connector that's already set up. Fields are checked one by one so
// the form can point at the bad one.
func SaveDraft(q db.Q, in DraftInput, masterKey string, now int64) (db.Connector, error) {
	m, ok := registry.Manifest(in.Type)
	if !ok {
		return db.Connector{}, fail(fmt.Sprintf("Unknown connector type %q.", in.Type))
	}
	fields := jsonx.NewMap[string]()
	config := jsonx.NewMap[any]()
	var clear []string
	if in.Config != nil {
		for _, k := range in.Config.Keys() {
			v, _ := in.Config.Get(k)
			schema := configField(m, k)
			if schema == nil {
				continue
			}
			if v == nil || v == "" || zod.IsUndefined(v) {
				config.Delete(k)
				clear = append(clear, k)
				continue
			}
			out, err := zod.Parse(schema, v)
			if err != nil {
				fields.Set(k, issueText(err))
				continue
			}
			config.Set(k, out)
			clear = slices.DeleteFunc(clear, func(c string) bool { return c == k })
		}
	}
	secrets := jsonx.NewMap[string]()
	if in.Secrets != nil {
		for _, k := range in.Secrets.Keys() {
			v, _ := in.Secrets.Get(k)
			schema := secretField(m, k)
			if schema == nil {
				continue
			}
			if v == "" {
				secrets.Set(k, "")
				continue
			}
			out, err := zod.Parse(schema, v)
			if err != nil {
				fields.Set(k, issueText(err))
				continue
			}
			secrets.Set(k, fmt.Sprint(out))
		}
	}
	if fields.Len() > 0 {
		return db.Connector{}, &Error{Message: "Some fields need a fix.", Fields: fields}
	}
	if !idRe.MatchString(in.ID) {
		return db.Connector{}, fail("Connector ids use lowercase letters, digits and dashes.")
	}
	existing, found, err := connectors.Get(q, in.ID)
	if err != nil {
		return db.Connector{}, err
	}
	if found && existing.Type != m.ID {
		return db.Connector{}, fail(fmt.Sprintf("%q is already used by another connector.", in.ID))
	}
	// Only tool connectors have an owner, and only an agent that exists.
	if in.AgentIDSet && in.AgentID != nil && *in.AgentID != "" {
		list, err := agents.List(q, now)
		if err != nil {
			return db.Connector{}, err
		}
		if !slices.ContainsFunc(list, func(a db.Agent) bool { return a.ID == *in.AgentID }) {
			return db.Connector{}, fail("That agent doesn't exist anymore.")
		}
	}
	up := connectors.UpsertInput{ID: in.ID, Manifest: m.Row(), Label: in.Label, Config: config, Secrets: secrets, Trust: in.Trust, ClearConfig: clear}
	if m.Kind == "tools" && in.AgentIDSet {
		up.AgentIDSet = true
		if in.AgentID != nil && *in.AgentID != "" {
			up.AgentID = in.AgentID
		}
	}
	return connectors.Upsert(q, up, masterKey, now)
}

// MissingForConnect is missingForConnect: required config fields and secrets
// that are still empty.
func MissingForConnect(row db.Connector, m connectors.Manifest, secretValues *jsonx.Map[string]) *jsonx.Map[string] {
	missing := jsonx.NewMap[string]()
	if m.ConfigZod != nil {
		var raw any = row.Config
		if row.Config == nil {
			raw = jsonx.NewMap[any]()
		}
		if _, err := zod.Parse(m.ConfigZod, raw); err != nil {
			var ze *zod.Error
			if errors.As(err, &ze) {
				for _, issue := range ze.Issues {
					if len(issue.Path) == 0 {
						continue
					}
					key := fmt.Sprint(issue.Path[0])
					if key != "" && m.ConfigZod.Field(key) != nil {
						missing.Set(key, issue.Message)
					}
				}
			}
		}
	}
	optional := map[string]bool{}
	for _, s := range m.Setup {
		if s.Field != "" && s.Optional {
			optional[s.Field] = true
		}
	}
	for _, f := range m.SecretZod {
		v, _ := secretValues.Get(f.Key)
		if v == "" && !optional[f.Key] {
			missing.Set(f.Key, "Required")
		}
	}
	return missing
}

// Connect is connectConnectorAction without the poke.
func Connect(q db.Q, id, masterKey string, now int64) error {
	row, err := MustExist(q, id)
	if err != nil {
		return err
	}
	m, ok := registry.Manifest(row.Type)
	if !ok {
		return fail(fmt.Sprintf("Unknown connector type %q.", row.Type))
	}
	secrets, err := connectors.Secrets(row, masterKey)
	if err != nil {
		return err
	}
	if missing := MissingForConnect(row, m, secrets); missing.Len() > 0 {
		return &Error{Message: "Fill in the required fields first.", Fields: missing}
	}
	return connectors.Connect(q, id, now)
}

// Item is one row of listConnectorItems (secrets masked).
type Item struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Kind         string             `json:"kind"`
	Label        string             `json:"label"`
	Enabled      bool               `json:"enabled"`
	Status       string             `json:"status"`
	StatusDetail *string            `json:"statusDetail"`
	Trust        string             `json:"trust"`
	AgentID      *string            `json:"agentId"`
	Config       *jsonx.Object      `json:"config"`
	Secrets      *jsonx.Map[string] `json:"secrets"`
	SecretsError *string            `json:"secretsError"`
	UpdatedAt    int64              `json:"updatedAt"`
}

// ListItems is listConnectorItems: what the Connectors page renders.
func ListItems(q db.Q, masterKey string) ([]Item, error) {
	rows, err := connectors.List(q)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		secrets, err := connectors.MaskedSecrets(row, masterKey)
		var secretsError *string
		if err != nil {
			secrets = jsonx.NewMap[string]()
			msg := "These credentials can't be decrypted with the current MASTER_KEY."
			secretsError = &msg
		}
		out = append(out, Item{ID: row.ID, Type: row.Type, Kind: row.Kind, Label: row.Label, Enabled: row.Enabled, Status: row.Status, StatusDetail: row.StatusDetail, Trust: row.Trust, AgentID: row.AgentID, Config: row.Config, Secrets: secrets, SecretsError: secretsError, UpdatedAt: row.UpdatedAt})
	}
	return out, nil
}

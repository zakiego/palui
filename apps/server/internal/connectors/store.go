// Package connectors ports packages/core/src/connectors. M1 has store.ts: the
// desired connector state, the `connectors` rows. The dashboard writes here
// (connect, pause, resume, remove, configure); the supervisor (M2) reconciles
// running handles against these rows.
package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// ConfigHash is sha256(JSON.stringify(config) + "\0" + secretsBlob), 16 hex chars.
func ConfigHash(config *jsonx.Object, secretsBlob *string) string {
	if config == nil {
		config = jsonx.NewMap[any]()
	}
	h := sha256.New()
	h.Write([]byte(jsonx.Stringify(config)))
	h.Write([]byte{0})
	if secretsBlob != nil {
		h.Write([]byte(*secretsBlob))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func List(q db.Q) ([]db.Connector, error) {
	rows, err := q.Query("SELECT " + db.ConnectorCols + " FROM connectors ORDER BY sort_order ASC, created_at ASC, id ASC")
	return db.All(rows, err, db.ScanConnector)
}

func Get(q db.Q, id string) (db.Connector, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ConnectorCols+" FROM connectors WHERE id = ?", id), db.ScanConnector)
}

func secretsOf(row db.Connector) string {
	if row.Secrets == nil {
		return ""
	}
	return *row.Secrets
}

// Secrets decrypts a row's secrets, in their stored key order.
func Secrets(row db.Connector, masterKey string) (*jsonx.Map[string], error) {
	return secret.DecryptJSON(secretsOf(row), masterKey)
}

// MaskedSecrets is what the UI gets: secrets are write-only.
func MaskedSecrets(row db.Connector, masterKey string) (*jsonx.Map[string], error) {
	all, err := Secrets(row, masterKey)
	if err != nil {
		return nil, err
	}
	out := jsonx.NewMap[string]()
	for _, k := range all.Keys() {
		if v, _ := all.Get(k); v != "" {
			out.Set(k, secret.MaskSecret(v))
		}
	}
	return out, nil
}

// RowManifest is the part of a connector manifest the store needs.
type RowManifest struct {
	ID    string
	Kind  string
	Name  string
	Trust string
}

type UpsertInput struct {
	ID       string
	Manifest RowManifest
	Label    *string
	Config   *jsonx.Object
	// Only keys present are replaced; an empty string deletes that secret.
	Secrets *jsonx.Map[string]
	Trust   *string
	Enabled *bool
	// The agents it serves (set AgentIDsSet; empty is every agent). Left
	// unset, they're kept.
	AgentIDs    []string
	AgentIDsSet bool
	// Config keys to drop after the merge (saveDraft: an emptied field becomes
	// undefined and JSON.stringify leaves it out).
	ClearConfig []string
}

func mergeSecrets(existing *jsonx.Map[string], patch *jsonx.Map[string]) *jsonx.Map[string] {
	merged := existing.Clone()
	for _, k := range patch.Keys() {
		v, _ := patch.Get(k)
		if v == "" {
			merged.Delete(k)
		} else {
			merged.Set(k, v)
		}
	}
	return merged
}

func encryptOrNil(m *jsonx.Map[string], masterKey string) (*string, error) {
	if m.Len() == 0 {
		return nil, nil
	}
	blob, err := secret.EncryptJSON(m, masterKey)
	if err != nil {
		return nil, err
	}
	return &blob, nil
}

// Upsert creates or updates a connector row. New rows start as `not_set_up`
// drafts so the wizard keeps its progress when the sheet closes.
func Upsert(q db.Q, in UpsertInput, masterKey string, now int64) (db.Connector, error) {
	existing, found, err := Get(q, in.ID)
	if err != nil {
		return db.Connector{}, err
	}
	var secretsBlob *string
	if found {
		secretsBlob = existing.Secrets
	}
	if in.Secrets != nil && in.Secrets.Len() > 0 {
		base := jsonx.NewMap[string]()
		if found {
			if base, err = Secrets(existing, masterKey); err != nil {
				return db.Connector{}, err
			}
		}
		if secretsBlob, err = encryptOrNil(mergeSecrets(base, in.Secrets), masterKey); err != nil {
			return db.Connector{}, err
		}
	}
	config := jsonx.NewMap[any]()
	if found {
		config = existing.Config.Clone()
	}
	config.Assign(in.Config)
	for _, k := range in.ClearConfig {
		config.Delete(k)
	}
	hash := ConfigHash(config, secretsBlob)
	cfg := db.JSON[*jsonx.Object]{V: config}
	if !found {
		label := in.Manifest.Name
		if in.Label != nil {
			label = *in.Label
		}
		trust := in.Manifest.Trust
		if in.Trust != nil {
			trust = *in.Trust
		}
		if trust == "" {
			trust = "own"
		}
		enabled := false
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		ids, first := agentCols(in.AgentIDs)
		return db.ScanConnector(q.WQueryRow("INSERT INTO connectors (id, type, kind, label, enabled, status, trust, config, secrets, config_hash, agent_ids, agent_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'not_set_up', ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.ConnectorCols,
			in.ID, in.Manifest.ID, in.Manifest.Kind, label, db.Bool(enabled), trust, cfg, secretsBlob, hash, ids, first, now, now))
	}
	label := existing.Label
	if in.Label != nil {
		label = *in.Label
	}
	trust := existing.Trust
	if in.Trust != nil {
		trust = *in.Trust
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	agentIDs := existing.AgentIDs
	if in.AgentIDsSet {
		agentIDs = in.AgentIDs
	}
	ids, first := agentCols(agentIDs)
	return db.ScanConnector(q.WQueryRow("UPDATE connectors SET label = ?, config = ?, secrets = ?, config_hash = ?, trust = ?, enabled = ?, agent_ids = ?, agent_id = ?, updated_at = ? WHERE id = ? RETURNING "+db.ConnectorCols,
		label, cfg, secretsBlob, hash, trust, db.Bool(enabled), ids, first, now, in.ID))
}

// CleanAgentIDs drops blank and repeated ids, keeping the order.
func CleanAgentIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// agentCols is what the two owner columns hold: agent_ids (NULL when global)
// and agent_id, its first id, for a build from before migration 0023.
func agentCols(ids []string) (db.NullJSON[[]string], *string) {
	ids = CleanAgentIDs(ids)
	if len(ids) == 0 {
		return db.NullJSON[[]string]{}, nil
	}
	return db.SomeJSON(ids), &ids[0]
}

// SetAgents makes a connector global (no ids) or serve only the agents given.
// The runtime reads the row on every turn, so this applies from the next one.
func SetAgents(q db.Q, id string, agentIDs []string, now int64) (db.Connector, bool, error) {
	ids, first := agentCols(agentIDs)
	return db.One(q.WQueryRow("UPDATE connectors SET agent_ids = ?, agent_id = ?, updated_at = ? WHERE id = ? RETURNING "+db.ConnectorCols, ids, first, now, id), db.ScanConnector)
}

// ServesAgent: a global connector serves everyone, a scoped one only the
// agents it lists. A turn with no agent ("") gets globals only.
func ServesAgent(row db.Connector, agentID string) bool {
	return len(row.AgentIDs) == 0 || slices.Contains(row.AgentIDs, agentID)
}

// OnlyAgent: the connector serves this one agent and no other.
func OnlyAgent(row db.Connector, agentID string) bool {
	return len(row.AgentIDs) == 1 && row.AgentIDs[0] == agentID
}

// NoPrivateDataKey is the MCP config key the owner sets on the agent's Tools
// tab to say a server keeps nothing about anyone (PLAN 19.4).
const NoPrivateDataKey = "noPrivateData"

// NoPrivateData: the owner marked this MCP server as holding no private data.
// Only the boolean true on an mcp row counts. A string, a number, false, a
// missing key or any other connector type is unmarked (fail closed).
func NoPrivateData(row db.Connector) bool {
	if row.Type != "mcp" || row.Config == nil {
		return false
	}
	v, _ := row.Config.Get(NoPrivateDataKey)
	return v == true
}

// ResolveData is a tool's data scope on the server. A declared chat, agent,
// shared or none stands as is. Anything else (no scope, or an unknown string)
// is DataNone on an MCP server the owner marked as holding no private data
// (NoPrivateData), and otherwise follows the connector: DataAgent when it
// serves one agent, DataShared when it's global or serves several (they all
// see the same data). Unknown never becomes chat, and becomes none only
// through the owner's mark (fail closed).
func ResolveData(declared string, row db.Connector) string {
	switch declared {
	case DataChat, DataAgent, DataShared, DataNone:
		return declared
	}
	if NoPrivateData(row) {
		return DataNone
	}
	if len(row.AgentIDs) == 1 {
		return DataAgent
	}
	return DataShared
}

// SetNoPrivateData sets (on) or removes (off) the owner's no-private-data mark.
// Like PatchConfig it leaves config_hash alone: the MCP runtime never reads
// the key, so the server isn't restarted, and ResolveData reads the fresh row
// on the next turn. It doesn't check the type; NoPrivateData and the RPC do.
func SetNoPrivateData(q db.Q, id string, on bool, now int64) (db.Connector, bool, error) {
	row, ok, err := Get(q, id)
	if err != nil || !ok {
		return db.Connector{}, ok, err
	}
	config := row.Config.Clone()
	if on {
		config.Set(NoPrivateDataKey, true)
	} else {
		config.Delete(NoPrivateDataKey)
	}
	return db.One(q.WQueryRow("UPDATE connectors SET config = ?, updated_at = ? WHERE id = ? RETURNING "+db.ConnectorCols,
		db.JSON[*jsonx.Object]{V: config}, now, id), db.ScanConnector)
}

func ptr(s string) *string { return &s }

// Connect marks desired = on. The supervisor moves it through connecting -> on|error.
func Connect(q db.Q, id string, now int64) error {
	if _, err := q.Exec("UPDATE connectors SET enabled = 1, status = 'connecting', status_detail = NULL, updated_at = ? WHERE id = ?", now, id); err != nil {
		return err
	}
	_, err := events.Log(q, events.Input{ConnectorID: ptr(id), Kind: "connect", Message: "Connect requested"}, now)
	return err
}

// Pause keeps credentials. Instant, no confirmation. by is "dashboard" by default.
func Pause(q db.Q, id, by string, now int64) error {
	if by == "" {
		by = "dashboard"
	}
	if _, err := q.Exec("UPDATE connectors SET enabled = 0, status = 'paused', status_detail = NULL, error_since = NULL, updated_at = ? WHERE id = ?", now, id); err != nil {
		return err
	}
	_, err := events.Log(q, events.Input{ConnectorID: ptr(id), Kind: "pause", Message: "Paused from " + by}, now)
	return err
}

func Resume(q db.Q, id, by string, now int64) error {
	if by == "" {
		by = "dashboard"
	}
	if _, err := q.Exec("UPDATE connectors SET enabled = 1, status = 'connecting', status_detail = NULL, updated_at = ? WHERE id = ?", now, id); err != nil {
		return err
	}
	_, err := events.Log(q, events.Input{ConnectorID: ptr(id), Kind: "resume", Message: "Resumed from " + by}, now)
	return err
}

// Delete removes credentials and the row. Device unlinking is done before this
// (the supervisor's remove path).
func Delete(q db.Q, id string, now int64) error {
	if _, err := q.Exec("DELETE FROM connectors WHERE id = ?", id); err != nil {
		return err
	}
	_, err := events.Log(q, events.Input{ConnectorID: ptr(id), Kind: "remove", Message: "Removed, credentials deleted", Level: "warn"}, now)
	return err
}

// RetiredTypes are connector types that no longer exist (routines went to flows).
var RetiredTypes = []string{"routines"}

// RemoveRetired deletes rows of retired types when the database opens.
func RemoveRetired(q db.Q, now int64) (int, error) {
	all, err := List(q)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range all {
		if slices.Contains(RetiredTypes, c.Type) {
			if err := Delete(q, c.ID, now); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// SetStatus records a status change. Connector errors can echo a token back (a
// bot URL, an auth header), so the detail is redacted before storing.
func SetStatus(q db.Q, id, status string, rawDetail *string, now int64) error {
	var detail *string
	if rawDetail != nil {
		d := logx.Redact(*rawDetail)
		detail = &d
	}
	row, ok, err := Get(q, id)
	if err != nil || !ok {
		return err
	}
	sameDetail := (row.StatusDetail == nil && detail == nil) || (row.StatusDetail != nil && detail != nil && *row.StatusDetail == *detail)
	if row.Status == status && sameDetail {
		return nil
	}
	var errorSince *int64
	alertedAt := (*int64)(nil)
	if status == "error" {
		errorSince = row.ErrorSince
		if errorSince == nil {
			errorSince = &now
		}
		alertedAt = row.AlertedAt
	}
	if _, err := q.Exec("UPDATE connectors SET status = ?, status_detail = ?, error_since = ?, alerted_at = ?, updated_at = ? WHERE id = ?", status, detail, errorSince, alertedAt, now, id); err != nil {
		return err
	}
	level := "info"
	if status == "error" {
		level = "error"
	}
	message := status
	if detail != nil && *detail != "" {
		message = status + ": " + *detail
	}
	data := jsonx.NewMap[any]()
	data.Set("from", row.Status)
	data.Set("to", status)
	_, err = events.Log(q, events.Input{ConnectorID: ptr(id), Kind: "status", Level: level, Message: message, Data: data}, now)
	return err
}

// PatchSecrets stores runtime-learned secrets (a token swapped for a one-time
// code). Merged like Upsert, but the hash stays so the running handle isn't restarted.
func PatchSecrets(q db.Q, id string, patch *jsonx.Map[string], masterKey string, now int64) error {
	row, ok, err := Get(q, id)
	if err != nil || !ok {
		return err
	}
	current, err := Secrets(row, masterKey)
	if err != nil {
		return err
	}
	blob, err := encryptOrNil(mergeSecrets(current, patch), masterKey)
	if err != nil {
		return err
	}
	_, err = q.Exec("UPDATE connectors SET secrets = ?, updated_at = ? WHERE id = ?", blob, now, id)
	return err
}

// PatchConfig stores runtime-learned config. It doesn't change the hash, so it
// doesn't trigger a restart.
func PatchConfig(q db.Q, id string, patch *jsonx.Object, now int64) error {
	row, ok, err := Get(q, id)
	if err != nil || !ok {
		return err
	}
	config := row.Config.Clone().Assign(patch)
	_, err = q.Exec("UPDATE connectors SET config = ?, updated_at = ? WHERE id = ?", db.JSON[*jsonx.Object]{V: config}, now, id)
	return err
}

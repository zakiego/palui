// Package settings ports packages/core/src/settings.ts, settings-schema.ts and
// models/effort.ts: typed settings with defaults, stored as JSON in the
// `settings` table, and the validation the Settings page and the agent's
// settings tools share.
package settings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// ReasoningEfforts are the values a model chain link may carry.
var ReasoningEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// ModelChainLink is one step of a chain (db.ModelLink, shared with agents.model_roles).
type ModelChainLink = db.ModelLink

type ModelRoles struct {
	Default []ModelChainLink `json:"default"`
	Fast    []ModelChainLink `json:"fast"`
	Vision  []ModelChainLink `json:"vision"`
}

// Role returns the chain for "default", "fast" or "vision".
func (m ModelRoles) Role(role string) []ModelChainLink {
	switch role {
	case "fast":
		return m.Fast
	case "vision":
		return m.Vision
	}
	return m.Default
}

type Budgets struct {
	MaxSteps       int64 `json:"maxSteps"`
	TurnTimeoutMs  int64 `json:"turnTimeoutMs"`
	MaxTools       int64 `json:"maxTools"`
	DailyTokenCap  int64 `json:"dailyTokenCap"`
	MaxRunsPerHour int64 `json:"maxRunsPerHour"`
}

type FirstRun struct {
	SaidHi    bool `json:"saidHi"`
	Dismissed bool `json:"dismissed"`
}

// Outbound is messages to contacts on the owner's behalf.
type Outbound struct {
	// The kill switch: off refuses new sends and cancels the waiting ones.
	Enabled bool `json:"enabled"`
	// Rolling 24 hours, cancelled sends don't count.
	PerContactPerDay int64 `json:"perContactPerDay"`
	PerDay           int64 `json:"perDay"`
	// "Now" sends wait this long so the owner can cancel them.
	HoldSeconds int64  `json:"holdSeconds"`
	SignatureOn bool   `json:"signatureOn"`
	Signature   string `json:"signature"`
	// Texts this signature had before, newest first.
	PastSignatures []string `json:"pastSignatures"`
}

// Settings in the field order of DEFAULT_SETTINGS.
type Settings struct {
	Persona  string `json:"persona"`
	Timezone string `json:"timezone"`
	// "auto", "id" or "en".
	Language   string     `json:"language"`
	UILanguage string     `json:"uiLanguage"`
	ModelRoles ModelRoles `json:"modelRoles"`
	Budgets    Budgets    `json:"budgets"`
	// "telegram", "whatsapp" or "none".
	FallbackChannel               string   `json:"fallbackChannel"`
	ReminderFallbackAfterAttempts int64    `json:"reminderFallbackAfterAttempts"`
	TurnsPausedUntil              *int64   `json:"turnsPausedUntil"`
	TurnsPausedReason             *string  `json:"turnsPausedReason"`
	FirstRun                      FirstRun `json:"firstRun"`
	Outbound                      Outbound `json:"outbound"`
	DeployNoticeChats             []string `json:"deployNoticeChats"`
	LastRunVersion                *string  `json:"lastRunVersion"`
	LastRunCommit                 *string  `json:"lastRunCommit"`
}

// DefaultOutboundSignature: the em dash line and the WhatsApp italics are wanted.
const DefaultOutboundSignature = "\u2014\n_Dikirim otomatis oleh asisten AI Zaki. Balasan ke nomor ini tidak dibaca, silakan hubungi Zaki langsung_"

// LegacyOutboundSignatures are earlier defaults; messages with them count as signed.
var LegacyOutboundSignatures = []string{
	"\u2014\n_Pesan otomatis dari Palui, asisten Zaki. Balasan ke nomor ini tidak dibaca_",
	"\u2014\n_Pesan otomatis dari asisten AI milik Zaki. Balasan ke nomor ini tidak dibaca. Silakan balas ke nomor utama Zaki_",
}

const DefaultPersona = `Kamu Palui, asisten pribadi Zaki. Gayamu santai, singkat, dan to the point, seperti teman yang cerdas.
Default bahasa Indonesia santai. Kalau Zaki menulis dalam bahasa Inggris, jawab dalam bahasa Inggris.
Jangan mengarang fakta. Kalau tidak tahu, bilang tidak tahu. Pakai tools kalau memang membantu.
Kalau sebuah tool tidak disetujui, jangan coba lagi tanpa ditanya.`

// Defaults returns a fresh copy of DEFAULT_SETTINGS.
func Defaults() Settings {
	link := func(c, m string) ModelChainLink { return ModelChainLink{ConnectorID: c, Model: m} }
	return Settings{
		Persona:    DefaultPersona,
		Timezone:   "Asia/Makassar",
		Language:   "auto",
		UILanguage: "en",
		ModelRoles: ModelRoles{
			Default: []ModelChainLink{link("cliproxy", "claude-sonnet-4-6"), link("ai-gateway", "claude-sonnet-4-6")},
			Fast:    []ModelChainLink{link("cliproxy", "claude-haiku-4-5-20251001"), link("ai-gateway", "claude-haiku-4-5-20251001")},
			Vision:  []ModelChainLink{link("cliproxy", "claude-sonnet-4-6"), link("ai-gateway", "claude-sonnet-4-6")},
		},
		Budgets: Budgets{
			MaxSteps: 12,
			// Long enough for a slow model to write and test a flow draft in one turn.
			TurnTimeoutMs:  300_000,
			MaxTools:       64,
			DailyTokenCap:  2_000_000,
			MaxRunsPerHour: 60,
		},
		FallbackChannel:               "telegram",
		ReminderFallbackAfterAttempts: 2,
		FirstRun:                      FirstRun{},
		Outbound:                      Outbound{Enabled: true, PerContactPerDay: 5, PerDay: 20, HoldSeconds: 15, SignatureOn: true, Signature: DefaultOutboundSignature, PastSignatures: []string{}},
		DeployNoticeChats:             []string{},
	}
}

// Keys in DEFAULT_SETTINGS order.
var Keys = []string{"persona", "timezone", "language", "uiLanguage", "modelRoles", "budgets", "fallbackChannel", "reminderFallbackAfterAttempts", "turnsPausedUntil", "turnsPausedReason", "firstRun", "outbound", "deployNoticeChats", "lastRunVersion", "lastRunCommit"}

// fieldFor finds the Settings field whose json name is key.
func fieldFor(s *Settings, key string) (reflect.Value, bool) {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get("json") == key {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// ErrUnknownKey is returned for a key that isn't a setting.
var ErrUnknownKey = errors.New("unknown setting")

func readRow(q db.Q, key string) (db.RawJSON, bool, error) {
	var raw db.RawJSON
	err := q.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// Get reads one setting into T (the type of that Settings field). A missing row
// gives the default. Objects are merged over the default so a new default field
// shows up on old rows.
func Get[T any](q db.Q, key string) (T, error) {
	var out T
	defs := Defaults()
	field, ok := fieldFor(&defs, key)
	if !ok {
		return out, fmt.Errorf("%w %q", ErrUnknownKey, key)
	}
	def, ok := field.Interface().(T)
	if !ok {
		return out, fmt.Errorf("settings: %q is %s, not %T", key, field.Type(), out)
	}
	raw, found, err := readRow(q, key)
	if err != nil || !found {
		return def, err
	}
	isObject := field.Kind() == reflect.Struct
	stored, perr := jsonx.Parse(raw)
	if perr != nil {
		return def, perr
	}
	storedObj, storedIsObject := stored.(*jsonx.Object)
	if isObject && storedIsObject {
		merged := def
		if err := json.Unmarshal(raw, &merged); err != nil {
			return def, err
		}
		if key == "outbound" {
			o := any(&merged).(*Outbound)
			upgradeOutbound(storedObj, o)
		}
		return merged, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return def, err
	}
	return out, nil
}

// Rows saved before the signature switch existed had signature "" for "none
// yet". They get the default signature, which is on; a custom text is kept.
func upgradeOutbound(raw *jsonx.Object, merged *Outbound) {
	if _, ok := raw.Get("signatureOn"); ok {
		return
	}
	merged.SignatureOn = true
	sig := ""
	if v, ok := raw.Get("signature"); ok {
		if s, ok := v.(string); ok {
			sig = strx.Trim(s)
		}
	}
	if sig == "" {
		sig = DefaultOutboundSignature
	}
	merged.Signature = sig
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map:
		return rv.IsNil()
	}
	return false
}

// Set writes one setting. Nil (a nil pointer) drops the row, so Get falls back
// to the default: the value column is NOT NULL and every nullable key defaults to null.
func Set(q db.Q, key string, value any, now int64) error {
	defs := Defaults()
	if _, ok := fieldFor(&defs, key); !ok {
		return fmt.Errorf("%w %q", ErrUnknownKey, key)
	}
	if isNil(value) {
		_, err := q.Exec("DELETE FROM settings WHERE key = ?", key)
		return err
	}
	b, err := jsonx.Marshal(value)
	if err != nil {
		return err
	}
	if string(b) == "null" {
		// A nil slice is an empty list, never a null row.
		b = []byte("[]")
	}
	_, err = q.Exec("INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at", key, string(b), now)
	return err
}

// All reads every setting.
func All(q db.Q) (Settings, error) {
	out := Defaults()
	for _, key := range Keys {
		field, _ := fieldFor(&out, key)
		val, err := getValue(q, key, field.Type())
		if err != nil {
			return out, err
		}
		field.Set(val)
	}
	return out, nil
}

func getValue(q db.Q, key string, t reflect.Type) (reflect.Value, error) {
	var v any
	var err error
	switch t {
	case reflect.TypeOf(""):
		v, err = Get[string](q, key)
	case reflect.TypeOf(int64(0)):
		v, err = Get[int64](q, key)
	case reflect.TypeOf((*int64)(nil)):
		v, err = Get[*int64](q, key)
	case reflect.TypeOf((*string)(nil)):
		v, err = Get[*string](q, key)
	case reflect.TypeOf([]string(nil)):
		v, err = Get[[]string](q, key)
	case reflect.TypeOf(ModelRoles{}):
		v, err = Get[ModelRoles](q, key)
	case reflect.TypeOf(Budgets{}):
		v, err = Get[Budgets](q, key)
	case reflect.TypeOf(FirstRun{}):
		v, err = Get[FirstRun](q, key)
	case reflect.TypeOf(Outbound{}):
		v, err = Get[Outbound](q, key)
	default:
		return reflect.Value{}, fmt.Errorf("settings: no reader for %s", t)
	}
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.ValueOf(v), nil
}

// Typed shortcuts for the keys other packages read most.

func Timezone(q db.Q) (string, error)      { return Get[string](q, "timezone") }
func GetBudgets(q db.Q) (Budgets, error)   { return Get[Budgets](q, "budgets") }
func Persona(q db.Q) (string, error)       { return Get[string](q, "persona") }
func GetOutbound(q db.Q) (Outbound, error) { return Get[Outbound](q, "outbound") }

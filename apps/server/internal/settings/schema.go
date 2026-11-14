package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	_ "time/tzdata"

	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

// MaxSignature: a send_message body is at most 1800 characters and the
// signature at most 150, so body + blank line + signature fits the 2000
// character cap on a message to a contact.
const MaxSignature = 150

// ValidationError is one failed rule, at a dotted path ("budgets.maxSteps").
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

func fail(path, format string, args ...any) error {
	return &ValidationError{Path: path, Message: fmt.Sprintf(format, args...)}
}

// IsTimeZone is isTimeZone: whether Intl.DateTimeFormat accepts tz (any case,
// offset forms like "+08:00" too).
func IsTimeZone(tz string) bool { return zone.Valid(tz) }

// ValidateTimezone is timezoneSchema.
func ValidateTimezone(path, tz string) error {
	n := strx.Len16(tz)
	if n < 1 || n > 64 {
		return fail(path, "Too long or empty")
	}
	if !IsTimeZone(tz) {
		return fail(path, "Use an IANA zone like Asia/Makassar.")
	}
	return nil
}

// ValidateModelChain is modelChainSchema. It trims each model, like the zod
// transform does, and returns the cleaned chain.
func ValidateModelChain(path string, chain []ModelChainLink) ([]ModelChainLink, error) {
	if len(chain) < 1 || len(chain) > 6 {
		return nil, fail(path, "A chain has 1 to 6 models")
	}
	out := make([]ModelChainLink, len(chain))
	for i, l := range chain {
		p := fmt.Sprintf("%s.%d", path, i)
		if n := strx.Len16(l.ConnectorID); n < 1 || n > 64 {
			return nil, fail(p+".connectorId", "Must be 1 to 64 characters")
		}
		model := strx.Trim(l.Model)
		if n := strx.Len16(model); n < 1 || n > 120 {
			return nil, fail(p+".model", "Must be 1 to 120 characters")
		}
		if l.Effort != "" && !slices.Contains(ReasoningEfforts, l.Effort) {
			return nil, fail(p+".effort", "Invalid option")
		}
		out[i] = ModelChainLink{ConnectorID: l.ConnectorID, Model: model, Effort: l.Effort}
	}
	return out, nil
}

func intIn(path string, v, lo, hi int64) error {
	if v < lo || v > hi {
		return fail(path, "Must be between %d and %d", lo, hi)
	}
	return nil
}

// ValidateBudgets is budgetsSchema. A daily token cap of 0 turns the cap off.
func ValidateBudgets(path string, b Budgets) error {
	if err := intIn(path+".maxSteps", b.MaxSteps, 1, 50); err != nil {
		return err
	}
	if err := intIn(path+".turnTimeoutMs", b.TurnTimeoutMs, 10_000, 600_000); err != nil {
		return err
	}
	if err := intIn(path+".maxTools", b.MaxTools, 1, 128); err != nil {
		return err
	}
	if b.DailyTokenCap != 0 {
		if err := intIn(path+".dailyTokenCap", b.DailyTokenCap, 10_000, 100_000_000); err != nil {
			return err
		}
	}
	return intIn(path+".maxRunsPerHour", b.MaxRunsPerHour, 1, 1000)
}

// OutboundInput is outboundSettingsSchema's input (no pastSignatures).
type OutboundInput struct {
	Enabled          bool   `json:"enabled"`
	PerContactPerDay int64  `json:"perContactPerDay"`
	PerDay           int64  `json:"perDay"`
	HoldSeconds      int64  `json:"holdSeconds"`
	SignatureOn      bool   `json:"signatureOn"`
	Signature        string `json:"signature"`
}

// ValidateOutbound is outboundSettingsSchema. The signature's line endings are
// normalized and it is trimmed before the length check.
func ValidateOutbound(path string, o OutboundInput) (OutboundInput, error) {
	if err := intIn(path+".perContactPerDay", o.PerContactPerDay, 1, 50); err != nil {
		return o, err
	}
	if err := intIn(path+".perDay", o.PerDay, 1, 200); err != nil {
		return o, err
	}
	if err := intIn(path+".holdSeconds", o.HoldSeconds, 0, 120); err != nil {
		return o, err
	}
	o.Signature = strx.Trim(strx.NormalizeNewlines(o.Signature))
	if strx.Len16(o.Signature) > MaxSignature {
		return o, fail(path+".signature", "At most %d characters", MaxSignature)
	}
	return o, nil
}

// Patch is settingsPatchSchema: every key optional, objects whole.
type Patch struct {
	Timezone        *string     `json:"timezone,omitempty"`
	Language        *string     `json:"language,omitempty"`
	UILanguage      *string     `json:"uiLanguage,omitempty"`
	ModelRoles      *ModelRoles `json:"modelRoles,omitempty"`
	Budgets         *Budgets    `json:"budgets,omitempty"`
	FallbackChannel *string     `json:"fallbackChannel,omitempty"`
}

func enum(path string, v *string, allowed ...string) error {
	if v != nil && !slices.Contains(allowed, *v) {
		return fail(path, "Invalid option: expected one of %v", allowed)
	}
	return nil
}

// strictInts rejects non-integer numbers in a JSON object of numbers.
func strictInts(raw json.RawMessage, path string, keys ...string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fail(path, "Expected an object")
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			return fail(path+"."+k, "Required")
		}
		var f float64
		if err := json.Unmarshal(v, &f); err != nil {
			return fail(path+"."+k, "Expected a number")
		}
		if f != math.Trunc(f) {
			return fail(path+"."+k, "Expected an integer")
		}
	}
	return nil
}

// ParsePatch decodes and validates a settings patch. Unknown keys are dropped.
func ParsePatch(raw []byte) (Patch, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return Patch{}, fail("", "Expected an object")
	}
	var p Patch
	if v, ok := shape["budgets"]; ok {
		if err := strictInts(v, "budgets", "maxSteps", "turnTimeoutMs", "maxTools", "dailyTokenCap", "maxRunsPerHour"); err != nil {
			return Patch{}, err
		}
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Patch{}, fail("", "%v", err)
	}
	if p.Timezone != nil {
		if err := ValidateTimezone("timezone", *p.Timezone); err != nil {
			return Patch{}, err
		}
	}
	if err := enum("language", p.Language, "auto", "id", "en"); err != nil {
		return Patch{}, err
	}
	if err := enum("uiLanguage", p.UILanguage, "en", "id"); err != nil {
		return Patch{}, err
	}
	if err := enum("fallbackChannel", p.FallbackChannel, "telegram", "whatsapp", "none"); err != nil {
		return Patch{}, err
	}
	if p.ModelRoles != nil {
		var err error
		if p.ModelRoles.Default, err = ValidateModelChain("modelRoles.default", p.ModelRoles.Default); err != nil {
			return Patch{}, err
		}
		if p.ModelRoles.Fast, err = ValidateModelChain("modelRoles.fast", p.ModelRoles.Fast); err != nil {
			return Patch{}, err
		}
		if p.ModelRoles.Vision, err = ValidateModelChain("modelRoles.vision", p.ModelRoles.Vision); err != nil {
			return Patch{}, err
		}
	}
	if p.Budgets != nil {
		if err := ValidateBudgets("budgets", *p.Budgets); err != nil {
			return Patch{}, err
		}
	}
	return p, nil
}

// Package kit holds what every connector package uses to build its tools the
// way the TS connectors build them with the AI SDK's tool() and zod: the input
// JSON schema, the input check a tool call gets, ordered results that print
// like JSON.stringify, and the manifest's config parse.
package kit

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// Exec is a tool's execute. in holds the call's input as given: a turn passes
// zod's output (defaults filled), a flow step the args as written (TS flows
// call execute with the raw args), so a missing key reads as JS undefined.
type Exec func(ctx context.Context, in Input, call connectors.ToolCallInfo) (any, error)

// Tool is tool({description, inputSchema, execute}).
func Tool(description string, schema zod.Schema, exec Exec) connectors.Tool {
	return connectors.Tool{
		Description: description,
		InputSchema: zod.JSONSchemaBytes(schema),
		Parse:       ParseWith(schema),
		Validate: func(raw json.RawMessage) error {
			_, err := zod.ParseJSON(schema, raw)
			return err
		},
		Execute: func(ctx context.Context, raw json.RawMessage, call connectors.ToolCallInfo) (any, error) {
			in, err := Decode(raw)
			if err != nil {
				return nil, err
			}
			return exec(ctx, in, call)
		},
	}
}

// ParseWith is the AI SDK's validation of a call's input against a zod schema.
func ParseWith(schema zod.Schema) func(any) (any, error) {
	return func(v any) (any, error) {
		out, err := zod.Parse(schema, v)
		if err != nil {
			return nil, &zod.TypeValidationError{Value: v, Cause: err}
		}
		return out, nil
	}
}

// Decode reads a tool input. Empty input is {}; anything but an object is an
// empty input too (execute destructures an object).
func Decode(raw json.RawMessage) (Input, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return Input{jsonx.NewMap[any]()}, nil
	}
	v, err := jsonx.Parse(raw)
	if err != nil {
		return Input{}, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		o = jsonx.NewMap[any]()
	}
	return Input{o}, nil
}

// Input is a tool's input object.
type Input struct{ O *jsonx.Object }

// In builds an Input from key/value pairs (tests).
func In(kv ...any) Input { return Input{O(kv...)} }

// Get is the raw value; ok false when the key is missing.
func (in Input) Get(k string) (any, bool) {
	if in.O == nil {
		return nil, false
	}
	v, ok := in.O.Get(k)
	if ok && zod.IsUndefined(v) {
		return nil, false
	}
	return v, ok
}

// Has: the key is present (null counts).
func (in Input) Has(k string) bool { _, ok := in.Get(k); return ok }

// Null: the key is present with null.
func (in Input) Null(k string) bool { v, ok := in.Get(k); return ok && v == nil }

// Str is a string field; ok false when missing or not a string.
func (in Input) Str(k string) (string, bool) {
	v, _ := in.Get(k)
	s, ok := v.(string)
	return s, ok
}

// S is a string field, "" when missing.
func (in Input) S(k string) string { s, _ := in.Str(k); return s }

// Num is a number field.
func (in Input) Num(k string) (float64, bool) {
	v, _ := in.Get(k)
	f, ok := v.(float64)
	return f, ok
}

// Int is a number field as an integer.
func (in Input) Int(k string) (int64, bool) {
	f, ok := in.Num(k)
	return int64(f), ok
}

// Bool is a boolean field.
func (in Input) Bool(k string) (bool, bool) {
	v, _ := in.Get(k)
	b, ok := v.(bool)
	return b, ok
}

// True: the field is the boolean true.
func (in Input) True(k string) bool { b, ok := in.Bool(k); return ok && b }

// Obj is an object field.
func (in Input) Obj(k string) (*jsonx.Object, bool) {
	v, _ := in.Get(k)
	o, ok := v.(*jsonx.Object)
	return o, ok
}

// Strs is an array field of strings (other items skipped).
func (in Input) Strs(k string) ([]string, bool) {
	v, _ := in.Get(k)
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

// --- results -------------------------------------------------------------------------------

// Undef is JS undefined in a result: O drops the key, like JSON.stringify.
var Undef = zod.Undefined

// O builds an ordered result object from key/value pairs, dropping Undef
// values (JSON.stringify drops undefined).
func O(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		if zod.IsUndefined(kv[i+1]) {
			continue
		}
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// If is cond ? v : undefined.
func If(cond bool, v any) any {
	if cond {
		return v
	}
	return Undef
}

// Spread appends another object's keys (the TS ...spread).
func Spread(o *jsonx.Object, more *jsonx.Object) *jsonx.Object {
	if more == nil {
		return o
	}
	for _, k := range more.Keys() {
		v, _ := more.Get(k)
		o.Set(k, v)
	}
	return o
}

// Fail is { ok: false, error }.
func Fail(msg string) *jsonx.Object { return O("ok", false, "error", msg) }

// Plural is "" for one, "s" otherwise.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// StrPtr is a nullable string as a JSON value.
func StrPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// --- manifests -----------------------------------------------------------------------------

// ParseConfig is manifest.config.parse for a zod object schema.
func ParseConfig(schema zod.Schema) func(raw *jsonx.Object) (*jsonx.Object, error) {
	return func(raw *jsonx.Object) (*jsonx.Object, error) {
		var v any = raw
		if raw == nil {
			v = jsonx.NewMap[any]()
		}
		out, err := zod.Parse(schema, v)
		if err != nil {
			return nil, err
		}
		return out.(*jsonx.Object), nil
	}
}

// ParseSecrets checks each secret against its schema; a missing key is
// undefined.
func ParseSecrets(fields []zod.Field) func(raw *jsonx.Map[string]) error {
	return func(raw *jsonx.Map[string]) error {
		in := jsonx.NewMap[any]()
		if raw != nil {
			for _, k := range raw.Keys() {
				v, _ := raw.Get(k)
				in.Set(k, v)
			}
		}
		_, err := zod.Parse(zod.Object(fields...), in)
		return err
	}
}

// SecretNames lists the secret fields.
func SecretNames(fields []zod.Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Key
	}
	return out
}

// ConfigOf parses a connector's stored config with the manifest's schema,
// falling back to the stored object itself when it doesn't parse (the TS
// runtime gets ctx.config as stored).
func ConfigOf(m connectors.Manifest, raw *jsonx.Object) *jsonx.Object {
	if raw == nil {
		raw = jsonx.NewMap[any]()
	}
	if m.ParseConfig == nil {
		return raw
	}
	if out, err := m.ParseConfig(raw); err == nil {
		return out
	}
	return raw
}

// ISO is new Date(ms).toISOString().
func ISO(ms int64) string {
	t := time.UnixMilli(ms).UTC()
	if y := t.Year(); y < 0 || y > 9999 {
		return t.Format("-0000-01-02T15:04:05.000Z")
	}
	return t.Format("2006-01-02T15:04:05.000Z")
}

var isoPartRe = regexp.MustCompile(`^([+-]\d{6}|\d{4})(?:-(\d{2})(?:-(\d{2}))?)?(?:T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|([+-])(\d{2}):?(\d{2}))?)?$`)

// DateParse is Date.parse for the ISO 8601 forms V8 reads: a date only is
// UTC, a time without an offset is the process zone (time.Local), an offset
// may drop its colon, and a day up to 31 rolls into the next month like V8
// ("2026-02-30" is March 2). ok false is NaN. V8's legacy formats
// ("Nov 10 2026") are not read.
func DateParse(s string) (int64, bool) {
	m := isoPartRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n := func(x string, def int) int {
		if x == "" {
			return def
		}
		v, _ := strconv.Atoi(x)
		return v
	}
	y := n(m[1], 0)
	mo, d := n(m[2], 1), n(m[3], 1)
	h, mi, sec := n(m[4], 0), n(m[5], 0), n(m[6], 0)
	ms := 0
	if m[7] != "" {
		ms = n((m[7] + "00")[:3], 0)
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return 0, false
	}
	if mi > 59 || sec > 59 || h > 24 || (h == 24 && (mi != 0 || sec != 0 || ms != 0)) {
		return 0, false
	}
	loc := time.UTC
	if m[4] != "" && m[8] == "" {
		loc = time.Local
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, ms*1e6, loc).UnixMilli()
	if m[8] != "" && m[8] != "Z" {
		oh, om := n(m[10], 0), n(m[11], 0)
		if oh > 23 || om > 59 {
			return 0, false
		}
		off := int64(oh*60+om) * 60_000
		if m[9] == "+" {
			t -= off
		} else {
			t += off
		}
	}
	return t, true
}

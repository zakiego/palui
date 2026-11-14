package flows

// A small port of the parts of zod 4.6.7 the flow schema uses (schema.ts and
// source-config.ts), so the Go validator accepts, rejects and fills defaults
// exactly like definitionSchema.safeParse: the same issues in the same order
// with zod's English messages, and an output whose objects list their keys in
// shape order (that is what flow_versions.definition stores).
//
// Values are jsonx values (nil, bool, float64, string, []any, *jsonx.Object);
// `undef` stands for a missing key (JS undefined).

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

type undefT struct{}

// undef is JS undefined: a key the input doesn't have.
var undef any = undefT{}

func isUndef(v any) bool { _, ok := v.(undefT); return ok }

// cont is an issue's `continue` flag: true, unset (aborts implicitly) or false
// (aborts explicitly, which also stops checks that have a `when`).
type cont int

const (
	contUnset cont = iota
	contTrue
	contFalse
)

type zIssue struct {
	code    string
	path    []any // string keys and int indices
	message string
	cont    cont
	// invalid_key: the key schema's issues.
	inner []zIssue
}

func aborted(issues []zIssue, from int) bool {
	for _, i := range issues[from:] {
		if i.cont != contTrue {
			return true
		}
	}
	return false
}

func explicitlyAborted(issues []zIssue) bool {
	for _, i := range issues {
		if i.cont == contFalse {
			return true
		}
	}
	return false
}

func prefix(key any, issues []zIssue) []zIssue {
	for i := range issues {
		issues[i].path = append([]any{key}, issues[i].path...)
	}
	return issues
}

// parsedType is util.parsedType for jsonx values.
func parsedType(v any) string {
	switch v.(type) {
	case undefT:
		return "undefined"
	case nil:
		return "null"
	case []any:
		return "array"
	case *jsonx.Object:
		return "object"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	}
	return "object"
}

func typeIssue(expected string, v any) zIssue {
	return zIssue{code: "invalid_type", message: fmt.Sprintf("Invalid input: expected %s, received %s", expected, parsedType(v))}
}

func stringifyPrimitive(v any) string {
	switch t := v.(type) {
	case string:
		return `"` + t + `"`
	case float64:
		return jsonx.NumberString(t)
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// --- checks ----------------------------------------------------------------------------

type zCheck struct {
	// when: run even after an implicit abort, if this says so (length checks).
	when func(v any) bool
	// run returns the issue to add, or nil. Overwrite checks change *v.
	run func(v *any) *zIssue
}

func hasLength(v any) bool {
	switch v.(type) {
	case string, []any:
		return true
	}
	return false
}

func lengthOrigin(v any) string {
	switch v.(type) {
	case []any:
		return "array"
	case string:
		return "string"
	}
	return "unknown"
}

// lengthOf is zod's length: strings in code points, arrays in items.
func lengthOf(v any) int {
	switch t := v.(type) {
	case string:
		return utf8.RuneCountInString(t)
	case []any:
		return len(t)
	}
	return 0
}

func sizing(origin string) (unit string, ok bool) {
	switch origin {
	case "string":
		return "characters", true
	case "array":
		return "items", true
	}
	return "", false
}

func minLen(n int, msg string) zCheck {
	return zCheck{when: hasLength, run: func(v *any) *zIssue {
		if lengthOf(*v) >= n {
			return nil
		}
		o := lengthOrigin(*v)
		m := msg
		if m == "" {
			if unit, ok := sizing(o); ok {
				m = fmt.Sprintf("Too small: expected %s to have >=%d %s", o, n, unit)
			} else {
				m = fmt.Sprintf("Too small: expected %s to be >=%d", o, n)
			}
		}
		return &zIssue{code: "too_small", message: m, cont: contTrue}
	}}
}

func maxLen(n int, msg string) zCheck {
	return zCheck{when: hasLength, run: func(v *any) *zIssue {
		if lengthOf(*v) <= n {
			return nil
		}
		o := lengthOrigin(*v)
		m := msg
		if m == "" {
			if unit, ok := sizing(o); ok {
				m = fmt.Sprintf("Too big: expected %s to have <=%d %s", o, n, unit)
			} else {
				m = fmt.Sprintf("Too big: expected %s to be <=%d", o, n)
			}
		}
		return &zIssue{code: "too_big", message: m, cont: contTrue}
	}}
}

func trimCheck() zCheck {
	return zCheck{run: func(v *any) *zIssue {
		if s, ok := (*v).(string); ok {
			*v = strx.Trim(s)
		}
		return nil
	}}
}

// regexCheck: pattern is the JS source between the slashes, used for the
// default message ("must match pattern /.../").
func regexCheck(re *regexp.Regexp, jsSource, msg string) zCheck {
	return zCheck{run: func(v *any) *zIssue {
		s, _ := (*v).(string)
		if re.MatchString(s) {
			return nil
		}
		m := msg
		if m == "" {
			m = "Invalid string: must match pattern /" + jsSource + "/"
		}
		return &zIssue{code: "invalid_format", message: m, cont: contTrue}
	}}
}

const maxSafeInt = 9007199254740991

func intCheck() zCheck {
	return zCheck{run: func(v *any) *zIssue {
		f, _ := (*v).(float64)
		if f != math.Trunc(f) {
			return &zIssue{code: "invalid_type", message: "Invalid input: expected int, received number", cont: contFalse}
		}
		if f > maxSafeInt {
			return &zIssue{code: "too_big", message: fmt.Sprintf("Too big: expected int to be <=%d", int64(maxSafeInt)), cont: contTrue}
		}
		if f < -maxSafeInt {
			return &zIssue{code: "too_small", message: fmt.Sprintf("Too small: expected int to be >=%d", int64(-maxSafeInt)), cont: contTrue}
		}
		return nil
	}}
}

func gte(n float64) zCheck {
	return zCheck{run: func(v *any) *zIssue {
		if f, _ := (*v).(float64); f >= n {
			return nil
		}
		return &zIssue{code: "too_small", message: "Too small: expected number to be >=" + jsonx.NumberString(n), cont: contTrue}
	}}
}

func lte(n float64) zCheck {
	return zCheck{run: func(v *any) *zIssue {
		if f, _ := (*v).(float64); f <= n {
			return nil
		}
		return &zIssue{code: "too_big", message: "Too big: expected number to be <=" + jsonx.NumberString(n), cont: contTrue}
	}}
}

func refineCheck(fn func(v any) bool, msg string) zCheck {
	return zCheck{run: func(v *any) *zIssue {
		if fn(*v) {
			return nil
		}
		return &zIssue{code: "custom", message: msg, cont: contTrue}
	}}
}

// runChecks is $ZodType's runChecks.
func runChecks(v any, issues []zIssue, checks []zCheck) (any, []zIssue) {
	isAborted := aborted(issues, 0)
	for _, ch := range checks {
		if ch.when != nil {
			if explicitlyAborted(issues) {
				continue
			}
			if !ch.when(v) {
				continue
			}
		} else if isAborted {
			continue
		}
		before := len(issues)
		if is := ch.run(&v); is != nil {
			issues = append(issues, *is)
		}
		if len(issues) != before && !isAborted {
			isAborted = aborted(issues, before)
		}
	}
	return v, issues
}

// --- schemas ---------------------------------------------------------------------------

type zSchema interface {
	parse(v any) (any, []zIssue)
	optin() string
	optout() string
}

type base struct{ checks []zCheck }

func (base) optin() string  { return "" }
func (base) optout() string { return "" }

type zString struct {
	base
}

func (s *zString) parse(v any) (any, []zIssue) {
	var issues []zIssue
	if _, ok := v.(string); !ok {
		issues = append(issues, typeIssue("string", v))
	}
	return runChecks(v, issues, s.checks)
}

func (s *zString) trim() *zString { s.checks = append(s.checks, trimCheck()); return s }
func (s *zString) min(n int, msg ...string) *zString {
	s.checks = append(s.checks, minLen(n, first(msg)))
	return s
}
func (s *zString) max(n int, msg ...string) *zString {
	s.checks = append(s.checks, maxLen(n, first(msg)))
	return s
}
func (s *zString) regex(re *regexp.Regexp, jsSource string, msg ...string) *zString {
	s.checks = append(s.checks, regexCheck(re, jsSource, first(msg)))
	return s
}

func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func zstr() *zString { return &zString{} }

type zNumber struct{ base }

func (s *zNumber) parse(v any) (any, []zIssue) {
	var issues []zIssue
	if _, ok := v.(float64); !ok {
		issues = append(issues, typeIssue("number", v))
	}
	return runChecks(v, issues, s.checks)
}

func (s *zNumber) int() *zNumber          { s.checks = append(s.checks, intCheck()); return s }
func (s *zNumber) min(n float64) *zNumber { s.checks = append(s.checks, gte(n)); return s }
func (s *zNumber) max(n float64) *zNumber { s.checks = append(s.checks, lte(n)); return s }

func znum() *zNumber { return &zNumber{} }

// zint is z.number().int().min(lo).max(hi).
func zint(lo, hi float64) *zNumber { return znum().int().min(lo).max(hi) }

type zBool struct{ base }

func (s *zBool) parse(v any) (any, []zIssue) {
	var issues []zIssue
	if _, ok := v.(bool); !ok {
		issues = append(issues, typeIssue("boolean", v))
	}
	return runChecks(v, issues, s.checks)
}

type zUnknown struct{ base }

func (s *zUnknown) parse(v any) (any, []zIssue) { return v, nil }

// zLiteral and zEnum: invalid_value.
type zLiteral struct {
	base
	values []any
}

func (s *zLiteral) parse(v any) (any, []zIssue) {
	for _, x := range s.values {
		if x == v {
			return v, nil
		}
	}
	var msg string
	if len(s.values) == 1 {
		msg = "Invalid input: expected " + stringifyPrimitive(s.values[0])
	} else {
		parts := make([]string, len(s.values))
		for i, x := range s.values {
			parts[i] = stringifyPrimitive(x)
		}
		msg = "Invalid option: expected one of " + strings.Join(parts, "|")
	}
	return v, []zIssue{{code: "invalid_value", message: msg}}
}

func zlit(v any) *zLiteral { return &zLiteral{values: []any{v}} }

func zenum(vals ...string) *zLiteral {
	out := &zLiteral{}
	for _, v := range vals {
		out.values = append(out.values, v)
	}
	return out
}

type zOptional struct {
	base
	inner zSchema
}

func (s *zOptional) optin() string {
	if s.inner.optin() == "defaulted" {
		return "defaulted"
	}
	return "optional"
}
func (s *zOptional) optout() string { return "optional" }
func (s *zOptional) parse(v any) (any, []zIssue) {
	if isUndef(v) {
		if s.inner.optin() != "defaulted" {
			return v, nil
		}
	}
	return s.inner.parse(v)
}

func opt(inner zSchema) *zOptional { return &zOptional{inner: inner} }

type zNullable struct {
	base
	inner zSchema
}

func (s *zNullable) optin() string  { return s.inner.optin() }
func (s *zNullable) optout() string { return s.inner.optout() }
func (s *zNullable) parse(v any) (any, []zIssue) {
	if v == nil {
		return nil, nil
	}
	return s.inner.parse(v)
}

func nullable(inner zSchema) *zNullable { return &zNullable{inner: inner} }

// zDefault: an undefined input becomes the default, which isn't validated
// (zod returns it as is). checks are refines added after .default().
type zDefault struct {
	base
	inner zSchema
	def   func() any
}

func (s *zDefault) optin() string { return "defaulted" }
func (s *zDefault) parse(v any) (any, []zIssue) {
	var issues []zIssue
	if isUndef(v) {
		v = s.def()
	} else {
		v, issues = s.inner.parse(v)
		if isUndef(v) {
			v = s.def()
		}
	}
	return runChecks(v, issues, s.checks)
}

func withDefault(inner zSchema, def func() any) *zDefault { return &zDefault{inner: inner, def: def} }

func (s *zDefault) refine(fn func(v any) bool, msg string) *zDefault {
	s.checks = append(s.checks, refineCheck(fn, msg))
	return s
}

type zField struct {
	key    string
	schema zSchema
}

type zObject struct {
	base
	fields []zField
}

func zobj(fields ...zField) *zObject { return &zObject{fields: fields} }

func f(key string, s zSchema) zField { return zField{key, s} }

func (o *zObject) extend(fields ...zField) *zObject {
	return &zObject{fields: append(append([]zField{}, o.fields...), fields...)}
}

func (o *zObject) field(key string) zSchema {
	for _, x := range o.fields {
		if x.key == key {
			return x.schema
		}
	}
	return nil
}

// parse is $ZodObjectJIT's fast path: keys in shape order, unknown keys dropped.
func (o *zObject) parse(v any) (any, []zIssue) {
	in, ok := v.(*jsonx.Object)
	if !ok {
		return runChecks(v, []zIssue{typeIssue("object", v)}, o.checks)
	}
	out := jsonx.NewMap[any]()
	var issues []zIssue
	for _, fl := range o.fields {
		raw, present := in.Get(fl.key)
		if !present {
			raw = undef
		}
		val, is := fl.schema.parse(raw)
		optin, optout := fl.schema.optin(), fl.schema.optout()
		switch {
		case optin != "" && optout == "optional":
			if len(is) == 0 || present {
				issues = append(issues, prefix(fl.key, is)...)
				assign := present
				if optin != "optional" {
					assign = !isUndef(val) || present
				}
				if assign {
					out.Set(fl.key, val)
				}
			}
		case optin == "":
			issues = append(issues, prefix(fl.key, is)...)
			if !present && len(is) == 0 {
				issues = append(issues, zIssue{code: "invalid_type", path: []any{fl.key}, message: "Invalid input: expected nonoptional, received undefined"})
			}
			if present {
				out.Set(fl.key, val)
			}
		default:
			issues = append(issues, prefix(fl.key, is)...)
			if optin == "defaulted" {
				out.Set(fl.key, val)
			} else if !isUndef(val) || present {
				out.Set(fl.key, val)
			}
		}
	}
	return runChecks(out, issues, o.checks)
}

type zArray struct {
	base
	elem zSchema
}

func zarr(elem zSchema) *zArray { return &zArray{elem: elem} }

func (a *zArray) min(n int) *zArray { a.checks = append(a.checks, minLen(n, "")); return a }
func (a *zArray) max(n int) *zArray { a.checks = append(a.checks, maxLen(n, "")); return a }

func (a *zArray) parse(v any) (any, []zIssue) {
	in, ok := v.([]any)
	if !ok {
		return runChecks(v, []zIssue{typeIssue("array", v)}, a.checks)
	}
	out := make([]any, len(in))
	var issues []zIssue
	for i, x := range in {
		val, is := a.elem.parse(x)
		issues = append(issues, prefix(i, is)...)
		if isUndef(val) {
			val = nil
		}
		out[i] = val
	}
	return runChecks(out, issues, a.checks)
}

type zRecord struct {
	base
	key, value zSchema
}

func zrecord(key, value zSchema) *zRecord { return &zRecord{key: key, value: value} }

func (r *zRecord) refine(fn func(v any) bool, msg string) *zRecord {
	r.checks = append(r.checks, refineCheck(fn, msg))
	return r
}

func (r *zRecord) parse(v any) (any, []zIssue) {
	in, ok := v.(*jsonx.Object)
	if !ok {
		return runChecks(v, []zIssue{typeIssue("record", v)}, r.checks)
	}
	out := jsonx.NewMap[any]()
	var issues []zIssue
	for _, k := range in.Keys() {
		if k == "__proto__" {
			continue
		}
		kv, kis := r.key.parse(k)
		if len(kis) > 0 {
			issues = append(issues, zIssue{code: "invalid_key", path: []any{k}, message: "Invalid key in record", inner: kis})
			continue
		}
		raw, _ := in.Get(k)
		val, is := r.value.parse(raw)
		issues = append(issues, prefix(k, is)...)
		out.Set(kv.(string), val)
	}
	return runChecks(out, issues, r.checks)
}

type zUnion struct {
	base
	options []zSchema
}

func zunion(opts ...zSchema) *zUnion { return &zUnion{options: opts} }

func (u *zUnion) optin() string {
	for _, o := range u.options {
		if o.optin() == "defaulted" {
			return "defaulted"
		}
	}
	for _, o := range u.options {
		if o.optin() != "" {
			return "optional"
		}
	}
	return ""
}

func (u *zUnion) optout() string {
	for _, o := range u.options {
		if o.optout() == "optional" {
			return "optional"
		}
	}
	return ""
}

func (u *zUnion) parse(v any) (any, []zIssue) {
	type res struct {
		v      any
		issues []zIssue
	}
	var results []res
	for _, o := range u.options {
		val, is := o.parse(v)
		if len(is) == 0 {
			return runChecks(val, nil, u.checks)
		}
		results = append(results, res{val, is})
	}
	var nonAborted []res
	for _, r := range results {
		if !aborted(r.issues, 0) {
			nonAborted = append(nonAborted, r)
		}
	}
	if len(nonAborted) == 1 {
		return runChecks(nonAborted[0].v, nonAborted[0].issues, u.checks)
	}
	return runChecks(v, []zIssue{{code: "invalid_union", message: "Invalid input"}}, u.checks)
}

type zDisc struct {
	base
	key     string
	options []*zObject
}

func zdisc(key string, opts ...*zObject) *zDisc { return &zDisc{key: key, options: opts} }

func (d *zDisc) parse(v any) (any, []zIssue) {
	in, ok := v.(*jsonx.Object)
	if !ok {
		return runChecks(v, []zIssue{typeIssue("object", v)}, d.checks)
	}
	tag, _ := in.Get(d.key)
	var names []string
	for _, o := range d.options {
		lit := o.field(d.key).(*zLiteral)
		for _, x := range lit.values {
			if x == tag {
				return o.parse(v)
			}
			names = append(names, "'"+fmt.Sprint(x)+"'")
		}
	}
	return runChecks(v, []zIssue{{code: "invalid_union", path: []any{d.key}, message: "Invalid discriminator value. Expected " + strings.Join(names, " | ")}}, d.checks)
}

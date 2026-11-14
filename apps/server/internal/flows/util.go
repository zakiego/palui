package flows

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// obj builds an ordered object from key, value pairs (a TS object literal).
// An undef value leaves the key out, like JSON.stringify drops undefined.
func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		if isUndef(kv[i+1]) {
			continue
		}
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// spread is { ...a, ...b }.
func spread(objs ...*jsonx.Object) *jsonx.Object {
	out := jsonx.NewMap[any]()
	for _, o := range objs {
		out.Assign(o)
	}
	return out
}

func mathFloor(f float64) float64 { return math.Floor(f) }

// jsLower is String.prototype.toLowerCase for the cases Go's rules differ on:
// a dotted capital I and a final sigma.
func jsLower(s string) string {
	if !strings.ContainsAny(s, "İΣ") {
		return strings.ToLower(s)
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		switch r {
		case 'İ':
			b.WriteString("i̇")
		case 'Σ':
			// Final when a letter comes before and none after.
			before := i > 0 && unicode.IsLetter(runes[i-1])
			after := i+1 < len(runes) && unicode.IsLetter(runes[i+1])
			if before && !after {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteString(strings.ToLower(string(r)))
		}
	}
	return b.String()
}

// jsUpper is String.prototype.toUpperCase for the cases Go's rules differ on
// (ß becomes SS).
func jsUpper(s string) string {
	if !strings.ContainsRune(s, 'ß') {
		return strings.ToUpper(s)
	}
	return strings.ToUpper(strings.ReplaceAll(s, "ß", "SS"))
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strx.Trim(s) }

var wsRun = regexp.MustCompile(`[` + strx.SpaceClass + `]+`)

// oneLine is .replace(/\s+/g, " ").
func oneLine(s string) string { return wsRun.ReplaceAllString(s, " ") }

// errorText is errorText: the message on one line, at most 300 units.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strx.Slice16(oneLine(err.Error()), 300)
}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// clip16 is `${s.slice(0, n-1)}…` when s is longer than n.
func ellipsis(s string, n int) string {
	if strx.Len16(s) > n {
		return strx.Slice16(s, n-1) + "…"
	}
	return s
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// jsonQuote is JSON.stringify of a string.
func jsonQuote(s string) string { return jsonx.Stringify(s) }

// objGet reads a key of an object value; nil when v isn't an object or lacks it.
func objGet(v any, k string) any {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	x, _ := o.Get(k)
	return x
}

func objStr(v any, k string) string {
	s, _ := objGet(v, k).(string)
	return s
}

// objNum reads a number key; ok false when missing or not a number.
func objNum(v any, k string) (float64, bool) {
	n, ok := objGet(v, k).(float64)
	return n, ok
}

func asObject(v any) *jsonx.Object {
	o, _ := v.(*jsonx.Object)
	return o
}

// cloneObj is a shallow copy (never nil).
func cloneObj(o *jsonx.Object) *jsonx.Object {
	if o == nil {
		return jsonx.NewMap[any]()
	}
	return o.Clone()
}

func i64(f float64) int64 { return int64(f) }

func minInt64(xs ...*int64) *int64 {
	var out *int64
	for _, x := range xs {
		if x != nil && (out == nil || *x < *out) {
			v := *x
			out = &v
		}
	}
	return out
}

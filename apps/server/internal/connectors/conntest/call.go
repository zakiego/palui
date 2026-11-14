package conntest

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Value is a tool result as JSON (what JSON.stringify gives the model), with
// dotted-path accessors for assertions: "attachment.url", "files.0.nomor".
type Value struct {
	V   any
	Err error
}

// Of normalizes any value to JSON.
func Of(v any) Value {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return Value{Err: err}
	}
	p, err := jsonx.Parse(b)
	if err != nil {
		return Value{Err: err}
	}
	return Value{V: p}
}

// Call runs one tool of a set with a JSON input (execute, as the tests call
// it: no schema parse, like t.tool.execute(input)).
func Call(t testing.TB, set *connectors.ToolSet, name, input string) Value {
	t.Helper()
	pt, ok := set.Get(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	return Exec(t, pt.Tool, input)
}

// Exec runs a tool's execute with a JSON input.
func Exec(t testing.TB, tool connectors.Tool, input string) Value {
	t.Helper()
	if tool.Execute == nil {
		t.Fatal("tool has no execute")
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(input), connectors.ToolCallInfo{ToolCallID: "tc_1"})
	if err != nil {
		return Value{Err: err}
	}
	return Of(out)
}

// Get walks a dotted path (numbers index arrays); nil when missing.
func (v Value) Get(path string) any {
	cur := v.V
	if path == "" {
		return cur
	}
	for _, p := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case *jsonx.Object:
			x, ok := c.Get(p)
			if !ok {
				return nil
			}
			cur = x
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(c) {
				return nil
			}
			cur = c[i]
		default:
			return nil
		}
	}
	return cur
}

// Has reports whether the path exists.
func (v Value) Has(path string) bool {
	i := strings.LastIndex(path, ".")
	parent, key := v.V, path
	if i >= 0 {
		parent, key = v.Get(path[:i]), path[i+1:]
	}
	switch c := parent.(type) {
	case *jsonx.Object:
		_, ok := c.Get(key)
		return ok
	case []any:
		n, err := strconv.Atoi(key)
		return err == nil && n >= 0 && n < len(c)
	}
	return false
}

func (v Value) S(path string) string    { s, _ := v.Get(path).(string); return s }
func (v Value) B(path string) bool      { b, _ := v.Get(path).(bool); return b }
func (v Value) N(path string) float64   { f, _ := v.Get(path).(float64); return f }
func (v Value) List(path string) []any  { l, _ := v.Get(path).([]any); return l }
func (v Value) Len(path string) int     { return len(v.List(path)) }
func (v Value) JSON(path string) string { return jsonx.Stringify(v.Get(path)) }
func (v Value) String() string          { return jsonx.Stringify(v.V) }

// Pluck is list.map(x => x[key]).
func (v Value) Pluck(path, key string) []any {
	var out []any
	for _, x := range v.List(path) {
		out = append(out, Value{V: x}.Get(key))
	}
	return out
}

// Strs is Pluck as strings.
func (v Value) Strs(path, key string) []string {
	var out []string
	for _, x := range v.Pluck(path, key) {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// Classes maps tool names to classes, in order, as "name:class".
func Classes(set *connectors.ToolSet) []string {
	var out []string
	for _, k := range set.Keys() {
		pt, _ := set.Get(k)
		out = append(out, k+":"+pt.Class)
	}
	return out
}

package flows

// Taint tracking for flows (taint.ts, RFC flows 4.3). A run keeps the set of
// root keys whose values came from untrusted data: "trigger", "steps.<id>",
// "vars.<name>", "flow.<name>", "item". Taint only changes what needs approval
// and how text is shown, never which branches exist.

import (
	"sort"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Taint is a sorted set of root keys.
type Taint = []string

var cleanRoots = map[string]bool{"now": true, "run": true, "index": true}

// KeyTainted is keyTainted.
func KeyTainted(taint Taint, key string) bool {
	if cleanRoots[key] {
		return false
	}
	if containsStr(taint, key) {
		return true
	}
	// A dynamic read ("steps" with a computed first segment) is tainted when any of its kind is.
	if key == "steps" || key == "vars" || key == "flow" {
		for _, t := range taint {
			if strings.HasPrefix(t, key+".") {
				return true
			}
		}
	}
	return false
}

func PathsTainted(taint Taint, paths []*Node) bool {
	for _, p := range paths {
		if KeyTainted(taint, RootKey(p)) {
			return true
		}
	}
	return false
}

func ExprTainted(taint Taint, ast *Node) bool { return PathsTainted(taint, PathsOf(ast, nil)) }

// ValueTainted is valueTainted: any template string inside v reads a tainted
// root. A template that doesn't parse reads nothing here (the TS threw; saved
// definitions always parse).
func ValueTainted(taint Taint, v any) bool {
	for _, t := range TemplatesIn(v, 0) {
		paths, err := TemplatePaths(t)
		if err != nil {
			panic(err)
		}
		if PathsTainted(taint, paths) {
			return true
		}
	}
	return false
}

// TaintedArgs is taintedArgs: the top-level arg names that read a tainted root.
func TaintedArgs(taint Taint, args *jsonx.Object) []string {
	out := []string{}
	for _, k := range args.Keys() {
		v, _ := args.Get(k)
		if ValueTainted(taint, v) {
			out = append(out, k)
		}
	}
	return out
}

// AddTaint is addTaint: the union, sorted (JS sort, UTF-16 order).
func AddTaint(taint Taint, keys ...string) Taint {
	seen := map[string]bool{}
	var out []string
	for _, k := range append(append([]string{}, taint...), keys...) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strx.Compare16(out[i], out[j]) < 0 })
	if out == nil {
		out = []string{}
	}
	return out
}

func RemoveTaint(taint Taint, key string) Taint {
	out := []string{}
	for _, t := range taint {
		if t != key {
			out = append(out, t)
		}
	}
	return out
}

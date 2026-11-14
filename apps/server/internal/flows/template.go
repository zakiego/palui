package flows

// Flow templates (template.ts, RFC flows 4.2): "{{ path }}" or
// "{{ path | filter | filter(arg) }}". A value that is exactly one template
// keeps its type; a template inside a longer string is stringified. Message
// mode sanitizes every interpolated value before any filter runs; a tainted
// value also loses its line breaks and is capped at 500 characters.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// FilterNames keeps the TS key order of FILTERS.
var FilterNames = []string{"time", "date", "json", "default", "join", "lines", "upper", "lower", "round", "truncate"}

var filters = map[string]arity{
	"time": {0, 0}, "date": {0, 0}, "json": {0, 0}, "default": {1, 1}, "join": {0, 1},
	"lines": {0, 1}, "upper": {0, 0}, "lower": {0, 0}, "round": {0, 1}, "truncate": {1, 1},
}

const (
	MaxValueChars    = 500
	MaxLines         = 20
	MaxRenderedChars = 4000
)

// TemplateError is a template that doesn't parse.
type TemplateError struct{ Msg string }

func (e *TemplateError) Error() string { return e.Msg }

func tplErr(format string, args ...any) *TemplateError {
	return &TemplateError{Msg: fmt.Sprintf(format, args...)}
}

// Filter is one filter with its arguments (string or float64).
type Filter struct {
	Name string
	Args []any
}

// TemplatePart is text, or a template with its path and filters.
type TemplatePart struct {
	T       string // text or tpl
	V       string
	Path    *Node
	Filters []Filter
	Src     string
}

// HasTemplate is hasTemplate.
func HasTemplate(v any) bool {
	s, ok := v.(string)
	return ok && strings.Contains(s, "{{")
}

func splitPipes(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	depth := 0
	for _, ch := range s {
		if quote != 0 {
			cur.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch {
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '[' || ch == '(':
			depth++
		case ch == ']' || ch == ')':
			depth--
		case ch == '|' && depth == 0:
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(ch)
	}
	out = append(out, cur.String())
	for i := range out {
		out[i] = strx.Trim(out[i])
	}
	return out
}

var (
	filterRe = regexp.MustCompile(`(?s)^([A-Za-z_][A-Za-z0-9_]*)[` + strx.SpaceClass + `]*(?:\((.*)\))?$`)
	argRe    = regexp.MustCompile(`^[` + strx.SpaceClass + `]*(?:"((?:[^"\\]|\\[^\n\r\x{2028}\x{2029}])*)"|'((?:[^'\\]|\\[^\n\r\x{2028}\x{2029}])*)'|(-?\d+(?:\.\d+)?))[` + strx.SpaceClass + `]*(,|$)`)
	escRe    = regexp.MustCompile(`\\([^\n\r\x{2028}\x{2029}])`)
)

func parseFilter(src string) (Filter, error) {
	m := filterRe.FindStringSubmatch(src)
	names := strings.Join(FilterNames, ", ")
	if m == nil {
		return Filter{}, tplErr(`"%s" isn't a filter. Filters: %s.`, src, names)
	}
	name := m[1]
	ar, ok := filters[name]
	if !ok {
		return Filter{}, tplErr(`Unknown filter "%s". Filters: %s.`, name, names)
	}
	args := []any{}
	raw := strx.Trim(m[2])
	at := 0
	for at < len(raw) {
		a := argRe.FindStringSubmatchIndex(raw[at:])
		if a == nil {
			return Filter{}, tplErr("Filter %s: arguments must be quoted strings or numbers.", name)
		}
		sub := func(i int) (string, bool) {
			if a[2*i] < 0 {
				return "", false
			}
			return raw[at+a[2*i] : at+a[2*i+1]], true
		}
		if num, ok := sub(3); ok {
			n, _ := strconv.ParseFloat(num, 64)
			args = append(args, n)
		} else {
			s, ok := sub(1)
			if !ok {
				s, _ = sub(2)
			}
			args = append(args, escRe.ReplaceAllStringFunc(s, func(x string) string {
				c := x[1:]
				if c == "n" {
					return "\n"
				}
				return c
			}))
		}
		sep, _ := sub(4)
		at += a[1]
		if sep == "" {
			break
		}
	}
	if len(args) < ar.min || len(args) > ar.max {
		count := strconv.Itoa(ar.min)
		if ar.min != ar.max {
			count = fmt.Sprintf("%d to %d", ar.min, ar.max)
		}
		plural := "s"
		if ar.max == 1 {
			plural = ""
		}
		return Filter{}, tplErr("Filter %s takes %s argument%s.", name, count, plural)
	}
	return Filter{Name: name, Args: args}, nil
}

// ParseTemplate is parseTemplate.
func ParseTemplate(s string) ([]TemplatePart, error) {
	var parts []TemplatePart
	i := 0
	for i < len(s) {
		rel := strings.Index(s[i:], "{{")
		if rel < 0 {
			parts = append(parts, TemplatePart{T: "text", V: s[i:]})
			break
		}
		open := i + rel
		if open > i {
			parts = append(parts, TemplatePart{T: "text", V: s[i:open]})
		}
		at := strx.Len16(s[:open]) + 1
		crel := strings.Index(s[open+2:], "}}")
		if crel < 0 {
			return nil, tplErr(`A "{{" at character %d has no closing "}}".`, at)
		}
		closeAt := open + 2 + crel
		inner := strx.Trim(s[open+2 : closeAt])
		if inner == "" {
			return nil, tplErr("Empty template at character %d.", at)
		}
		pieces := splitPipes(inner)
		path, err := ParsePath(pieces[0])
		if err != nil {
			return nil, tplErr("{{%s}}: %s", inner, err.Error())
		}
		var fs []Filter
		for _, fsrc := range pieces[1:] {
			fl, err := parseFilter(fsrc)
			if err != nil {
				return nil, err
			}
			fs = append(fs, fl)
		}
		parts = append(parts, TemplatePart{T: "tpl", Path: path, Filters: fs, Src: inner})
		i = closeAt + 2
	}
	return parts, nil
}

// TemplatePaths is templatePaths.
func TemplatePaths(s string) ([]*Node, error) {
	parts, err := ParseTemplate(s)
	if err != nil {
		return nil, err
	}
	var out []*Node
	for _, p := range parts {
		if p.T == "tpl" {
			out = append(out, p.Path)
		}
	}
	return out, nil
}

// --- rendering -------------------------------------------------------------------------

var (
	strictCtl = regexp.MustCompile(`[\x00-\x1f\x7f\x{2028}\x{2029}]`)
	crlf      = regexp.MustCompile(`\r\n?`)
	looseCtl  = regexp.MustCompile(`[\x00-\x09\x0b-\x1f\x7f\x{2028}\x{2029}]`)
	manyNL    = regexp.MustCompile(`\n{3,}`)
)

// SanitizeText is sanitizeText.
func SanitizeText(s string, strict bool) string {
	if strict {
		clean := strictCtl.ReplaceAllString(s, " ")
		if strx.Len16(clean) > MaxValueChars {
			return strx.Slice16(clean, MaxValueChars-1) + "…"
		}
		return clean
	}
	clean := crlf.ReplaceAllString(s, "\n")
	clean = looseCtl.ReplaceAllString(clean, " ")
	clean = manyNL.ReplaceAllString(clean, "\n\n")
	if strx.Len16(clean) > MaxRenderedChars {
		return strx.Slice16(clean, MaxRenderedChars-1) + "…"
	}
	return clean
}

func sanitizeDeep(v any, strict bool, depth int) any {
	if s, ok := v.(string); ok {
		return SanitizeText(s, strict)
	}
	if depth > 6 {
		return v
	}
	switch t := v.(type) {
	case []any:
		if len(t) > 100 {
			t = t[:100]
		}
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = sanitizeDeep(x, strict, depth+1)
		}
		return out
	case *jsonx.Object:
		out := jsonx.NewMap[any]()
		for i, k := range t.Keys() {
			if i >= 100 {
				break
			}
			x, _ := t.Get(k)
			out.Set(SanitizeText(k, true), sanitizeDeep(x, strict, depth+1))
		}
		return out
	}
	return v
}

// Stringify is template.ts's stringify.
func Stringify(v any) string {
	switch t := v.(type) {
	case nil, undefT:
		return ""
	case string:
		return t
	case float64:
		return jsonx.NumberString(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return CompactJSON(v)
}

// argNumber is Number(x) for a filter argument (NaN for junk).
func argNumber(x any) float64 {
	switch t := x.(type) {
	case float64:
		return t
	case string:
		return strx.Number(t)
	case nil, undefT:
		return 0
	}
	return math.NaN()
}

func pickField(el any, field []string) any {
	cur := el
	for _, k := range field {
		switch c := cur.(type) {
		case *jsonx.Object:
			v, ok := c.Get(k)
			if !ok {
				cur = undef
			} else {
				cur = v
			}
		case []any:
			if k == "length" {
				cur = float64(len(c))
			} else if digitsRe.MatchString(k) {
				n, _ := strconv.Atoi(k)
				if n < len(c) {
					cur = c[n]
				} else {
					cur = undef
				}
			} else {
				cur = undef
			}
		default:
			cur = nil
		}
	}
	return cur
}

func applyFilter(fl Filter, v any, env EvalEnv) any {
	arg := func(i int) any {
		if i < len(fl.Args) {
			return fl.Args[i]
		}
		return undef
	}
	switch fl.Name {
	case "time":
		ms, ok := ToMs(v, env.TZ, env.Now)
		if !ok {
			return v
		}
		return reminders.FormatClock(ms, env.TZ)
	case "date":
		ms, ok := ToMs(v, env.TZ, env.Now)
		if !ok {
			return v
		}
		return reminders.FormatDate(ms, env.TZ, env.Now)
	case "json":
		return CompactJSON(v)
	case "default":
		if IsEmpty(v) {
			return arg(0)
		}
		return v
	case "join":
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		sep := ", "
		if a := arg(0); !isUndef(a) && a != nil {
			sep = Stringify(a)
		}
		if len(arr) > 100 {
			arr = arr[:100]
		}
		parts := make([]string, len(arr))
		for i, x := range arr {
			parts[i] = Stringify(x)
		}
		return strings.Join(parts, sep)
	case "lines":
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		var field []string
		if s, ok := arg(0).(string); ok {
			for _, p := range strings.Split(s, ".") {
				if p != "" {
					field = append(field, p)
				}
			}
		}
		n := min(len(arr), MaxLines)
		shown := make([]string, 0, n+1)
		for _, el := range arr[:n] {
			shown = append(shown, "- "+Stringify(pickField(el, field)))
		}
		if len(arr) > MaxLines {
			shown = append(shown, fmt.Sprintf("- … (+%d lagi)", len(arr)-MaxLines))
		}
		return strings.Join(shown, "\n")
	case "upper":
		if s, ok := v.(string); ok {
			return jsUpper(s)
		}
		return v
	case "lower":
		if s, ok := v.(string); ok {
			return jsLower(s)
		}
		return v
	case "round":
		x, ok := Numeric(v)
		if !ok {
			return v
		}
		d := argNumber(arg(0))
		if isUndef(arg(0)) {
			d = 0
		}
		p := math.Pow(10, math.Max(0, math.Min(6, math.Trunc(d))))
		if math.IsNaN(d) {
			return nil
		}
		return noNegZero(jsRoundF(x*p) / p)
	case "truncate":
		s := Stringify(v)
		n := argNumber(arg(0))
		if math.IsNaN(n) {
			return s
		}
		k := int(math.Max(1, math.Trunc(n)))
		if strx.Len16(s) > k {
			return strx.Slice16(s, max(0, k-1)) + "…"
		}
		return s
	}
	return v
}

// TaintCheck reports whether a root key holds outside data.
type TaintCheck func(key string) bool

func evalPart(p TemplatePart, env EvalEnv, mode string, tainted TaintCheck) any {
	v := Evaluate(p.Path, env).Value
	if mode == "message" {
		strict := true
		if tainted != nil {
			strict = tainted(RootKey(p.Path))
		}
		v = sanitizeDeep(v, strict, 0)
	}
	for _, fl := range p.Filters {
		v = applyFilter(fl, v, env)
	}
	return v
}

// RenderString is renderString: a lone template keeps its type (data mode),
// anything else becomes a string.
func RenderString(s string, env EvalEnv, mode string, tainted TaintCheck) (any, error) {
	if !HasTemplate(s) {
		return s, nil
	}
	parts, err := ParseTemplate(s)
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = "data"
	}
	if len(parts) == 1 && parts[0].T == "tpl" && mode == "data" {
		return evalPart(parts[0], env, mode, nil), nil
	}
	var b strings.Builder
	n16 := 0
	for _, p := range parts {
		var piece string
		if p.T == "text" {
			piece = p.V
		} else {
			piece = Stringify(evalPart(p, env, mode, tainted))
		}
		b.WriteString(piece)
		n16 += strx.Len16(piece)
		if n16 > MaxRenderedChars*4 {
			break
		}
	}
	return b.String(), nil
}

// RenderText is renderText: always a string, capped. Without a taint check
// every value is treated as tainted.
func RenderText(s string, env EvalEnv, tainted TaintCheck) (string, error) {
	v, err := RenderString(s, env, "message", tainted)
	if err != nil {
		return "", err
	}
	out := Stringify(v)
	if str, ok := v.(string); ok {
		out = str
	}
	return ellipsis(out, MaxRenderedChars), nil
}

// RenderDeep is renderDeep: every string inside is rendered, keys stay literal.
func RenderDeep(v any, env EvalEnv, depth int) (any, error) {
	if s, ok := v.(string); ok {
		return RenderString(s, env, "data", nil)
	}
	if depth > 10 {
		return v, nil
	}
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			r, err := RenderDeep(x, env, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case *jsonx.Object:
		out := jsonx.NewMap[any]()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			r, err := RenderDeep(x, env, depth+1)
			if err != nil {
				return nil, err
			}
			out.Set(k, r)
		}
		return out, nil
	}
	return v, nil
}

// TemplatesIn is templatesIn: every template string inside a value.
func TemplatesIn(v any, depth int) []string {
	if s, ok := v.(string); ok {
		if HasTemplate(s) {
			return []string{s}
		}
		return nil
	}
	if depth > 10 {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			out = append(out, TemplatesIn(x, depth+1)...)
		}
	case *jsonx.Object:
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			out = append(out, TemplatesIn(x, depth+1)...)
		}
	}
	return out
}

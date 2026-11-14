package flows

// The flow expression language (expr.ts, RFC flows 6). A hand-written parser
// turns a string into an AST at save time; the AST is what runs. No eval, no
// regex from the flow, and evaluation is bounded.
//
// Semantics: == is strict except numbers against numeric strings; a
// comparison with null other than == / != is false; and/or short-circuit;
// division by zero is null; in + and - a null counts as 0.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var Roots = []string{"trigger", "steps", "vars", "flow", "item", "index", "now", "run"}

type arity struct{ min, max int }

// FunctionNames keeps the TS key order of FUNCTIONS (error messages list it).
var FunctionNames = []string{"exists", "empty", "contains", "startsWith", "len", "lower", "any", "count", "number", "round", "min", "max", "choose", "date", "daysUntil", "hoursUntil", "minutesSince", "weekday", "today"}

var functions = map[string]arity{
	"exists": {1, 1}, "empty": {1, 1}, "contains": {2, 2}, "startsWith": {2, 2}, "len": {1, 1}, "lower": {1, 1},
	"any": {3, 3}, "count": {3, 3}, "number": {1, 1}, "round": {1, 2}, "min": {2, 2}, "max": {2, 2},
	"choose": {3, 3}, "date": {1, 1}, "daysUntil": {1, 1}, "hoursUntil": {1, 1}, "minutesSince": {1, 1},
	"weekday": {0, 1}, "today": {0, 0},
}

const (
	MaxNodes     = 200
	MaxEvalSteps = 1000
	MaxString    = 10_000
	MaxSource    = 2000
)

// Node is one AST node. T is lit, path, call, not, neg, and, or, cmp or arith.
type Node struct {
	T   string
	Src string
	// lit
	V any
	// path: Root and Segs (string, float64 or *Node)
	Root string
	Segs []any
	// call
	Name string
	Args []*Node
	// not, neg
	E *Node
	// and, or, cmp, arith
	Op   string
	L, R *Node
}

// ExprError is a parse error with the problem as a sentence.
type ExprError struct {
	Msg string
	At  int
}

func (e *ExprError) Error() string { return e.Msg }

func exprErr(at int, format string, args ...any) *ExprError {
	return &ExprError{Msg: fmt.Sprintf(format, args...), At: at}
}

type tok struct {
	k    string // num, str, id, op, end
	v    string
	n    float64
	s, e int
}

var ops = []string{"==", "!=", "<=", ">=", "<", ">", "+", "-", "*", "/", "(", ")", "[", "]", ".", ","}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }
func isIDStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}
func isIDPart(r rune) bool { return isIDStart(r) || isDigit(r) }

func tokenize(src []rune) ([]tok, error) {
	var out []tok
	i := 0
	for i < len(src) {
		c := src[i]
		if strx.IsSpace(c) {
			i++
			continue
		}
		s := i
		if isDigit(c) {
			for i < len(src) && isDigit(src[i]) {
				i++
			}
			lastDot := len(out) > 0 && out[len(out)-1].k == "op" && out[len(out)-1].v == "."
			if i < len(src) && src[i] == '.' && i+1 < len(src) && isDigit(src[i+1]) && !lastDot {
				i++
				for i < len(src) && isDigit(src[i]) {
					i++
				}
			}
			v := string(src[s:i])
			n, _ := strconv.ParseFloat(v, 64)
			out = append(out, tok{k: "num", v: v, n: n, s: s, e: i})
			continue
		}
		if c == '"' || c == '\'' {
			i++
			var b strings.Builder
			for {
				if i >= len(src) {
					return nil, exprErr(s, "A string is missing its closing quote.")
				}
				ch := src[i]
				if ch == c {
					i++
					break
				}
				if ch == '\\' {
					if i+1 < len(src) {
						switch nx := src[i+1]; nx {
						case 'n':
							b.WriteRune('\n')
						case 't':
							b.WriteRune('\t')
						default:
							b.WriteRune(nx)
						}
					}
					i += 2
					continue
				}
				b.WriteRune(ch)
				i++
			}
			out = append(out, tok{k: "str", v: b.String(), s: s, e: i})
			continue
		}
		if isIDStart(c) {
			for i < len(src) && isIDPart(src[i]) {
				i++
			}
			out = append(out, tok{k: "id", v: string(src[s:i]), s: s, e: i})
			continue
		}
		op := ""
		for _, o := range ops {
			if strings.HasPrefix(string(src[i:min(len(src), i+2)]), o) {
				op = o
				break
			}
		}
		if op == "" {
			switch c {
			case '=':
				return nil, exprErr(i, `Use "==" to compare, "=" alone is not an operator.`)
			case '&', '|':
				return nil, exprErr(i, `Use "and" / "or" instead of && and ||.`)
			case '!':
				return nil, exprErr(i, `Use "not" instead of "!".`)
			}
			return nil, exprErr(i, `Unexpected character "%c".`, c)
		}
		out = append(out, tok{k: "op", v: op, s: s, e: i + len(op)})
		i += len(op)
	}
	out = append(out, tok{k: "end", s: len(src), e: len(src)})
	return out, nil
}

var keywords = map[string]bool{"and": true, "or": true, "not": true, "true": true, "false": true, "null": true}

func isRoot(s string) bool { return containsStr(Roots, s) }

type parser struct {
	src   []rune
	toks  []tok
	p     int
	nodes int
}

// panics with *ExprError; ParseExpr recovers.
func (ps *parser) fail(e *ExprError) { panic(e) }

func (ps *parser) peek() tok { return ps.toks[ps.p] }
func (ps *parser) next() tok { t := ps.toks[ps.p]; ps.p++; return t }
func (ps *parser) isOp(v string) bool {
	t := ps.peek()
	return t.k == "op" && t.v == v
}
func (ps *parser) isKw(v string) bool {
	t := ps.peek()
	return t.k == "id" && t.v == v
}
func (ps *parser) node(n *Node) *Node {
	ps.nodes++
	if ps.nodes > MaxNodes {
		ps.fail(exprErr(0, "The expression has more than %d parts.", MaxNodes))
	}
	return n
}
func (ps *parser) text(s, e int) string { return strx.Trim(string(ps.src[s:e])) }
func (ps *parser) prevEnd() int         { return ps.toks[ps.p-1].e }

func (ps *parser) expect(v string) {
	if !ps.isOp(v) {
		t := ps.peek()
		where := fmt.Sprintf(` near "%s"`, t.v)
		if t.k == "end" {
			where = " before the end"
		}
		ps.fail(exprErr(t.s, `Expected "%s"%s.`, v, where))
	}
	ps.next()
}

func (ps *parser) or() *Node {
	s := ps.peek().s
	l := ps.and()
	for ps.isKw("or") {
		ps.next()
		r := ps.and()
		l = ps.node(&Node{T: "or", L: l, R: r, Src: ps.text(s, ps.prevEnd())})
	}
	return l
}

func (ps *parser) and() *Node {
	s := ps.peek().s
	l := ps.not()
	for ps.isKw("and") {
		ps.next()
		r := ps.not()
		l = ps.node(&Node{T: "and", L: l, R: r, Src: ps.text(s, ps.prevEnd())})
	}
	return l
}

func (ps *parser) not() *Node {
	s := ps.peek().s
	if ps.isKw("not") {
		ps.next()
		e := ps.not()
		return ps.node(&Node{T: "not", E: e, Src: ps.text(s, ps.prevEnd())})
	}
	return ps.cmp()
}

func isCmp(t tok) bool {
	return t.k == "op" && (t.v == "==" || t.v == "!=" || t.v == "<" || t.v == "<=" || t.v == ">" || t.v == ">=")
}

func (ps *parser) cmp() *Node {
	s := ps.peek().s
	l := ps.sum()
	t := ps.peek()
	if isCmp(t) {
		ps.next()
		r := ps.sum()
		n := ps.node(&Node{T: "cmp", Op: t.v, L: l, R: r, Src: ps.text(s, ps.prevEnd())})
		if after := ps.peek(); isCmp(after) {
			ps.fail(exprErr(after.s, `Comparisons don't chain: write "a < b and b < c".`))
		}
		return n
	}
	return l
}

func (ps *parser) sum() *Node {
	s := ps.peek().s
	l := ps.term()
	for ps.isOp("+") || ps.isOp("-") {
		op := ps.next().v
		r := ps.term()
		l = ps.node(&Node{T: "arith", Op: op, L: l, R: r, Src: ps.text(s, ps.prevEnd())})
	}
	return l
}

func (ps *parser) term() *Node {
	s := ps.peek().s
	l := ps.unary()
	for ps.isOp("*") || ps.isOp("/") {
		op := ps.next().v
		r := ps.unary()
		l = ps.node(&Node{T: "arith", Op: op, L: l, R: r, Src: ps.text(s, ps.prevEnd())})
	}
	return l
}

func (ps *parser) unary() *Node {
	s := ps.peek().s
	if ps.isOp("-") {
		ps.next()
		e := ps.unary()
		return ps.node(&Node{T: "neg", E: e, Src: ps.text(s, ps.prevEnd())})
	}
	return ps.atom()
}

func (ps *parser) atom() *Node {
	t := ps.peek()
	switch t.k {
	case "num":
		ps.next()
		return ps.node(&Node{T: "lit", V: t.n, Src: t.v})
	case "str":
		ps.next()
		if strx.Len16(t.v) > MaxString {
			ps.fail(exprErr(t.s, "A string literal is too long."))
		}
		return ps.node(&Node{T: "lit", V: t.v, Src: ps.text(t.s, t.e)})
	}
	if ps.isOp("(") {
		ps.next()
		e := ps.or()
		ps.expect(")")
		return e
	}
	if t.k == "id" {
		if t.v == "true" || t.v == "false" {
			ps.next()
			return ps.node(&Node{T: "lit", V: t.v == "true", Src: t.v})
		}
		if t.v == "null" {
			ps.next()
			return ps.node(&Node{T: "lit", V: nil, Src: t.v})
		}
		if keywords[t.v] {
			ps.fail(exprErr(t.s, `"%s" can't start a value here.`, t.v))
		}
		if nx := ps.toks[ps.p+1]; nx.k == "op" && nx.v == "(" {
			return ps.call()
		}
		return ps.path()
	}
	if t.k == "end" {
		ps.fail(exprErr(t.s, "The expression ends too early."))
	}
	ps.fail(exprErr(t.s, `Unexpected "%s".`, t.v))
	return nil
}

func (ps *parser) call() *Node {
	t := ps.next()
	ar, ok := functions[t.v]
	if !ok {
		ps.fail(exprErr(t.s, `Unknown function "%s". Functions: %s.`, t.v, strings.Join(FunctionNames, ", ")))
	}
	ps.expect("(")
	var args []*Node
	if !ps.isOp(")") {
		args = append(args, ps.or())
		for ps.isOp(",") {
			ps.next()
			args = append(args, ps.or())
		}
	}
	ps.expect(")")
	if len(args) < ar.min || len(args) > ar.max {
		count := strconv.Itoa(ar.min)
		if ar.min != ar.max {
			count = fmt.Sprintf("%d to %d", ar.min, ar.max)
		}
		plural := "s"
		if ar.max == 1 {
			plural = ""
		}
		ps.fail(exprErr(t.s, "%s() takes %s argument%s, got %d.", t.v, count, plural, len(args)))
	}
	return ps.node(&Node{T: "call", Name: t.v, Args: args, Src: ps.text(t.s, ps.prevEnd())})
}

func (ps *parser) path() *Node {
	t := ps.next()
	if !isRoot(t.v) {
		hint := ""
		if t.v == "n" {
			hint = " For the re-ask number use run.followUps."
		}
		ps.fail(exprErr(t.s, `"%s" isn't a known value. Paths start with %s.%s`, t.v, strings.Join(Roots, ", "), hint))
	}
	var segs []any
	for {
		if ps.isOp(".") {
			ps.next()
			seg := ps.next()
			switch {
			case seg.k == "id":
				segs = append(segs, seg.v)
			case seg.k == "num" && digitsRe.MatchString(seg.v):
				segs = append(segs, seg.n)
			case seg.k == "num":
				for _, part := range strings.Split(seg.v, ".") {
					n, _ := strconv.ParseFloat(part, 64)
					segs = append(segs, n)
				}
			default:
				ps.fail(exprErr(seg.s, `Expected a field name after "."`))
			}
			continue
		}
		if ps.isOp("[") {
			ps.next()
			inner := ps.peek()
			switch {
			case inner.k == "str":
				ps.next()
				segs = append(segs, inner.v)
			case inner.k == "num":
				ps.next()
				segs = append(segs, inner.n)
			case inner.k == "id" && isRoot(inner.v):
				segs = append(segs, ps.path())
			default:
				ps.fail(exprErr(inner.s, "Inside [ ] use a path, a string or a number."))
			}
			ps.expect("]")
			continue
		}
		break
	}
	return ps.node(&Node{T: "path", Root: t.v, Segs: segs, Src: ps.text(t.s, ps.prevEnd())})
}

// ParseExpr is parseExpr.
func ParseExpr(src string) (n *Node, err error) {
	if strx.Trim(src) == "" {
		return nil, exprErr(0, "The expression is empty.")
	}
	if strx.Len16(src) > MaxSource {
		return nil, exprErr(0, "The expression is longer than %d characters.", MaxSource)
	}
	runes := []rune(src)
	toks, err := tokenize(runes)
	if err != nil {
		return nil, err
	}
	ps := &parser{src: runes, toks: toks}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*ExprError)
			if !ok {
				panic(r)
			}
			n, err = nil, e
		}
	}()
	ast := ps.or()
	if ps.peek().k != "end" {
		t := ps.peek()
		return nil, exprErr(t.s, `Unexpected "%s" after the end of the expression.`, t.v)
	}
	return ast, nil
}

// ParsePath is parsePath: a plain path only.
func ParsePath(src string) (*Node, error) {
	ast, err := ParseExpr(src)
	if err != nil {
		return nil, err
	}
	if ast.T != "path" {
		return nil, exprErr(0, "Use a plain path here, like steps.x.data.items.")
	}
	return ast, nil
}

// PathsOf is pathsOf: every path the expression reads, including inside [ ].
func PathsOf(n *Node, out []*Node) []*Node {
	switch n.T {
	case "lit":
	case "path":
		out = append(out, n)
		for _, s := range n.Segs {
			if sn, ok := s.(*Node); ok {
				out = PathsOf(sn, out)
			}
		}
	case "call":
		for _, a := range n.Args {
			out = PathsOf(a, out)
		}
	case "not", "neg":
		out = PathsOf(n.E, out)
	default:
		out = PathsOf(n.L, out)
		out = PathsOf(n.R, out)
	}
	return out
}

// RootKey is rootKey: the taint key a path belongs to.
func RootKey(p *Node) string {
	if p.Root == "steps" || p.Root == "vars" || p.Root == "flow" {
		if len(p.Segs) > 0 {
			switch s := p.Segs[0].(type) {
			case string:
				return p.Root + "." + s
			case float64:
				return p.Root + "." + jsonx.NumberString(s)
			}
		}
		return p.Root
	}
	return p.Root
}

// --- evaluation ------------------------------------------------------------------------

type EvalEnv struct {
	Scope Scope
	TZ    string
	Now   int64
}

type TraceEntry struct {
	Expr  string `json:"expr"`
	Value any    `json:"value"`
}

type EvalResult struct {
	Value    any
	Trace    []TraceEntry
	Warnings []string
}

type budgetErr struct{}

type evaluator struct {
	env      EvalEnv
	steps    int
	trace    []TraceEntry
	warnings []string
	seen     map[string]bool
}

func (e *evaluator) record(src string, v any) {
	if e.seen[src] || len(e.trace) >= 24 {
		return
	}
	e.seen[src] = true
	e.trace = append(e.trace, TraceEntry{Expr: src, Value: clip(v)})
}

func (e *evaluator) warn(w string) {
	if len(e.warnings) < 8 && !containsStr(e.warnings, w) {
		e.warnings = append(e.warnings, w)
	}
}

func (e *evaluator) resolvePath(n *Node) any {
	segs := make([]any, 0, len(n.Segs))
	for _, s := range n.Segs {
		if sn, ok := s.(*Node); ok {
			k := e.ev(sn)
			switch kv := k.(type) {
			case string:
				segs = append(segs, kv)
			case float64:
				if math.IsInf(kv, 0) || math.IsNaN(kv) {
					return nil
				}
				segs = append(segs, kv)
			default:
				return nil
			}
		} else {
			segs = append(segs, s)
		}
	}
	root, ok := e.env.Scope[n.Root]
	if !ok || isUndef(root) {
		root = nil
	}
	return GetIn(root, segs)
}

func (e *evaluator) ev(n *Node) any {
	e.steps++
	if e.steps > MaxEvalSteps {
		panic(budgetErr{})
	}
	switch n.T {
	case "lit":
		return n.V
	case "path":
		v := e.resolvePath(n)
		e.record(n.Src, v)
		return v
	case "call":
		args := make([]any, len(n.Args))
		for i, a := range n.Args {
			args[i] = e.ev(a)
		}
		v := e.callFn(n.Name, args)
		e.record(n.Src, v)
		return v
	case "not":
		return !Truthy(e.ev(n.E))
	case "neg":
		v, ok := Numeric(e.ev(n.E))
		if !ok {
			e.warn(n.Src + ": not a number")
			return nil
		}
		return noNegZero(-v)
	case "and":
		if Truthy(e.ev(n.L)) {
			return Truthy(e.ev(n.R))
		}
		return false
	case "or":
		if Truthy(e.ev(n.L)) {
			return true
		}
		return Truthy(e.ev(n.R))
	case "cmp":
		return e.compare(n.Op, e.ev(n.L), e.ev(n.R), n.Src)
	case "arith":
		return e.arith(n.Op, e.ev(n.L), e.ev(n.R), n.Src)
	}
	return nil
}

func noNegZero(f float64) float64 {
	if f == 0 {
		return 0
	}
	return f
}

// Evaluate is evaluate.
func Evaluate(ast *Node, env EvalEnv) (res EvalResult) {
	e := &evaluator{env: env, seen: map[string]bool{}}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(budgetErr); !ok {
				panic(r)
			}
			e.warn(fmt.Sprintf("evaluation took more than %d steps", MaxEvalSteps))
			res = EvalResult{Value: false, Trace: e.trace, Warnings: e.warnings}
		}
	}()
	v := e.ev(ast)
	if s, ok := v.(string); ok && strx.Len16(s) > MaxString {
		e.warn(fmt.Sprintf("the result is longer than %d characters", MaxString))
		return EvalResult{Value: nil, Trace: e.trace, Warnings: e.warnings}
	}
	return EvalResult{Value: v, Trace: e.trace, Warnings: e.warnings}
}

// EvalCondition is evalCondition.
func EvalCondition(ast *Node, env EvalEnv) (EvalResult, bool) {
	r := Evaluate(ast, env)
	return r, Truthy(r.Value)
}

func clip(v any) any {
	switch t := v.(type) {
	case string:
		if strx.Len16(t) > 200 {
			return strx.Slice16(t, 199) + "…"
		}
	case []any, *jsonx.Object:
		s := CompactJSON(t)
		if strx.Len16(s) > 300 {
			return strx.Slice16(s, 299) + "…"
		}
	}
	return v
}

func isObjectish(v any) bool {
	switch v.(type) {
	case []any, *jsonx.Object:
		return true
	}
	return false
}

func deepEqual(a, b any) bool {
	if isObjectish(a) && isObjectish(b) {
		return CompactJSON(a) == CompactJSON(b)
	}
	if isObjectish(a) || isObjectish(b) {
		return false
	}
	return a == b
}

// LooseEqual is looseEqual.
func LooseEqual(a, b any) bool {
	if isUndef(a) {
		a = nil
	}
	if isUndef(b) {
		b = nil
	}
	_, an := a.(float64)
	_, bn := b.(float64)
	if an || bn {
		x, xok := Numeric(a)
		y, yok := Numeric(b)
		if xok && yok {
			return x == y
		}
	}
	return deepEqual(a, b)
}

func typeOfValue(v any) string {
	switch v.(type) {
	case nil, undefT:
		return "null"
	case []any:
		return "a list"
	case *jsonx.Object:
		return "an object"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	}
	return "an object"
}

func (e *evaluator) compare(op string, a, b any, src string) bool {
	if op == "==" {
		return LooseEqual(a, b)
	}
	if op == "!=" {
		return !LooseEqual(a, b)
	}
	if a == nil || isUndef(a) || b == nil || isUndef(b) {
		return false
	}
	na, aok := Numeric(a)
	nb, bok := Numeric(b)
	var c int
	if aok && bok {
		switch {
		case na < nb:
			c = -1
		case na > nb:
			c = 1
		}
	} else if sa, ok := a.(string); ok {
		sb, ok2 := b.(string)
		if !ok2 {
			e.warn(fmt.Sprintf("%s: can't compare %s with %s", src, typeOfValue(a), typeOfValue(b)))
			return false
		}
		c = strx.Compare16(sa, sb)
	} else {
		e.warn(fmt.Sprintf("%s: can't compare %s with %s", src, typeOfValue(a), typeOfValue(b)))
		return false
	}
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

// templateString is `${v}` in a JS template literal (null and undefined as
// "", since the caller wrote `${a ?? ""}`).
func templateString(v any, top bool) string {
	switch t := v.(type) {
	case nil, undefT:
		if top {
			return ""
		}
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
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = templateString(x, false)
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func (e *evaluator) arith(op string, a, b any, src string) any {
	_, aNum := Numeric(a)
	_, bNum := Numeric(b)
	_, aStr := a.(string)
	_, bStr := b.(string)
	if op == "+" && (aStr || bStr) && !aNum && !bNum {
		s := templateString(a, true) + templateString(b, true)
		if strx.Len16(s) > MaxString {
			return strx.Slice16(s, MaxString)
		}
		return s
	}
	if op == "+" || op == "-" {
		var x, y float64
		var xok, yok bool
		if a == nil || isUndef(a) {
			x, xok = 0, true
		} else {
			x, xok = Numeric(a)
		}
		if b == nil || isUndef(b) {
			y, yok = 0, true
		} else {
			y, yok = Numeric(b)
		}
		if !xok || !yok {
			e.warn(src + ": not a number")
			return nil
		}
		if op == "+" {
			return noNegZero(x + y)
		}
		return noNegZero(x - y)
	}
	x, xok := Numeric(a)
	y, yok := Numeric(b)
	if !xok || !yok {
		if a != nil && b != nil && !isUndef(a) && !isUndef(b) {
			e.warn(src + ": not a number")
		}
		return nil
	}
	if op == "*" {
		return noNegZero(x * y)
	}
	if y == 0 {
		return nil
	}
	return noNegZero(x / y)
}

func jsRoundF(f float64) float64 { return mathFloor(f + 0.5) }

func (e *evaluator) callFn(name string, args []any) any {
	get := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return undef
	}
	a, b, c := get(0), get(1), get(2)
	env := e.env
	switch name {
	case "exists":
		return a != nil && !isUndef(a)
	case "empty":
		return IsEmpty(a)
	case "contains":
		if s, ok := a.(string); ok {
			switch bv := b.(type) {
			case string:
				return strings.Contains(s, bv)
			case float64:
				return strings.Contains(s, jsonx.NumberString(bv))
			}
		}
		if arr, ok := a.([]any); ok {
			for _, x := range arr {
				if LooseEqual(x, b) {
					return true
				}
			}
			return false
		}
		return false
	case "startsWith":
		if s, ok := a.(string); ok {
			switch bv := b.(type) {
			case string:
				return strings.HasPrefix(s, bv)
			case float64:
				return strings.HasPrefix(s, jsonx.NumberString(bv))
			}
		}
		return false
	case "len":
		switch t := a.(type) {
		case string:
			return float64(strx.Len16(t))
		case []any:
			return float64(len(t))
		case *jsonx.Object:
			return float64(t.Len())
		}
		return 0.0
	case "lower":
		if s, ok := a.(string); ok {
			return jsLower(s)
		}
		return a
	case "any", "count":
		arr, ok := a.([]any)
		if !ok {
			if name == "any" {
				return false
			}
			return 0.0
		}
		var field []any
		if s, ok := b.(string); ok {
			for _, p := range strings.Split(s, ".") {
				if p != "" {
					field = append(field, p)
				}
			}
		}
		if len(arr) > 1000 {
			arr = arr[:1000]
		}
		n := 0
		for _, el := range arr {
			if LooseEqual(GetIn(el, field), c) {
				n++
			}
		}
		if name == "any" {
			return n > 0
		}
		return float64(n)
	case "number":
		if x, ok := Numeric(a); ok {
			return x
		}
		return nil
	case "round":
		x, xok := Numeric(a)
		d, dok := 0.0, true
		if !isUndef(b) {
			d, dok = Numeric(b)
		}
		if !xok || !dok {
			return nil
		}
		p := math.Pow(10, math.Max(0, math.Min(6, math.Trunc(d))))
		return noNegZero(jsRoundF(x*p) / p)
	case "min", "max":
		x, xok := Numeric(a)
		y, yok := Numeric(b)
		if !xok || !yok {
			if xok {
				return x
			}
			if yok {
				return y
			}
			return nil
		}
		if name == "min" {
			return math.Min(x, y)
		}
		return math.Max(x, y)
	case "choose":
		if Truthy(a) {
			return b
		}
		return c
	case "date":
		if ms, ok := ToMs(a, env.TZ, env.Now); ok {
			return float64(ms)
		}
		return nil
	case "daysUntil":
		ms, ok := ToMs(a, env.TZ, env.Now)
		if !ok {
			if a != nil {
				e.warn(fmt.Sprintf("daysUntil: %s isn't a date", CompactJSON(clip(a))))
			}
			return nil
		}
		return float64(CalendarDaysBetween(env.Now, ms, env.TZ))
	case "hoursUntil":
		ms, ok := ToMs(a, env.TZ, env.Now)
		if !ok {
			return nil
		}
		return noNegZero(jsRoundF(float64(ms-env.Now)/3600_000*100) / 100)
	case "minutesSince":
		ms, ok := ToMs(a, env.TZ, env.Now)
		if !ok {
			return nil
		}
		return noNegZero(math.Floor(float64(env.Now-ms) / 60_000))
	case "weekday":
		ms, ok := env.Now, true
		if len(args) > 0 {
			ms, ok = ToMs(a, env.TZ, env.Now)
		}
		if !ok {
			return nil
		}
		return WeekdayOfMs(ms, env.TZ)
	case "today":
		return LocalDateOf(env.Now, env.TZ)
	}
	return nil
}

// ExplainTrace is explainTrace: "daysUntil(item.due) = 2" lines.
func ExplainTrace(trace []TraceEntry) string {
	parts := make([]string, len(trace))
	for i, t := range trace {
		parts[i] = t.Expr + " = " + CompactJSON(t.Value)
	}
	return strings.Join(parts, ", ")
}

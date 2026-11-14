package flows

// The flow definition (schema.ts): triggers[] and steps[], validated by the
// zod port in zod.go, then by the graph and data checks in validate.go. The
// parsed definition is kept twice: Raw is zod's output (what
// flow_versions.definition stores and what the hash reads), the typed fields
// are decoded from it for the engine.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const DefinitionSchema = 1

var IDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

const (
	MaxSteps           = 40
	MaxTriggers        = 8
	MaxDefinitionBytes = 32 * 1024
	MaxEach            = 50
	DefaultEach        = 20
	MaxWaitMin         = 7 * 24 * 60
)

// ReservedIDs are onError values that aren't step ids.
var ReservedIDs = []string{"fail", "continue"}

const idMsg = "Ids use lowercase letters, digits and _, starting with a letter (max 32), like ask_1"

func idSchema() *zString    { return zstr().regex(IDRe, `^[a-z][a-z0-9_]{0,31}$`, idMsg) }
func targetSchema() zSchema { return nullable(idSchema()) }
func text(max int) *zString { return zstr().trim().min(1).max(max) }
func anyRecord() *zRecord   { return zrecord(zstr(), &zUnknown{}) }
func defRecord() *zDefault  { return withDefault(anyRecord(), emptyObject()) }

var triggerBase = []zField{
	f("id", idSchema()),
	f("start", idSchema()),
	f("lateGraceMin", opt(zint(1, 1440))),
}

func trigger(fields ...zField) *zObject {
	return zobj(append(append([]zField{}, triggerBase...), fields...)...)
}

var ChatCommandRe = regexp.MustCompile(`^/[a-z0-9_]{1,32}$`)
var selectRe = regexp.MustCompile(`^[A-Za-z0-9_.-]*$`)

// FlowEvents are FLOW_EVENTS.
var FlowEvents = []string{"checkin.answered", "checkin.missed", "run.failed", "run.done"}

var triggerSchema = zdisc("kind",
	trigger(f("kind", zlit("schedule")), f("source", sourceSchema), f("days", opt(daysSchema()))),
	trigger(f("kind", zlit("webhook")), f("hookId", text(80))),
	trigger(
		f("kind", zlit("chat")),
		f("command", opt(zstr().trim().regex(ChatCommandRe, `^\/[a-z0-9_]{1,32}$`, `A command looks like "/beres" (lowercase, max 32)`))),
		f("phrases", opt(zarr(text(60)).min(1).max(10))),
	),
	trigger(
		f("kind", zlit("poll")),
		f("tool", text(130)),
		f("connectorId", opt(text(80))),
		f("args", defRecord()),
		f("everyMin", zint(5, 1440)),
		f("select", withDefault(zstr().trim().max(200).regex(selectRe, `^[A-Za-z0-9_.-]*$`, "Use a dotted path like items.0.state"), func() any { return "" })),
		f("fire", withDefault(zenum("change", "truthy"), func() any { return "change" })),
	),
	trigger(f("kind", zlit("event")), f("event", zenum(FlowEvents...)), f("flow", withDefault(text(80), func() any { return "self" }))),
	trigger(f("kind", zlit("manual"))),
)

var stepBase = []zField{
	f("id", idSchema()),
	f("next", opt(targetSchema())),
	f("name", opt(text(80))),
	f("onError", opt(zunion(zenum("fail", "continue"), idSchema()))),
	f("timeoutSec", opt(zint(1, 60))),
}

func step(fields ...zField) *zObject {
	return zobj(append(append([]zField{}, stepBase...), fields...)...)
}

var deadlineSchema = zdisc("kind",
	zobj(f("kind", zlit("next")), f("plusMin", opt(zint(-1440, 1440)))),
	zobj(f("kind", zlit("after")), f("minutes", zunion(zint(1, MaxWaitMin), text(300)))),
	zobj(f("kind", zlit("at")), f("at", text(300)), f("plusMin", opt(zint(-MaxWaitMin, MaxWaitMin)))),
)

var promptInputKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

func answersUnique(v any) bool {
	arr, _ := v.([]any)
	seen := map[string]bool{}
	for _, a := range arr {
		id := idOf(a)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func idOf(a any) string {
	if o, ok := a.(*jsonx.Object); ok {
		v, _ := o.Get("id")
		s, _ := v.(string)
		return s
	}
	return ""
}

func answersClose(v any) bool {
	arr, _ := v.([]any)
	for _, a := range arr {
		if o, ok := a.(*jsonx.Object); ok {
			if c, _ := o.Get("closes"); jsonx.Truthy(c) {
				return true
			}
		}
	}
	return false
}

func setValuesCount(v any) bool {
	o, _ := v.(*jsonx.Object)
	n := o.Len()
	return n >= 1 && n <= 20
}

var stepSchema = zdisc("kind",
	step(
		f("kind", zlit("tool")),
		f("tool", text(130)),
		f("connectorId", opt(text(80))),
		f("args", defRecord()),
		f("parse", opt(zobj(f("field", withDefault(zstr().trim().max(200), func() any { return "text" })), f("as", zlit("json"))))),
		f("retry", opt(zobj(f("max", zint(0, 5)), f("backoffSec", withDefault(zint(5, 3600), func() any { return 30.0 }))))),
		f("idempotent", opt(&zBool{})),
	),
	step(f("kind", zlit("if")), f("when", text(2000)), f("then", targetSchema()), f("else", withDefault(targetSchema(), func() any { return nil }))),
	step(
		f("kind", zlit("message")),
		f("text", text(4000)),
		f("to", opt(text(80))),
		f("contact", opt(text(80))),
		f("signature", opt(&zBool{})),
	),
	step(
		f("kind", zlit("checkin")),
		f("text", text(1000)),
		f("answers", withDefault(zarr(answerSchema).min(1).max(6), defaultAnswersValue).
			refine(answersUnique, "Answer ids must be unique").
			refine(answersClose, "At least one answer must close the check-in")),
		f("followUp", withDefault(zobj(
			f("everyMin", withDefault(zint(5, 1440), func() any { return 30.0 })),
			f("max", withDefault(zint(0, 20), func() any { return 3.0 })),
			f("text", opt(text(1000))),
		), func() any { return obj("everyMin", 30.0, "max", 3.0) })),
		f("deadline", withDefault(deadlineSchema, func() any { return obj("kind", "next") })),
		f("missedText", opt(text(1000))),
		f("on", opt(zrecord(zstr(), targetSchema()))),
		f("onMissed", opt(targetSchema())),
	),
	step(
		f("kind", zlit("wait")),
		f("minutes", opt(zint(1, MaxWaitMin))),
		f("until", opt(text(300))),
		f("plusMin", opt(zint(-MaxWaitMin, MaxWaitMin))),
	),
	step(
		f("kind", zlit("prompt")),
		f("prompt", text(4000)),
		f("input", withDefault(zrecord(zstr().regex(promptInputKeyRe, `^[A-Za-z][A-Za-z0-9_]{0,31}$`), zstr().max(2000)), emptyObject())),
		f("tools", withDefault(zarr(text(130)).max(20), func() any { return []any{} })),
		f("send", withDefault(&zBool{}, func() any { return false })),
		f("model", withDefault(zenum("main", "fast"), func() any { return "main" })),
	),
	step(
		f("kind", zlit("set")),
		f("scope", withDefault(zenum("run", "flow"), func() any { return "run" })),
		f("values", zrecord(
			zstr().regex(IDRe, `^[a-z][a-z0-9_]{0,31}$`, "Variable names use lowercase letters, digits and _, starting with a letter (max 32), like last_at"),
			text(2000),
		).refine(setValuesCount, "Set 1 to 20 values")),
	),
	step(
		f("kind", zlit("each")),
		f("items", text(300)),
		f("where", opt(text(2000))),
		f("key", opt(text(300))),
		f("max", withDefault(zint(1, MaxEach), func() any { return float64(DefaultEach) })),
		f("body", idSchema()),
	),
)

var definitionSchema = zobj(
	f("schema", withDefault(zlit(float64(DefinitionSchema)), func() any { return float64(DefinitionSchema) })),
	f("name", text(80)),
	f("description", opt(text(200))),
	f("tz", opt(zstr().trim().min(1).max(64))),
	f("triggers", withDefault(zarr(triggerSchema).max(MaxTriggers), func() any { return []any{} })),
	f("steps", zarr(stepSchema).min(1).max(MaxSteps)),
	f("limits", opt(zobj(
		f("runsPerHour", opt(zint(1, 30))),
		f("dailyTokens", opt(zint(1000, 500_000))),
	))),
)

// UpgradeDefinition is upgradeDefinition: version 1 is the first, so it is
// the identity today.
func UpgradeDefinition(raw any) any { return raw }

// CanonicalJSON is canonicalJson: sorted keys, "" for undefined.
func CanonicalJSON(v any) string {
	if isUndef(v) {
		return ""
	}
	return jsonx.Canonical(v)
}

// Problem is a save-time problem: a JSON path and a sentence.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// zodProblems is zodProblems: the first 12 issues, with ids and keys that
// break the naming rule quoted.
func zodProblems(issues []zIssue, input any) []Problem {
	if len(issues) > 12 {
		issues = issues[:12]
	}
	out := make([]Problem, len(issues))
	for i, is := range issues {
		out[i] = Problem{Path: FormatPath(is.path), Message: issueMessage(is, input)}
	}
	return out
}

func issueMessage(i zIssue, input any) string {
	if i.code == "invalid_key" {
		key := ""
		if len(i.path) > 0 {
			key = fmt.Sprint(i.path[len(i.path)-1])
		}
		inner := ""
		if len(i.inner) > 0 {
			inner = i.inner[0].message
		}
		if inner != "" && inner != "Invalid input" {
			return `Key "` + key + `": ` + inner + "."
		}
		return `Key "` + key + `": not allowed here.`
	}
	if i.code == "invalid_format" {
		if s, ok := valueAt(input, i.path).(string); ok {
			return `"` + s + `": ` + i.message + "."
		}
	}
	return i.message
}

func valueAt(v any, path []any) any {
	cur := v
	for _, p := range path {
		switch c := cur.(type) {
		case *jsonx.Object:
			x, ok := c.Get(fmt.Sprint(p))
			if !ok {
				return nil
			}
			cur = x
		case []any:
			n, ok := p.(int)
			if !ok || n < 0 || n >= len(c) {
				return nil
			}
			cur = c[n]
		default:
			return nil
		}
	}
	return cur
}

// FormatPath is formatPath: steps[1].id, (root) when empty.
func FormatPath(path []any) string {
	var b strings.Builder
	for _, p := range path {
		if n, ok := p.(int); ok {
			fmt.Fprintf(&b, "[%d]", n)
		} else if b.Len() > 0 {
			b.WriteString("." + fmt.Sprint(p))
		} else {
			b.WriteString(fmt.Sprint(p))
		}
	}
	if b.Len() == 0 {
		return "(root)"
	}
	return b.String()
}

// --- typed view ------------------------------------------------------------------------

// Target is a step reference that may be absent (Set false), null or an id.
type Target struct {
	Set bool
	ID  *string
}

func (t Target) To() string {
	if t.ID == nil {
		return ""
	}
	return *t.ID
}

type TimeItem struct {
	Time  string
	Label *string
}

type SourceItem struct {
	Field     string
	Label     *string
	OffsetMin int
}

type SourceList struct {
	Path      string
	At        string
	Label     *string
	Time      string
	OffsetMin int
}

// Source is ScheduleSource.
type Source struct {
	Kind string
	// times
	Times []TimeItem
	// rrule
	RRule string
	Start string
	Label *string
	// tool
	ConnectorID string
	Tool        string
	Args        *jsonx.Object
	RefreshAt   string
	Items       []SourceItem
	List        *SourceList
}

// Answer is AnswerOption.
type Answer struct {
	ID     string
	Label  string
	Words  []string
	Closes bool
	Reply  *string
}

// Trigger is FlowTrigger.
type Trigger struct {
	Raw          *jsonx.Object
	ID           string
	Start        string
	LateGraceMin *int
	Kind         string
	// schedule
	Source  Source
	Days    []string
	HasDays bool
	// webhook
	HookID string
	// chat
	Command *string
	Phrases []string
	// poll
	Tool        string
	ConnectorID *string
	Args        *jsonx.Object
	EveryMin    int
	Select      string
	Fire        string
	// event
	Event string
	Flow  string
}

type Deadline struct {
	Kind    string
	PlusMin *int
	// after: a number or a template.
	Minutes     *int
	MinutesText *string
	At          string
	Raw         *jsonx.Object
}

type FollowUp struct {
	EveryMin int
	Max      int
	Text     *string
}

// Step is FlowStep.
type Step struct {
	Raw        *jsonx.Object
	ID         string
	Kind       string
	Next       Target
	Name       *string
	OnError    *string
	TimeoutSec *int
	// tool
	Tool        string
	ConnectorID *string
	Args        *jsonx.Object
	ParseField  *string // parse.field when parse is set
	Retry       *Retry
	Idempotent  bool
	// if
	When string
	Then Target
	Else Target
	// message
	Text      string
	To        *string
	Contact   *string
	Signature *bool
	// checkin
	Answers    []Answer
	FollowUp   FollowUp
	Deadline   Deadline
	MissedText *string
	On         *jsonx.Map[*string]
	OnMissed   Target
	// wait
	Minutes *int
	Until   *string
	PlusMin *int
	// prompt
	Prompt string
	Input  *jsonx.Map[string]
	Tools  []string
	Send   bool
	Model  string
	// set
	Scope  string
	Values *jsonx.Map[string]
	// each
	Items string
	Where *string
	Key   *string
	Max   int
	Body  string
}

type Retry struct {
	Max        int
	BackoffSec int
}

type DefLimits struct {
	RunsPerHour *int
	DailyTokens *int
}

// Definition is FlowDefinition.
type Definition struct {
	Raw         *jsonx.Object
	Schema      int
	Name        string
	Description *string
	TZ          *string
	Triggers    []Trigger
	Steps       []Step
	Limits      *DefLimits
}

func (d *Definition) Step(id string) *Step {
	for i := range d.Steps {
		if d.Steps[i].ID == id {
			return &d.Steps[i]
		}
	}
	return nil
}

func (d *Definition) Trigger(id string) *Trigger {
	for i := range d.Triggers {
		if d.Triggers[i].ID == id {
			return &d.Triggers[i]
		}
	}
	return nil
}

// --- decoding zod's output -----------------------------------------------------------

func gstr(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func gostr(o *jsonx.Object, k string) *string {
	v, ok := o.Get(k)
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

func gint(o *jsonx.Object, k string) int {
	v, _ := o.Get(k)
	n, _ := v.(float64)
	return int(n)
}

func goint(o *jsonx.Object, k string) *int {
	v, ok := o.Get(k)
	if !ok {
		return nil
	}
	n, ok := v.(float64)
	if !ok {
		return nil
	}
	i := int(n)
	return &i
}

func gbool(o *jsonx.Object, k string) bool {
	v, _ := o.Get(k)
	b, _ := v.(bool)
	return b
}

func gobj(o *jsonx.Object, k string) *jsonx.Object {
	v, _ := o.Get(k)
	x, _ := v.(*jsonx.Object)
	return x
}

func garr(o *jsonx.Object, k string) []any {
	v, _ := o.Get(k)
	x, _ := v.([]any)
	return x
}

func gstrs(o *jsonx.Object, k string) []string {
	var out []string
	for _, x := range garr(o, k) {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func gtarget(o *jsonx.Object, k string) Target {
	v, ok := o.Get(k)
	if !ok {
		return Target{}
	}
	s, ok := v.(string)
	if !ok {
		return Target{Set: true}
	}
	return Target{Set: true, ID: &s}
}

func decodeSource(o *jsonx.Object) Source {
	s := Source{Kind: gstr(o, "kind")}
	switch s.Kind {
	case "times":
		for _, x := range garr(o, "times") {
			t := x.(*jsonx.Object)
			s.Times = append(s.Times, TimeItem{Time: gstr(t, "time"), Label: gostr(t, "label")})
		}
	case "rrule":
		s.RRule, s.Start, s.Label = gstr(o, "rrule"), gstr(o, "start"), gostr(o, "label")
	case "tool":
		s.ConnectorID, s.Tool, s.Args, s.RefreshAt = gstr(o, "connectorId"), gstr(o, "tool"), gobj(o, "args"), gstr(o, "refreshAt")
		if _, ok := o.Get("items"); ok {
			s.Items = []SourceItem{}
			for _, x := range garr(o, "items") {
				it := x.(*jsonx.Object)
				s.Items = append(s.Items, SourceItem{Field: gstr(it, "field"), Label: gostr(it, "label"), OffsetMin: gint(it, "offsetMin")})
			}
		}
		if l := gobj(o, "list"); l != nil {
			s.List = &SourceList{Path: gstr(l, "path"), At: gstr(l, "at"), Label: gostr(l, "label"), Time: gstr(l, "time"), OffsetMin: gint(l, "offsetMin")}
		}
	}
	return s
}

func decodeAnswers(arr []any) []Answer {
	var out []Answer
	for _, x := range arr {
		a := x.(*jsonx.Object)
		out = append(out, Answer{ID: gstr(a, "id"), Label: gstr(a, "label"), Words: gstrs(a, "words"), Closes: gbool(a, "closes"), Reply: gostr(a, "reply")})
	}
	return out
}

func decodeTrigger(o *jsonx.Object) Trigger {
	t := Trigger{Raw: o, ID: gstr(o, "id"), Start: gstr(o, "start"), LateGraceMin: goint(o, "lateGraceMin"), Kind: gstr(o, "kind")}
	switch t.Kind {
	case "schedule":
		t.Source = decodeSource(gobj(o, "source"))
		if v, ok := o.Get("days"); ok && v != nil {
			t.Days, t.HasDays = gstrs(o, "days"), true
		}
	case "webhook":
		t.HookID = gstr(o, "hookId")
	case "chat":
		t.Command = gostr(o, "command")
		if _, ok := o.Get("phrases"); ok {
			t.Phrases = gstrs(o, "phrases")
		}
	case "poll":
		t.Tool, t.ConnectorID, t.Args = gstr(o, "tool"), gostr(o, "connectorId"), gobj(o, "args")
		t.EveryMin, t.Select, t.Fire = gint(o, "everyMin"), gstr(o, "select"), gstr(o, "fire")
	case "event":
		t.Event, t.Flow = gstr(o, "event"), gstr(o, "flow")
	}
	return t
}

func decodeStep(o *jsonx.Object) Step {
	s := Step{Raw: o, ID: gstr(o, "id"), Kind: gstr(o, "kind"), Next: gtarget(o, "next"), Name: gostr(o, "name"), OnError: gostr(o, "onError"), TimeoutSec: goint(o, "timeoutSec")}
	switch s.Kind {
	case "tool":
		s.Tool, s.ConnectorID, s.Args = gstr(o, "tool"), gostr(o, "connectorId"), gobj(o, "args")
		if p := gobj(o, "parse"); p != nil {
			field := gstr(p, "field")
			s.ParseField = &field
		}
		if r := gobj(o, "retry"); r != nil {
			s.Retry = &Retry{Max: gint(r, "max"), BackoffSec: gint(r, "backoffSec")}
		}
		s.Idempotent = gbool(o, "idempotent")
	case "if":
		s.When, s.Then, s.Else = gstr(o, "when"), gtarget(o, "then"), gtarget(o, "else")
	case "message":
		s.Text, s.To, s.Contact = gstr(o, "text"), gostr(o, "to"), gostr(o, "contact")
		if v, ok := o.Get("signature"); ok {
			b, _ := v.(bool)
			s.Signature = &b
		}
	case "checkin":
		s.Text = gstr(o, "text")
		s.Answers = decodeAnswers(garr(o, "answers"))
		fu := gobj(o, "followUp")
		s.FollowUp = FollowUp{EveryMin: gint(fu, "everyMin"), Max: gint(fu, "max"), Text: gostr(fu, "text")}
		d := gobj(o, "deadline")
		s.Deadline = Deadline{Kind: gstr(d, "kind"), PlusMin: goint(d, "plusMin"), At: gstr(d, "at"), Raw: d}
		if s.Deadline.Kind == "after" {
			s.Deadline.Minutes, s.Deadline.MinutesText = goint(d, "minutes"), gostr(d, "minutes")
		}
		s.MissedText = gostr(o, "missedText")
		if on := gobj(o, "on"); on != nil {
			s.On = jsonx.NewMap[*string]()
			for _, k := range on.Keys() {
				v, _ := on.Get(k)
				if str, ok := v.(string); ok {
					s.On.Set(k, &str)
				} else {
					s.On.Set(k, nil)
				}
			}
		}
		s.OnMissed = gtarget(o, "onMissed")
	case "wait":
		s.Minutes, s.Until, s.PlusMin = goint(o, "minutes"), gostr(o, "until"), goint(o, "plusMin")
	case "prompt":
		s.Prompt = gstr(o, "prompt")
		s.Input = jsonx.NewMap[string]()
		if in := gobj(o, "input"); in != nil {
			for _, k := range in.Keys() {
				v, _ := in.Get(k)
				str, _ := v.(string)
				s.Input.Set(k, str)
			}
		}
		s.Tools = gstrs(o, "tools")
		if s.Tools == nil {
			s.Tools = []string{}
		}
		s.Send, s.Model = gbool(o, "send"), gstr(o, "model")
	case "set":
		s.Scope = gstr(o, "scope")
		s.Values = jsonx.NewMap[string]()
		if vals := gobj(o, "values"); vals != nil {
			for _, k := range vals.Keys() {
				v, _ := vals.Get(k)
				str, _ := v.(string)
				s.Values.Set(k, str)
			}
		}
	case "each":
		s.Items, s.Where, s.Key, s.Max, s.Body = gstr(o, "items"), gostr(o, "where"), gostr(o, "key"), gint(o, "max"), gstr(o, "body")
	}
	return s
}

func decodeDefinition(o *jsonx.Object) *Definition {
	d := &Definition{Raw: o, Schema: gint(o, "schema"), Name: gstr(o, "name"), Description: gostr(o, "description"), TZ: gostr(o, "tz")}
	for _, x := range garr(o, "triggers") {
		d.Triggers = append(d.Triggers, decodeTrigger(x.(*jsonx.Object)))
	}
	for _, x := range garr(o, "steps") {
		d.Steps = append(d.Steps, decodeStep(x.(*jsonx.Object)))
	}
	if l := gobj(o, "limits"); l != nil {
		d.Limits = &DefLimits{RunsPerHour: goint(l, "runsPerHour"), DailyTokens: goint(l, "dailyTokens")}
	}
	return d
}

// normalizeJSON turns any Go value into jsonx values by a JSON round trip.
// jsonx values (and undef) come back as they are.
func normalizeJSON(v any) (any, error) {
	switch v.(type) {
	case nil, bool, float64, string, *jsonx.Object, undefT:
		return v, nil
	case []any:
		ok := true
		for _, x := range v.([]any) {
			switch x.(type) {
			case nil, bool, float64, string, *jsonx.Object:
			default:
				ok = false
			}
		}
		if ok {
			return v, nil
		}
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonx.Parse(b)
}

// ParseDefinition is parseDefinition: the size limit, then the schema.
func ParseDefinition(raw any) (*Definition, []Problem) {
	up := UpgradeDefinition(raw)
	norm, err := normalizeJSON(up)
	if err != nil {
		return nil, []Problem{{Path: "(root)", Message: "The definition isn't JSON."}}
	}
	size := len(CanonicalJSON(norm))
	if size > MaxDefinitionBytes {
		return nil, []Problem{{Path: "(root)", Message: fmt.Sprintf("The definition is %d KB; the limit is 32 KB.", jsRound(float64(size)/1024))}}
	}
	out, issues := definitionSchema.parse(norm)
	if len(issues) > 0 {
		return nil, zodProblems(issues, norm)
	}
	return decodeDefinition(out.(*jsonx.Object)), nil
}

// jsRound is Math.round for non-negative values.
func jsRound(f float64) int64 {
	return int64(mathFloor(f + 0.5))
}

func trimmed(s string) string { return strx.Trim(s) }

package flows

// Save-time checks for a flow definition (validate.ts, RFC flows 4, 6, 7.2):
// the schema, then the graph, every expression and template, templates only
// where allowed, `item` only inside an each body, and every tool resolved
// against the running connectors with its class. The derived WriteCalls list
// is what the owner approves for a version.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// ToolInfo is a tool as the running connectors describe it.
type ToolInfo = connectors.FlowToolInfo

// WriteCall is one side effect a version can cause, as the owner approves it.
type WriteCall = db.FlowWriteCall

// ToolRef is the tool a step or poll names.
type ToolRef struct {
	Tool        string
	ConnectorID string
}

// ContactRef is a saved contact a message step names.
type ContactRef struct {
	ID   string
	Name string
}

// ValidateContext is ValidateContext. Nil funcs mean that check is skipped.
type ValidateContext struct {
	// Resolves a tool step's tool. Nil: tools aren't checked (offline drafts).
	ResolveTool func(ref ToolRef) *ToolInfo
	HookExists  func(hookID string) bool
	// The flow's own chat, and whether another chat may receive messages:
	// "own", "allowed" or "" (not allowed).
	ChatID      string
	ChatAllowed func(chatID string) string
	// A saved contact by name or id.
	ResolveContact func(ref string) *ContactRef
	// Other flows in the chat (nil: not checked).
	FlowNames []string
}

// Validated is Validated.
type Validated struct {
	OK         bool
	Problems   []Problem
	Definition *Definition
	Tools      map[string]ToolInfo
	WriteCalls []WriteCall
}

var isoDateTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}.*)?$`)

// ValidateDefinition is validateDefinition.
func ValidateDefinition(raw any, ctx ValidateContext) (out Validated) {
	def, parseProblems := ParseDefinition(raw)
	out = Validated{Problems: parseProblems, Definition: def, Tools: map[string]ToolInfo{}, WriteCalls: []WriteCall{}}
	if def == nil {
		return out
	}
	problems := []Problem{}
	p := func(path []any, msg string) {
		problems = append(problems, Problem{Path: FormatPath(path), Message: msg})
	}
	defer func() { out.Problems = problems }()

	if def.TZ != nil && !reminders.IsValidZone(*def.TZ) {
		p([]any{"tz"}, fmt.Sprintf(`Unknown time zone "%s".`, *def.TZ))
	}

	// --- ids and targets
	stepIDs := map[string]bool{}
	for i, s := range def.Steps {
		if stepIDs[s.ID] {
			p([]any{"steps", i, "id"}, fmt.Sprintf(`Step id "%s" is used twice.`, s.ID))
		}
		if containsStr(ReservedIDs, s.ID) {
			p([]any{"steps", i, "id"}, fmt.Sprintf(`"%s" is reserved for onError; pick another step id.`, s.ID))
		}
		stepIDs[s.ID] = true
	}
	triggerIDs := map[string]bool{}
	for i, t := range def.Triggers {
		if triggerIDs[t.ID] {
			p([]any{"triggers", i, "id"}, fmt.Sprintf(`Trigger id "%s" is used twice.`, t.ID))
		}
		triggerIDs[t.ID] = true
		if !stepIDs[t.Start] {
			p([]any{"triggers", i, "start"}, fmt.Sprintf(`Trigger "%s" starts at "%s", which isn't a step.`, t.ID, t.Start))
		}
	}
	for i := range def.Steps {
		s := &def.Steps[i]
		if s.Kind == "if" && s.Next.Set {
			p([]any{"steps", i, "next"}, fmt.Sprintf(`Step "%s" is an if: use then/else, not next.`, s.ID))
		}
		for _, e := range EdgesOf(def, s) {
			if !stepIDs[e.To] {
				p([]any{"steps", i, e.Label}, fmt.Sprintf(`Step "%s" points %s to "%s", which isn't a step.`, s.ID, e.Label, e.To))
			}
		}
		if s.Kind == "checkin" && s.On != nil {
			for _, a := range s.On.Keys() {
				var opt *Answer
				for j := range s.Answers {
					if s.Answers[j].ID == a {
						opt = &s.Answers[j]
						break
					}
				}
				if opt == nil {
					p([]any{"steps", i, "on", a}, fmt.Sprintf(`Step "%s": "%s" isn't one of its answers.`, s.ID, a))
				} else if !opt.Closes {
					p([]any{"steps", i, "on", a}, fmt.Sprintf(`Step "%s": "%s" doesn't close the check-in, so it can't branch.`, s.ID, a))
				}
			}
		}
	}
	if len(problems) > 0 {
		return out
	}

	// --- graph
	if cycle := FindCycle(def); cycle != nil {
		p([]any{"steps"}, fmt.Sprintf("The steps loop: %s. Flows can't repeat steps; use a checkin's re-asks or an each instead.", strings.Join(cycle, " -> ")))
	}
	var starts []string
	if len(def.Triggers) > 0 {
		for _, t := range def.Triggers {
			starts = append(starts, t.Start)
		}
	} else {
		starts = []string{def.Steps[0].ID}
	}
	reach := Reachable(def, starts, true)
	for i, s := range def.Steps {
		if !reach[s.ID] {
			p([]any{"steps", i}, fmt.Sprintf(`Step "%s" can't be reached from any trigger. Point a next, branch or trigger start at it, or remove it.`, s.ID))
		}
	}
	var bodyStarts []string
	for _, s := range def.Steps {
		if s.Kind == "each" {
			bodyStarts = append(bodyStarts, s.Body)
		}
	}
	inBody := Reachable(def, bodyStarts, true)
	onMain := Reachable(def, starts, false)
	for i, s := range def.Steps {
		if s.Kind == "each" && inBody[s.ID] {
			p([]any{"steps", i}, fmt.Sprintf(`Step "%s": an each can't run inside another each's body. Use a second flow with an event trigger instead.`, s.ID))
		}
	}

	// --- expressions and templates
	usesItem := func(paths []*Node) bool {
		for _, x := range paths {
			if x.Root == "item" || x.Root == "index" {
				return true
			}
		}
		return false
	}
	for i := range def.Steps {
		s := &def.Steps[i]
		var paths []*Node
		at := func(field ...any) []any { return append([]any{"steps", i}, field...) }
		expr := func(field []any, src string, pathOnly bool) {
			var ast *Node
			var err error
			if pathOnly {
				ast, err = ParsePath(src)
			} else {
				ast, err = ParseExpr(src)
			}
			if err != nil {
				p(at(field...), fmt.Sprintf("%s (in %s)", err.Error(), jsonQuote(strx.Slice16(src, 80))))
				return
			}
			paths = PathsOf(ast, paths)
		}
		tpl := func(field []any, src any) {
			for _, t := range TemplatesIn(src, 0) {
				ps, err := TemplatePaths(t)
				if err != nil {
					p(at(field...), err.Error())
					continue
				}
				paths = append(paths, ps...)
			}
		}
		noTemplate := func(field []any, src *string, what string) {
			if src != nil && HasTemplate(*src) {
				p(at(field...), what+" is a literal; templates aren't allowed there.")
			}
		}
		switch s.Kind {
		case "tool":
			tpl([]any{"args"}, s.Args)
			for _, k := range s.Args.Keys() {
				if HasTemplate(k) {
					p(at("args", k), "Arg names are literals; templates aren't allowed there.")
				}
			}
		case "if":
			expr([]any{"when"}, s.When, false)
		case "message":
			tpl([]any{"text"}, s.Text)
			noTemplate([]any{"to"}, s.To, "message.to")
			noTemplate([]any{"contact"}, s.Contact, "message.contact")
			if s.To != nil && s.Contact != nil {
				p(at("contact"), "Use either to (a chat) or contact (a saved contact), not both.")
			}
			if s.Signature != nil && s.Contact == nil {
				p(at("signature"), "signature only applies to a message to a contact.")
			}
		case "checkin":
			tpl([]any{"text"}, s.Text)
			if s.FollowUp.Text != nil {
				tpl([]any{"followUp", "text"}, *s.FollowUp.Text)
			}
			if s.MissedText != nil {
				tpl([]any{"missedText"}, *s.MissedText)
			}
			for j, a := range s.Answers {
				if a.Reply != nil {
					tpl([]any{"answers", j, "reply"}, *a.Reply)
				}
			}
			if s.Deadline.Kind == "after" && s.Deadline.MinutesText != nil {
				m := *s.Deadline.MinutesText
				if !HasTemplate(m) {
					p(at("deadline", "minutes"), `deadline.minutes is a number or a template like "{{vars.left}}".`)
				}
				tpl([]any{"deadline", "minutes"}, m)
			}
			if s.Deadline.Kind == "at" {
				if !HasTemplate(s.Deadline.At) && !isoDateTimeRe.MatchString(s.Deadline.At) {
					p(at("deadline", "at"), "deadline.at is a template or an ISO date/datetime.")
				}
				tpl([]any{"deadline", "at"}, s.Deadline.At)
			}
		case "wait":
			if (s.Minutes == nil) == (s.Until == nil) {
				p(at(), fmt.Sprintf(`Wait "%s" needs exactly one of minutes or until.`, s.ID))
			}
			if s.Until != nil {
				if !HasTemplate(*s.Until) && !isoDateTimeRe.MatchString(*s.Until) {
					p(at("until"), "until is a template or an ISO date/datetime.")
				}
				tpl([]any{"until"}, *s.Until)
			}
			if s.PlusMin != nil && s.Until == nil {
				p(at("plusMin"), "plusMin only goes with until.")
			}
		case "prompt":
			noTemplate([]any{"prompt"}, &s.Prompt, "The prompt instruction")
			in := jsonx.NewMap[any]()
			for _, k := range s.Input.Keys() {
				v, _ := s.Input.Get(k)
				in.Set(k, v)
			}
			tpl([]any{"input"}, in)
		case "set":
			for _, k := range s.Values.Keys() {
				v, _ := s.Values.Get(k)
				if HasTemplate(v) {
					tpl([]any{"values", k}, v)
				} else {
					expr([]any{"values", k}, v, false)
				}
			}
		case "each":
			expr([]any{"items"}, s.Items, true)
			if s.Where != nil {
				expr([]any{"where"}, *s.Where, false)
			}
			if s.Key != nil {
				if !HasTemplate(*s.Key) {
					p(at("key"), `key is a template, like "{{item.id}}".`)
				}
				tpl([]any{"key"}, *s.Key)
			}
		}
		if s.Kind != "each" && usesItem(paths) && (!inBody[s.ID] || onMain[s.ID]) {
			p(at(), fmt.Sprintf(`Step "%s" reads item/index but isn't only inside an each body.`, s.ID))
		}
		if s.Kind == "each" {
			if ast, err := ParsePath(s.Items); err == nil {
				for _, x := range PathsOf(ast, nil) {
					if x.Root == "item" {
						p(at("items"), "items can't read item; it's the list the each walks.")
						break
					}
				}
			}
		}
	}

	// --- triggers
	for i, t := range def.Triggers {
		switch t.Kind {
		case "chat":
			if (t.Command == nil || *t.Command == "") == (t.Phrases == nil) {
				p([]any{"triggers", i}, fmt.Sprintf(`Chat trigger "%s" needs exactly one of command or phrases.`, t.ID))
			}
			if t.Command != nil && *t.Command != "" && containsStr(chat.BuiltinCommands, *t.Command) {
				p([]any{"triggers", i, "command"}, *t.Command+" is a built-in command; pick another.")
			}
		case "webhook":
			if ctx.HookExists != nil && !ctx.HookExists(t.HookID) {
				p([]any{"triggers", i, "hookId"}, fmt.Sprintf("No hook %s. Create one on the Hooks page first.", t.HookID))
			}
		case "event":
			if t.Flow == "self" && t.Event == "run.failed" {
				p([]any{"triggers", i}, "A flow can't subscribe to its own run.failed.")
			}
			if t.Flow != "self" && ctx.FlowNames != nil && !containsStr(ctx.FlowNames, t.Flow) {
				p([]any{"triggers", i, "flow"}, fmt.Sprintf(`No flow named "%s" in this chat.`, t.Flow))
			}
		case "poll":
			if ctx.ResolveTool != nil {
				info := ctx.ResolveTool(ToolRef{Tool: t.Tool, ConnectorID: deref(t.ConnectorID, "")})
				if info == nil {
					cid := ""
					if t.ConnectorID != nil {
						cid = *t.ConnectorID + "."
					}
					p([]any{"triggers", i, "tool"}, fmt.Sprintf("No enabled tool %s%s. Find it with palui__search_tools.", cid, t.Tool))
				} else if info.Class != "read" {
					p([]any{"triggers", i, "tool"}, fmt.Sprintf("%s is %s; a poll only calls read tools.", info.Name, info.Class))
				}
			}
			for range TemplatesIn(t.Args, 0) {
				p([]any{"triggers", i, "args"}, "Poll args are literals; templates aren't allowed there.")
			}
		}
	}

	// `deadline: next` needs the next occurrence of a schedule trigger.
	for i := range def.Steps {
		s := &def.Steps[i]
		if s.Kind != "checkin" || s.Deadline.Kind != "next" {
			continue
		}
		var from []Trigger
		for _, t := range def.Triggers {
			if Reachable(def, []string{t.Start}, true)[s.ID] {
				from = append(from, t)
			}
		}
		bad := len(from) == 0
		for _, t := range from {
			if t.Kind != "schedule" {
				bad = true
			}
		}
		if bad {
			p([]any{"steps", i, "deadline"}, fmt.Sprintf(`Check-in "%s" uses deadline "next", which only works when every trigger reaching it is a schedule. Use {kind:"after", minutes:N} or {kind:"at", at:"<template>", plusMin?}.`, s.ID))
		}
	}

	// --- tools and side effects
	ownChat := ctx.ChatID
	for i := range def.Steps {
		s := &def.Steps[i]
		if s.Kind == "tool" && ctx.ResolveTool != nil {
			info := ctx.ResolveTool(ToolRef{Tool: s.Tool, ConnectorID: deref(s.ConnectorID, "")})
			if info == nil {
				cid := ""
				if s.ConnectorID != nil {
					cid = *s.ConnectorID + "."
				}
				p([]any{"steps", i, "tool"}, fmt.Sprintf("No enabled tool %s%s. Find it with palui__search_tools.", cid, s.Tool))
				continue
			}
			out.Tools[s.ID] = *info
			if info.Class == "destructive" {
				p([]any{"steps", i, "tool"}, info.Name+" is destructive. Flows never run destructive tools; ask with a checkin and do it from chat instead.")
				continue
			}
			if info.Class == "write" {
				out.WriteCalls = append(out.WriteCalls, WriteCall{StepID: s.ID, Kind: "tool", ConnectorID: info.ConnectorID, ConnectorType: info.ConnectorType, Tool: info.Tool, Class: info.Class, Args: s.Args})
			}
		}
		if s.Kind == "prompt" && ctx.ResolveTool != nil {
			for j, name := range s.Tools {
				if name == "*" {
					continue
				}
				info := ctx.ResolveTool(ToolRef{Tool: name})
				if info == nil {
					p([]any{"steps", i, "tools", j}, fmt.Sprintf("No enabled tool %s.", name))
				} else if info.Class == "destructive" {
					p([]any{"steps", i, "tools", j}, info.Name+" is destructive; prompt steps never get destructive tools.")
				}
			}
		}
		if s.Kind == "message" && s.Contact != nil && !HasTemplate(*s.Contact) {
			var c *ContactRef
			if ctx.ResolveContact != nil {
				c = ctx.ResolveContact(*s.Contact)
			}
			if c == nil {
				p([]any{"steps", i, "contact"}, fmt.Sprintf("No saved contact %s. Contacts are added from chat by sending them a message, or on the Outbound page.", *s.Contact))
			} else {
				w := WriteCall{StepID: s.ID, Kind: "message", To: "contact:" + c.ID, Label: c.Name}
				if s.Signature != nil && !*s.Signature {
					w.Signature = ptr(false)
				}
				out.WriteCalls = append(out.WriteCalls, w)
			}
		}
		if s.Kind == "message" && s.To != nil && *s.To != ownChat {
			ok := "allowed"
			if ctx.ChatAllowed != nil {
				ok = ctx.ChatAllowed(*s.To)
			}
			if ok == "" {
				p([]any{"steps", i, "to"}, fmt.Sprintf("Chat %s isn't one of the owner's chats or an allowed chat.", *s.To))
			} else if ok == "allowed" {
				out.WriteCalls = append(out.WriteCalls, WriteCall{StepID: s.ID, Kind: "message", To: *s.To})
			}
		}
	}

	var kept []Problem
	for _, x := range problems {
		if x.Message != "" {
			kept = append(kept, x)
		}
	}
	problems = kept
	if problems == nil {
		problems = []Problem{}
	}
	out.OK = len(problems) == 0
	return out
}

// DescribeWriteCall is describeWriteCall: "bills.mark_paid(id: {{item.id}})".
func DescribeWriteCall(w WriteCall) string {
	if w.Kind == "message" {
		if !strings.HasPrefix(w.To, "contact:") {
			return "kirim pesan ke chat " + w.To
		}
		name := w.Label
		if name == "" {
			name = w.To[8:]
		}
		unsigned := ""
		if w.Signature != nil && !*w.Signature {
			unsigned = " (tanpa tanda tangan)"
		}
		return "kirim pesan ke kontak " + name + unsigned
	}
	var parts []string
	if w.Args != nil {
		for _, k := range w.Args.Keys() {
			v, _ := w.Args.Get(k)
			if s, ok := v.(string); ok {
				parts = append(parts, k+": "+s)
			} else {
				parts = append(parts, k+": "+CompactJSON(v))
			}
		}
	}
	args := strings.Join(parts, ", ")
	if strx.Len16(args) > 200 {
		args = strx.Slice16(args, 199) + "…"
	}
	return fmt.Sprintf("%s.%s(%s)", w.ConnectorID, w.Tool, args)
}

// SameWriteCalls is sameWriteCalls: same calls, args templates and classes.
func SameWriteCalls(a, b []WriteCall) bool {
	key := func(w WriteCall) string {
		if w.Kind == "message" {
			s := "m:" + w.To
			if w.Signature != nil && !*w.Signature {
				s += ":unsigned"
			}
			return s
		}
		var args any = w.Args
		if w.Args == nil {
			args = jsonx.NewMap[any]()
		}
		return fmt.Sprintf("t:%s:%s:%s:%s", w.ConnectorID, w.Tool, w.Class, CanonicalJSON(args))
	}
	keys := func(list []WriteCall) []string {
		out := make([]string, len(list))
		for i, w := range list {
			out[i] = key(w)
		}
		sort.SliceStable(out, func(i, j int) bool { return strx.Compare16(out[i], out[j]) < 0 })
		return out
	}
	ka, kb := keys(a), keys(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// ResolverFrom is resolverFrom: by namespaced name, or by connector id plus
// the bare tool name.
func ResolverFrom(tools []ToolInfo) func(ref ToolRef) *ToolInfo {
	return func(ref ToolRef) *ToolInfo {
		if ref.ConnectorID != "" {
			for i := range tools {
				if tools[i].ConnectorID == ref.ConnectorID && tools[i].Tool == ref.Tool {
					return &tools[i]
				}
			}
			for i := range tools {
				if tools[i].Name == ref.Tool && tools[i].ConnectorID == ref.ConnectorID {
					return &tools[i]
				}
			}
			return nil
		}
		for i := range tools {
			if tools[i].Name == ref.Tool {
				return &tools[i]
			}
		}
		return nil
	}
}

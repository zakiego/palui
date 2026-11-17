// Package flows is the flows connector (connectors/src/flows): the agent
// builds a flow from chat (palui__search_tools, check_flow, test_flow, then
// create_flow), runs, pauses and explains it. Every tool works on the turn's
// chat only, and the tools are only offered in the owner's own chats, never in
// shared or guest turns, jobs or hooks. flows__recap is also a flow tool step.
package flows

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	remindersconn "github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:          "flows",
	Kind:        "skill",
	Name:        "Flows",
	Icon:        "workflow",
	Summary:     "Automations built from chat.",
	Description: "A flow is a small automation: one or more triggers (a schedule, a webhook, a chat command, a value that changes, another flow's event, or a manual run) and a list of steps (call a tool, branch on a condition, set a variable, send a message, ask a check-in, wait, run a prompt, or repeat for each item in a list). Steps pass data with templates and decide with simple conditions, so nothing calls the model unless a step asks for it, and every run logs what each step saw and did. Ask in chat (\"tiap pagi kirim jadwal sholat, cuaca sama pengingat hari ini\") and Palui builds it, dry-runs it, and shows you the result before saving.",
	Builtin:     true,
	Setup:       []connectors.SetupStep{},
	PauseNote:   "Pausing hides the flow tools. Flows already saved keep running; pause one from chat to stop it.",
	Docs: &connectors.Docs{
		Commands: []string{"tiap pagi jam 6 kirim jadwal sholat, cuaca, sama pengingat hari ini", "kalau PR di palui di-merge, cek CI-nya terus kabari aku", "kenapa flow briefing tadi pagi nggak jalan?"},
		Notes: []string{
			"Tool and condition steps run without the model. Only a prompt step calls it, with fetched data in a separate block it's told never to follow.",
			"Write tools are approved once per flow version. Tainted arguments, a changed tool class, or a tool set to ask every run still ask each time. Destructive tools never run in a flow.",
			"Every run is logged with each step's inputs, outputs and the branch it took, including runs that were skipped and why.",
		},
	},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

// FlowHelp is FLOW_HELP, the definition guide in create_flow's description.
const FlowHelp = `A flow definition is JSON: { name, description?, tz?, triggers: [...], steps: [...], limits?: { runsPerHour?, dailyTokens? } }.
Triggers (each has id, kind, start = first step id):
- schedule: { source: <time source: {kind:"times", times:[{time:"06:00", label}]} | {kind:"rrule", rrule, start:"YYYY-MM-DDTHH:mm"} | {kind:"tool", connectorId, tool, args, refreshAt, items|list}>, days?: ["MO".."SU"], lateGraceMin? }. trigger.* = at, time, date, label, key, nextAt (the next occurrence, epoch ms).
- webhook: { hookId } (an existing hook; its mode becomes flow). trigger.payload is untrusted.
- chat: { command: "/beres" } or { phrases: ["udah bayar"] }, the owner's messages in this chat. trigger.text, trigger.args.
- poll: { tool, connectorId?, args, everyMin: 5..1440, select: "path.in.result", fire: "change"|"truthy" }. trigger.value, trigger.previous.
- event: { event: "checkin.answered"|"checkin.missed"|"run.failed"|"run.done", flow: "<flow name>"|"self" }.
- Every flow can also run manually (run_flow); trigger.args are its args.
Names: trigger ids, step ids and set value names are lowercase letters, digits and _, starting with a letter, max 32 ("ask_1", "last_at"; not "L" or "Ask1"). Answer ids are lowercase letters, digits and _, max 20.
Steps (each has id, kind, optional next (step id or null to end; default the next in the array), name, onError: "fail"|"continue"|<step id>, timeoutSec):
- tool: { tool: "<connectorId>__<tool>" (as palui__search_tools shows), args, parse?: {field:"text", as:"json"}, retry?: {max, backoffSec} }. Output steps.<id>.data (or .error with onError).
- if: { when: "<expression>", then: <id|null>, else: <id|null> }.
- message: { text, to?: <another chat id the owner allowed>, contact?: <a saved contact name from outbound__list_contacts>, signature?: false }. A contact message is approved once per version, must use fixed text (no tool or webhook data), and is how conditional "kabarin X kalau ..." sends work. Palui appends the owner's signature to it; don't write one. signature: false only when the owner explicitly asked for that message to go without it.
- checkin: { text, answers: [{id, label, words?, closes}], followUp: {everyMin, max, text?}, deadline, missedText?, on?: {<answer id>: <step id>}, onMissed? }. Output answer, label, missed (skipped too when the deadline had already passed).
  deadline (default next, max 7 days): {kind:"next", plusMin?} the schedule's next occurrence (plusMin:-10 = 10 min before it) | {kind:"after", minutes: N or "{{template}}"} | {kind:"at", at: "<template or ISO>", plusMin?} a computed time, e.g. {kind:"at", at:"{{steps.nextp.data.next.timestamp}}", plusMin:-10}. A deadline already past skips the ask silently and goes onMissed. Re-asks stop at the deadline.
- wait: { minutes } or { until: "<template>", plusMin? } (max 7 days).
- prompt: { prompt: "<literal instruction, no templates>", input: { name: "{{...}}" }, tools: [], send: false, model: "main"|"fast" }. Output text, silent.
- set: { scope: "run"|"flow", values: { name: "<expression or template>" } } -> vars.<name> or flow.<name> (flow ones persist). Times in expressions are epoch ms: "trigger.nextAt - 10 * 60000" is 10 minutes before the next occurrence.
- each: { items: "<path to a list>", where?: "<expression on item>", key?: "{{item.id}}", max: 20, body: <step id> } runs body once per item, deduped by key across runs.
Templates "{{path | filter}}" go only in tool args, message/check-in text, prompt input, set values, wait.until, checkin deadline.at and deadline.minutes, each.key. Paths start with trigger, steps, vars, flow, item, index, now (now.date, now.time, now.weekday), run (run.followUps: the re-ask number, for check-in followUp.text). Filters: time, date, json, default("x"), join(", "), lines("field"), upper, lower, round(n), truncate(n).
Expressions (if.when, set, each.where): and or not, == != < <= > >=, + - * /, functions exists empty contains startsWith len lower any count number round min max choose(cond, a, b) date daysUntil hoursUntil minutesSince weekday today.
Destructive tools are never allowed. Write tools need the owner's approval once per version.`

// DraftFlowID is the flow id a draft is checked under, before it has one.
const DraftFlowID = "draft"

func localDateTime() *zod.StringS {
	return zod.String().Regex(remindersconn.LocalDateTimeRe, remindersconn.LocalDateTimeSource, "Use YYYY-MM-DDTHH:mm with no offset")
}

func definitionInput() *zod.RecordS {
	return zod.Record(zod.Unknown()).Describe("The flow definition (see create_flow).")
}

var runStatuses = []string{"queued", "running", "waiting", "awaiting_approval", "done", "failed", "skipped", "cancelled"}

func fail(err error) (any, error) {
	var fe *flows.FlowError
	var te *reminders.TimeError
	if errors.As(err, &fe) {
		o := kit.O("ok", false, "error", fe.Msg)
		if len(fe.Problems) > 0 {
			o.Set("problems", problemsJSON(fe.Problems))
		}
		return o, nil
	}
	if errors.As(err, &te) {
		return kit.Fail(te.Msg), nil
	}
	return nil, err
}

func problemsJSON(ps []flows.Problem) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = kit.O("path", p.Path, "message", p.Message)
	}
	return out
}

// Deps are FlowToolDeps: the clock, the flow tool caller and the engine.
type Deps struct {
	Clock  func() int64
	Tools  connectors.FlowToolCaller
	Engine *flows.Engine
}

func zoneFor(q db.Q, turn connectors.TurnInfo) (string, error) {
	if turn.TZ != "" {
		return turn.TZ, nil
	}
	return settings.Timezone(q)
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Tools is flowTools(db, turn, deps).
func Tools(d *db.DB, turn connectors.TurnInfo, deps Deps) *connectors.ToolSet {
	chatID := turn.ChatID
	clock := deps.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	engine := deps.Engine
	if engine == nil {
		engine = flows.NewEngine(flows.EngineOptions{DB: d, Log: logx.Silent, Clock: clock, Tools: deps.Tools})
	}
	var agentID *string
	if turn.AgentID != "" {
		a := turn.AgentID
		agentID = &a
	}

	validateCtx := func(ctx context.Context) (*flows.ValidateContext, error) {
		vc := &flows.ValidateContext{
			HookExists: func(id string) bool {
				var x string
				return d.QueryRow("SELECT id FROM hooks WHERE id = ?", id).Scan(&x) == nil
			},
			ChatID:         chatID,
			ChatAllowed:    flows.ChatAccess(d, chatID, clock()),
			ResolveContact: flows.ContactResolver(d, agentID, clock()),
		}
		if deps.Tools != nil {
			t := turn
			t.Trigger, t.FlowID = "flow", DraftFlowID
			list, err := deps.Tools.List(ctx, t)
			if err != nil {
				return nil, err
			}
			vc.ResolveTool = flows.ResolverFrom(list)
		}
		return vc, nil
	}
	own := func(ref string) (*db.Flow, any, error) {
		f, err := flows.ResolveFlow(d, chatID, ref)
		if err != nil {
			return nil, nil, err
		}
		if f == nil {
			return nil, kit.Fail(fmt.Sprintf("No flow \"%s\" in this chat. Call list_flows for the ids.", ref)), nil
		}
		return f, nil, nil
	}
	saved := func(r *flows.SaveResult, now int64, verb string) (any, error) {
		times, err := flows.PreviewTimes(d, r.Flow, 3, now)
		if err != nil {
			return nil, err
		}
		next := make([]any, len(times))
		for i, t := range times {
			label := ""
			if t.Label != "" {
				label = ": " + t.Label
			}
			next[i] = fmt.Sprintf("%s (%s%s)", t.When, t.Trigger, label)
		}
		msg := fmt.Sprintf("Flow \"%s\" %s.", r.Flow.Name, verb)
		if r.NeedsApproval {
			msg = fmt.Sprintf("Flow \"%s\" %s. Ada aksi yang mengubah data, jadi Zaki perlu setujui dulu (prompt udah dikirim). Bagian read-only tetap jalan.", r.Flow.Name, verb)
		}
		calls := make([]any, len(r.WriteCalls))
		for i, w := range r.WriteCalls {
			calls[i] = flows.DescribeWriteCall(w)
		}
		summary, err := flows.FlowSummaryView(d, r.Flow, now)
		if err != nil {
			return nil, err
		}
		o := kit.O("ok", true, "id", r.Flow.ID, "version", r.Version.N, "message", msg, "next", next, "needsApproval", r.NeedsApproval, "writeCalls", calls)
		if len(r.Diff) > 0 {
			diff := make([]any, len(r.Diff))
			for i, x := range r.Diff {
				diff[i] = x
			}
			o.Set("diff", diff)
		}
		o.Set("flow", summary)
		return o, nil
	}
	note := func(in kit.Input) *string {
		if s, ok := in.Str("note"); ok {
			return &s
		}
		return nil
	}

	set := connectors.NewToolSet()
	set.Set("create_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Create a flow",
		Tool: kit.Tool("Create an automation (a flow) in this chat. Use it when the logic is deterministic (fetch, check, send, ask), instead of a job. Always check_flow and test_flow first, show the owner the triggers, the next times and any write calls, then save. Find tools for tool steps with palui__search_tools (for: \"flow\"), dry-run a tool step alone to learn its output shape, then write conditions against the real output. Never put fetched text in a prompt instruction; pass it in input.\n\n"+FlowHelp,
			zod.Object(zod.F("definition", definitionInput())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				vc, err := validateCtx(ctx)
				if err != nil {
					return nil, err
				}
				def, _ := in.Get("definition")
				r, err := flows.CreateFlow(d, flows.SaveInput{ChatID: chatID, Definition: def, CreatedBy: "agent:" + turn.RunID, Validate: vc, AgentID: agentID}, now)
				if err != nil {
					return fail(err)
				}
				return saved(r, now, "dibuat")
			}),
	})
	set.Set("update_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Change a flow",
		Tool: kit.Tool(`Change a flow: a new version from a full definition, or a JSON merge patch on the current one. In a patch, "steps" and "triggers" may be objects keyed by id ({"steps": {"send": {"text": "..."}, "old": null}}) to change, add or remove one entry. Returns the diff and whether the owner must approve again (only when the write calls change).`,
			zod.Object(
				zod.F("id", zod.String().Describe("Flow id or name from list_flows.")),
				zod.F("definition", definitionInput().Optional()),
				zod.F("patch", zod.Record(zod.Unknown()).Optional()),
				zod.F("note", zod.String().Max(200).Optional().Describe("Why it changed, kept with the version.")),
			),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				f, refusal, err := own(in.S("id"))
				if err != nil || refusal != nil {
					return refusal, err
				}
				vc, err := validateCtx(ctx)
				if err != nil {
					return nil, err
				}
				var def, patch any
				if v, ok := in.Get("definition"); ok {
					def = v
				}
				if v, ok := in.Get("patch"); ok {
					patch = v
				}
				r, err := flows.UpdateFlow(d, f.ID, flows.UpdateInput{Definition: def, Patch: patch, Note: note(in), CreatedBy: "agent:" + turn.RunID, Validate: vc}, now)
				if err != nil {
					return fail(err)
				}
				if !r.Changed {
					return kit.O("ok", true, "message", "Nggak ada yang berubah.", "version", r.Version.N), nil
				}
				return saved(r, now, fmt.Sprintf("diubah ke versi %d", r.Version.N))
			}),
	})
	set.Set("check_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "Check a flow",
		Tool: kit.Tool("Validate a draft definition (or a saved flow) without saving: problems as sentences with their JSON path, the resolved tools with their class and trust, and the write calls the owner would approve.",
			zod.Object(zod.F("definition", definitionInput().Optional()), zod.F("id", zod.String().Optional())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				def, hasDef := in.Get("definition")
				if !hasDef || !jsonx.Truthy(def) {
					def = nil
					if id := in.S("id"); id != "" {
						f, refusal, err := own(id)
						if err != nil || refusal != nil {
							return refusal, err
						}
						v, err := flows.CurrentVersion(d, f)
						if err != nil {
							return nil, err
						}
						parsed, err := flows.DefinitionOf(v)
						if err != nil {
							return nil, err
						}
						def = parsed.Raw
					}
				}
				if def == nil {
					return kit.Fail("Pass a definition or an id."), nil
				}
				vc, err := validateCtx(ctx)
				if err != nil {
					return nil, err
				}
				r := flows.ValidateDefinition(def, *vc)
				toolsOut := kit.O()
				if r.Definition != nil {
					for _, s := range r.Definition.Steps {
						if t, ok := r.Tools[s.ID]; ok {
							fa := t.FlowApproval
							if fa == "" {
								fa = "version"
							}
							toolsOut.Set(s.ID, kit.O("tool", t.Name, "class", t.Class, "untrusted", t.Untrusted, "flowApproval", fa))
						}
					}
				}
				calls := make([]any, len(r.WriteCalls))
				for i, w := range r.WriteCalls {
					calls[i] = flows.DescribeWriteCall(w)
				}
				return kit.O("ok", r.OK, "problems", problemsJSON(r.Problems), "tools", toolsOut, "writeCalls", calls, "needsApproval", len(r.WriteCalls) > 0), nil
			}),
	})
	set.Set("test_flow", connectors.PaluiTool{
		Data:      connectors.DataChat,
		Class:     "read",
		Untrusted: true,
		Title:     "Dry-run a flow",
		Tool: kit.Tool(`Dry-run a flow (saved, by id) or a draft definition: read tools are called for real so you see their output, while write tools, messages, check-ins and prompts are stubbed and show what they would do. Waits are skipped, an each runs 3 items. Override a stub to walk a branch: stubs: {"ask": {"answer": "done"}, "cheer": "teks"}. trigger is a fake trigger payload (e.g. {"payload": {...}} for a webhook).`,
			zod.Object(
				zod.F("id", zod.String().Optional()),
				zod.F("definition", definitionInput().Optional()),
				zod.F("trigger", zod.Record(zod.Unknown()).Optional()),
				zod.F("stubs", zod.Record(zod.Unknown()).Optional()),
				zod.F("start", zod.String().Optional().Describe("A trigger id to start from (default the first).")),
				zod.F("step", zod.String().Optional().Describe("Run from this step instead (to try one tool step alone).")),
			),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				input := flows.DryRunInput{ChatID: chatID, TriggerID: in.S("start"), Start: in.S("step"), Now: now}
				if o, ok := in.Obj("trigger"); ok {
					input.Trigger = o
				}
				if o, ok := in.Obj("stubs"); ok {
					input.Stubs = o
				}
				if def, ok := in.Get("definition"); ok && jsonx.Truthy(def) {
					vc, err := validateCtx(ctx)
					if err != nil {
						return nil, err
					}
					v := flows.ValidateDefinition(def, *vc)
					if !v.OK || v.Definition == nil {
						return kit.O("ok", false, "error", "The draft has problems; fix them first (check_flow).", "problems", problemsJSON(v.Problems)), nil
					}
					input.Definition, input.WriteCalls = v.Definition, v.WriteCalls
				} else if id := in.S("id"); id != "" {
					f, refusal, err := own(id)
					if err != nil || refusal != nil {
						return refusal, err
					}
					input.Flow = f
				} else {
					return kit.Fail("Pass an id or a definition."), nil
				}
				tz, err := zoneFor(d, turn)
				if err != nil {
					return nil, err
				}
				input.TZ = tz
				res, err := flows.DryRunFlow(engine, input)
				if err != nil {
					return fail(err)
				}
				b, err := jsonx.Marshal(res)
				if err != nil {
					return nil, err
				}
				body, err := jsonx.ParseObject(b)
				if err != nil {
					return nil, err
				}
				return kit.Spread(kit.O("ok", true), body), nil
			}),
	})
	set.Set("list_flows", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "List flows",
		Tool: kit.Tool("This chat's flows with their triggers, next times, last run, approval state and pause state.",
			zod.Object(zod.F("filter", zod.Default(zod.Enum("all", "active", "paused", "failing"), "all"))),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				rows, err := flows.ListFlows(d, chatID)
				if err != nil {
					return nil, err
				}
				filter := in.S("filter")
				var picked []db.Flow
				for _, f := range rows {
					switch filter {
					case "active":
						if !f.Enabled {
							continue
						}
					case "paused":
						if f.Enabled {
							continue
						}
					case "failing":
						if !(f.ConsecutiveFailures > 0 || (f.LastError != nil && *f.LastError != "")) {
							continue
						}
					}
					picked = append(picked, f)
				}
				list := make([]any, len(picked))
				for i := range picked {
					v, err := flows.FlowSummaryView(d, &picked[i], now)
					if err != nil {
						return nil, err
					}
					list[i] = v
				}
				return kit.O("ok", true, "count", len(picked), "flows", list, "message", kit.If(len(picked) == 0, "Belum ada flow.")), nil
			}),
	})
	set.Set("get_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "Show a flow",
		Tool: kit.Tool("One flow in full: its definition (current or an older version), versions, approval state, trigger state and flow variables.",
			zod.Object(zod.F("id", zod.String()), zod.F("version", zod.Int().Min(1).Optional())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				f, refusal, err := own(in.S("id"))
				if err != nil || refusal != nil {
					return refusal, err
				}
				n, hasN := in.Int("version")
				view, err := flows.FlowDetailView(d, f, clock(), n)
				if err != nil {
					return nil, err
				}
				if view == nil {
					v := "undefined"
					if hasN {
						v = fmt.Sprint(n)
					}
					return kit.Fail(fmt.Sprintf("No version %s.", v)), nil
				}
				return kit.O("ok", true, "flow", view), nil
			}),
	})
	set.Set("pause_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Pause or resume a flow",
		Tool: kit.Tool("Pause a flow (optionally until a local date-time, then it resumes by itself) or resume it. Resuming starts from now: what was due while paused doesn't run.",
			zod.Object(zod.F("id", zod.String()), zod.F("paused", zod.Bool()), zod.F("until", localDateTime().Optional())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				f, refusal, err := own(in.S("id"))
				if err != nil || refusal != nil {
					return refusal, err
				}
				paused := in.True("paused")
				var until *int64
				if u := in.S("until"); paused && u != "" {
					at, err := reminders.LocalToUTC(u, f.TZ)
					if err != nil {
						return fail(err)
					}
					until = &at
				}
				r, err := flows.SetFlowPaused(d, f.ID, paused, until, now, "")
				if err != nil {
					return fail(err)
				}
				msg := fmt.Sprintf("Flow \"%s\" jalan lagi.", r.Name)
				if paused {
					msg = fmt.Sprintf("Flow \"%s\" dijeda.", r.Name)
					if r.PausedUntil != nil && *r.PausedUntil != 0 {
						msg = fmt.Sprintf("Flow \"%s\" dijeda sampai %s.", r.Name, reminders.FormatWhen(*r.PausedUntil, r.TZ, now))
					}
				}
				return kit.O("ok", true, "message", msg), nil
			}),
	})
	set.Set("delete_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "destructive",
		Title: "Delete a flow",
		Tool: kit.Tool("Delete a flow with its versions and run log. To stop it for a while, use pause_flow instead.",
			zod.Object(zod.F("id", zod.String())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				f, refusal, err := own(in.S("id"))
				if err != nil || refusal != nil {
					return refusal, err
				}
				if err := flows.DeleteFlow(d, f.ID, clock()); err != nil {
					return nil, err
				}
				return kit.O("ok", true, "message", fmt.Sprintf("Flow \"%s\" dihapus.", f.Name)), nil
			}),
	})
	set.Set("run_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Run a flow now",
		Tool: kit.Tool("Run a flow now, for real (messages go out, approved writes run). trigger picks which trigger's start step to use; args become trigger.args. To try it without side effects, use test_flow.",
			zod.Object(zod.F("id", zod.String()), zod.F("trigger", zod.String().Optional()), zod.F("args", zod.Record(zod.Unknown()).Optional())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				f, refusal, err := own(in.S("id"))
				if err != nil || refusal != nil {
					return refusal, err
				}
				v, err := flows.CurrentVersion(d, f)
				if err != nil {
					return nil, err
				}
				def, err := flows.DefinitionOf(v)
				if err != nil {
					return nil, err
				}
				var t *flows.Trigger
				if name := in.S("trigger"); name != "" {
					t = def.Trigger(name)
					if t == nil {
						ids := make([]string, len(def.Triggers))
						for i, x := range def.Triggers {
							ids[i] = x.ID
						}
						list := strings.Join(ids, ", ")
						if list == "" {
							list = "none"
						}
						return kit.Fail(fmt.Sprintf("No trigger \"%s\". Triggers: %s.", name, list)), nil
					}
				}
				args, ok := in.Obj("args")
				if !ok {
					args = jsonx.NewMap[any]()
				}
				start := flows.StartInput{FlowID: f.ID, TriggerKind: "manual", RunKey: "manual:" + uuid(), Payload: kit.O("kind", "manual", "args", args), Now: now}
				if t != nil {
					id := t.ID
					start.TriggerID, start.Start = &id, t.Start
				}
				r, err := engine.StartRun(start)
				if err != nil {
					return nil, err
				}
				if r.Run == nil || r.Skipped != "" {
					if r.Skipped == "paused" {
						return kit.Fail(fmt.Sprintf("Flow \"%s\" lagi dijeda.", f.Name)), nil
					}
					return kit.Fail(fmt.Sprintf("Run skipped: %s.", skippedText(r.Skipped))), nil
				}
				return kit.O("ok", true, "runId", r.Run.ID, "message", fmt.Sprintf("Flow \"%s\" dijalankan. Cek hasilnya dengan get_run.", f.Name)), nil
			}),
	})
	set.Set("flow_history", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "Flow history",
		Tool: kit.Tool(`Runs of a flow (or every flow in this chat): status, skip reason, the check-in answer, and counts by status and answer. Use it for "berapa kali aku lupa minggu ini?" or to find a run id.`,
			zod.Object(
				zod.F("id", zod.String().Optional()),
				zod.F("days", zod.Default(zod.Int().Min(1).Max(180), float64(7))),
				zod.F("status", zod.Enum(runStatuses...).Optional()),
				zod.F("limit", zod.Default(zod.Int().Min(1).Max(200), float64(30))),
			),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				tz, err := zoneFor(d, turn)
				if err != nil {
					return nil, err
				}
				flowID := ""
				if id := in.S("id"); id != "" {
					f, refusal, err := own(id)
					if err != nil || refusal != nil {
						return refusal, err
					}
					flowID, tz = f.ID, f.TZ
				}
				visible, err := flows.ListFlows(d, chatID)
				if err != nil {
					return nil, err
				}
				names := map[string]string{}
				for _, f := range visible {
					names[f.ID] = f.Name
				}
				days := int64(7)
				if n, ok := in.Int("days"); ok {
					days = n
				}
				since := now - days*24*3600_000
				rq := flows.RunQuery{ChatID: chatID, FlowID: flowID, Since: &since, Limit: 2000}
				if s := in.S("status"); s != "" {
					rq.Status = []string{s}
				}
				all, err := flows.ListRuns(d, rq)
				if err != nil {
					return nil, err
				}
				var rows []db.FlowRun
				for _, r := range all {
					if _, ok := names[r.FlowID]; ok {
						rows = append(rows, r)
					}
				}
				limit := int64(30)
				if n, ok := in.Int("limit"); ok {
					limit = n
				}
				var runs []any
				for i := range rows {
					if int64(i) >= limit {
						break
					}
					v, err := flows.RunView(d, &rows[i], tz, now, false)
					if err != nil {
						return nil, err
					}
					runs = append(runs, kit.Spread(kit.O("flow", names[rows[i].FlowID]), v))
				}
				if runs == nil {
					runs = []any{}
				}
				return kit.O("ok", true, "summary", SummaryJSON(flows.SummarizeRuns(rows)), "runs", runs), nil
			}),
	})
	set.Set("get_run", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "Show a flow run",
		Tool: kit.Tool("The step log of one flow run: each step's inputs, outputs, the branch it took (with the values an if compared), errors and timing.",
			zod.Object(zod.F("runId", zod.String())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				runID := in.S("runId")
				r, err := flows.GetRun(d, runID)
				if err != nil {
					return nil, err
				}
				var f *db.Flow
				if r != nil {
					if f, err = flows.ResolveFlow(d, chatID, r.FlowID); err != nil {
						return nil, err
					}
				}
				if r == nil || r.ChatID != chatID {
					return kit.Fail(fmt.Sprintf("No run %s in this chat.", runID)), nil
				}
				tz := ""
				var name any
				if f != nil {
					tz, name = f.TZ, f.Name
				} else if tz, err = zoneFor(d, turn); err != nil {
					return nil, err
				}
				v, err := flows.RunView(d, r, tz, clock(), true)
				if err != nil {
					return nil, err
				}
				return kit.O("ok", true, "flow", name, "run", v), nil
			}),
	})
	set.Set("cancel_run", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Cancel a flow run",
		Tool: kit.Tool("Cancel a queued or waiting flow run (a check-in that should stop asking, a long wait).",
			zod.Object(zod.F("runId", zod.String())),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				runID := in.S("runId")
				r, err := flows.GetRun(d, runID)
				if err != nil {
					return nil, err
				}
				if r == nil || r.ChatID != chatID {
					return kit.Fail(fmt.Sprintf("No run %s in this chat.", runID)), nil
				}
				res, err := d.Exec("UPDATE flow_runs SET status = 'cancelled', error = 'Cancelled from chat.', wait = NULL, wake_at = NULL, ended_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued', 'waiting', 'awaiting_approval')", now, now, runID)
				if err != nil {
					return nil, err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					return kit.O("ok", true, "message", "Run dibatalkan."), nil
				}
				return kit.Fail(fmt.Sprintf("That run is %s; only queued or waiting runs can be cancelled.", r.Status)), nil
			}),
	})
	set.Set("answer_checkin", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Record a check-in answer",
		Tool: kit.Tool(`Record the owner's answer to a check-in. Use it when their message answers one ("udah", "belum", "skip hari ini"). Without runId it answers the newest open check-in in this chat, or else the newest run that will ask one later. A run that has not asked yet keeps a closing answer (early: true) and skips the question when it gets there. Pass the run id from the system prompt or flow_history for another one, or to correct an earlier answer. answer is an answer id or its label.`,
			zod.Object(
				zod.F("answer", zod.String().Min(1).Max(40)),
				zod.F("runId", zod.String().Optional().Describe("A flow run id (flr_...).")),
				zod.F("note", zod.String().Max(200).Optional().Describe("The owner's words, kept in the log.")),
			),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				res, err := flows.AnswerFlowCheckin(d, flows.AnswerInput{ChatID: chatID, RunID: in.S("runId"), Answer: in.S("answer"), Text: in.S("note")}, clock())
				if err != nil {
					return fail(err)
				}
				return kit.O("ok", true, "message", res.Reply, "runId", res.Run.ID, "flow", res.Flow.Name, "closed", res.Closed, "early", kit.If(res.Early, true)), nil
			}),
	})
	set.Set("explain_flow", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "Why didn't a flow run",
		Tool: kit.Tool(`Diagnose "why didn't this flow fire / send / ask?" for a moment (default now): whether it is paused, the nearest run with its status, skip reason, the step it ended at and the values each if compared, or, with no run, what the trigger says (the computed times, source errors, the hook or poll state) and whether Palui was down. Phrase the verdict for the owner; don't guess beyond it.`,
			zod.Object(zod.F("id", zod.String()), zod.F("at", localDateTime().Optional().Describe("Local date-time to ask about (YYYY-MM-DDTHH:mm). Default now."))),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				f, refusal, err := own(in.S("id"))
				if err != nil || refusal != nil {
					return refusal, err
				}
				when := now
				if at := in.S("at"); at != "" {
					if when, err = reminders.LocalToUTC(at, f.TZ); err != nil {
						return fail(err)
					}
				}
				dg, err := flows.DiagnoseFlow(d, f, when, now)
				if err != nil {
					return fail(err)
				}
				b, err := jsonx.Marshal(dg)
				if err != nil {
					return nil, err
				}
				body, err := jsonx.ParseObject(b)
				if err != nil {
					return nil, err
				}
				return kit.Spread(kit.O("ok", true, "flow", f.Name, "at", reminders.FormatWhen(when, f.TZ, now)), body), nil
			}),
	})
	return set
}

func skippedText(s string) string {
	if s == "" {
		return "undefined"
	}
	return s
}

// SummaryJSON is summarizeRuns' object.
func SummaryJSON(s flows.RunSummary) *jsonx.Object {
	labels := make([]any, len(s.MissedLabels))
	for i, l := range s.MissedLabels {
		labels[i] = l
	}
	return kit.O("total", s.Total, "byStatus", s.ByStatus, "bySkipReason", s.BySkipReason, "byAnswer", s.ByAnswer, "missed", s.Missed, "open", s.Open, "missedLabels", labels)
}

// RecapTool is recapTool(db, turn, clock): flows__recap, callable by the
// agent and, as a tool step, by a flow ("self" or nothing is the calling flow).
func RecapTool(d *db.DB, turn connectors.TurnInfo, clock func() int64) connectors.PaluiTool {
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	return connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "Flow recap",
		Tool: kit.Tool(`A recap of a flow's runs over the last N days: total, counts by check-in answer, missed count and labels, open check-ins, counts by status, and `+"`text`"+` (the ready-to-send recap). flow is a name or id; "self" or nothing means the flow calling it.`,
			zod.Object(
				zod.F("flow", zod.String().Max(80).Optional()),
				zod.F("days", zod.Default(zod.Int().Min(1).Max(90), float64(7))),
				zod.F("trigger", zod.String().Max(32).Optional().Describe("Only runs started by this trigger (e.g. main).")),
			),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				ref := in.S("flow")
				if ref == "" || ref == "self" {
					ref = turn.FlowID
				}
				if ref == "" {
					return kit.Fail(`Name a flow ("self" only works inside a flow).`), nil
				}
				f, err := flows.ResolveFlow(d, turn.ChatID, ref)
				if err != nil {
					return nil, err
				}
				if f == nil {
					return kit.Fail(fmt.Sprintf("No flow \"%s\" in this chat.", ref)), nil
				}
				days := float64(7)
				if n, ok := in.Num("days"); ok {
					days = n
				}
				recap, err := flows.FlowRecap(d, f, days, in.S("trigger"), clock())
				if err != nil {
					return nil, err
				}
				return kit.Spread(kit.O("ok", true), recap), nil
			}),
	}
}

// OfferFlowTools: the owner's own chats (never a shared chat or a guest
// turn), and not unattended turns (jobs, hooks, flow prompt steps).
func OfferFlowTools(q db.Q, turn connectors.TurnInfo) (bool, error) {
	if turn.Trigger != "message" && turn.Trigger != "approval" {
		return false, nil
	}
	c, ok, err := chats.Get(q, turn.ChatID)
	if err != nil || !ok {
		return false, err
	}
	shared, err := agents.IsSharedChat(q, c)
	return !shared, err
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		rows, err := flows.ListFlows(c.DB, "")
		if err != nil {
			return connectors.TestResult{}, err
		}
		if n := len(rows); n > 0 {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d flow%s", n, kit.Plural(n))}, nil
		}
		return connectors.TestResult{OK: true, Detail: "Ready, no flows yet"}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		var mu sync.Mutex
		var engine *flows.Engine
		var flowTools connectors.FlowToolCaller
		if c.Services != nil {
			flowTools = c.Services.FlowTools
		}
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				if turn.Trigger == "flow" && turn.FlowID != "" {
					set := connectors.NewToolSet()
					set.Set("recap", RecapTool(c.DB, turn, nil))
					return set, nil
				}
				ok, err := OfferFlowTools(c.DB, turn)
				if err != nil {
					return nil, err
				}
				if !ok {
					return connectors.NewToolSet(), nil
				}
				mu.Lock()
				if engine == nil {
					engine = flows.NewEngine(flows.EngineOptions{DB: c.DB, Log: c.Log, Tools: flowTools})
				}
				e := engine
				mu.Unlock()
				set := Tools(c.DB, turn, Deps{Tools: flowTools, Engine: e})
				set.Set("recap", RecapTool(c.DB, turn, nil))
				return set, nil
			},
		}, nil
	},
}

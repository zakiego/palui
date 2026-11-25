// Package deps ports apps/runtime/src/deps.ts: it wires the reminders, jobs,
// outbound and flows modules into the engine (chat.Deps), builds the scheduler
// with its extensions (the flow engine first, as TS), and gives the outbox
// worker its outbound-failed hook.
//
// The engine takes Deps at construction and the job runner and smart
// reminders need engine.RunTurn, so Build takes RunTurn as a func that M8
// points at the engine once it exists:
//
//	var engine *chat.Engine
//	built := deps.Build(deps.Options{DB: d, Log: log, RunTurn: func(o chat.TurnOptions) (chat.TurnResult, error) { return engine.RunTurn(o) }})
//	engine = chat.NewEngine(chat.EngineOptions{..., Deps: built.Deps})
//	... built.Scheduler.Start() after engine.Start(), built.Scheduler.Stop() on shutdown.
package deps

import (
	"context"
	"fmt"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/tools"
	"github.com/zakiego/palui/apps/server/internal/watches"
)

// RunTurn is engine.RunTurn.
type RunTurn func(o chat.TurnOptions) (chat.TurnResult, error)

// RunSmartReminder: a smart reminder counts as fired only when its turn really
// sent something (or is waiting on an approval prompt it sent). Anything else
// (draining, no model, budget pause, an error, an empty answer) is an error,
// so the scheduler sends the plain reminder text instead of losing it.
func RunSmartReminder(runTurn RunTurn, r db.Reminder) error {
	prompt := r.Text
	if r.SmartPrompt != nil {
		prompt = *r.SmartPrompt
	}
	res, err := runTurn(chat.TurnOptions{ChatID: r.ChatID, Trigger: "reminder", Prompt: prompt})
	if err != nil {
		return err
	}
	if res.Status == "awaiting_approval" {
		return nil
	}
	if res.Status == "ok" && strx.Trim(res.Text) != "" {
		return nil
	}
	return fmt.Errorf("smart reminder turn sent nothing (status %s)", res.Status)
}

// JobTurn adapts engine.RunTurn to the job runner.
func JobTurn(runTurn RunTurn) jobs.RunTurn {
	return func(_ context.Context, r jobs.TurnRequest) (jobs.TurnResponse, error) {
		res, err := runTurn(chat.TurnOptions{ChatID: r.ChatID, Trigger: r.Trigger, Prompt: r.Prompt, JobID: r.JobID, ToolScope: r.ToolScope, ModelRole: r.ModelRole, SilentToken: r.SilentToken})
		if err != nil {
			return jobs.TurnResponse{}, err
		}
		return jobs.TurnResponse{RunID: res.RunID, Text: res.Text, Status: res.Status}, nil
	}
}

// WatchTurn adapts engine.RunTurn to a watch check: a "watch" turn in the
// watch's chat that delivers nothing and starts tainted (it reads the web).
// The engine reads its final text.
func WatchTurn(runTurn RunTurn) func(watches.CheckRequest) (watches.CheckResponse, error) {
	return func(r watches.CheckRequest) (watches.CheckResponse, error) {
		deliver := false
		res, err := runTurn(chat.TurnOptions{ChatID: r.ChatID, Trigger: "watch", Prompt: r.Prompt, ModelRole: "default", Deliver: &deliver, UntrustedInput: true})
		if err != nil {
			return watches.CheckResponse{}, err
		}
		return watches.CheckResponse{RunID: res.RunID, Text: res.Text, Status: res.Status}, nil
	}
}

// OutboundFailed is outbox.WorkerOptions.OutboundFailed (outboundFailed).
func OutboundFailed(log *logx.Logger) func(q db.Q, row db.Outbox, now int64) {
	if log == nil {
		log = logx.Silent
	}
	return func(q db.Q, row db.Outbox, now int64) {
		if err := outbound.OutboundFailed(q, row, now); err != nil {
			log.Error("outbound failure notice failed", logx.Fields{"outbox_id": row.ID, "err": err})
		}
	}
}

type Options struct {
	DB      db.Q
	Log     *logx.Logger
	RunTurn RunTurn
	// Unix ms; default time.Now.
	Clock  func() int64
	Timers reminders.Timers
	// More scheduler extensions, swept after the flow engine and before outbound.
	Extensions []reminders.Extension
	// The supervisor's running connectors (sup.Running): flow tool steps,
	// polls and schedule tool sources call tools through them. Nil: flow
	// steps that need a tool fail with "No tool caller is wired".
	Running func() []connectors.RunningEntry
	// FlowsSerial runs flow work in sweep order (tests).
	FlowsSerial bool
	// WatchesSerial runs watch checks inside the sweep (tests).
	WatchesSerial bool
	// Proactive is the proactive engine (docs/rfc/proactive.md), swept after
	// the flow engine. Nil leaves proactive out.
	Proactive *proactive.Engine
}

type Built struct {
	Deps      chat.Deps
	Scheduler *reminders.Scheduler
	Runner    *jobs.Runner
	Outbound  *outbound.SchedulerExt
	// The flow engine (also the first scheduler extension): StartRun for
	// run_flow, DryRunFlow's engine, and the flow tool caller it uses.
	Flows     *flows.Engine
	FlowTools *flows.ConnectorFlowCaller
	// Proactive is Options.Proactive (CheckNow for the dashboard and tools).
	Proactive *proactive.Engine
	// Watches checks the watches set from chat (PLAN 6e).
	Watches *watches.Engine
}

func Build(o Options) *Built {
	log := o.Log
	if log == nil {
		log = logx.Silent
	}
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	runner := jobs.NewRunner(jobs.RunnerOptions{DB: o.DB, Log: log.Child(logx.Fields{"component": "jobs"}), RunTurn: JobTurn(o.RunTurn), Clock: o.Clock})
	ob := outbound.NewScheduler(o.DB, log.Child(logx.Fields{"component": "outbound"}))
	fo := flows.EngineOptions{
		DB:        o.DB,
		Log:       log.Child(logx.Fields{"component": "flows"}),
		Clock:     o.Clock,
		RunPrompt: flows.RunFlowPrompt(o.RunTurn),
		Serial:    o.FlowsSerial,
	}
	var flowTools *flows.ConnectorFlowCaller
	if o.Running != nil {
		running := o.Running
		flowTools = flows.NewConnectorFlowCaller(running)
		fo.Tools = flowTools
		// Server-side, straight to the connector: never through a turn's tool list.
		fo.ReadSource = tools.ConnectorToolCaller(running, tools.CallerOptions{Clock: o.Clock, TZ: func() string {
			tz, err := settings.Timezone(o.DB)
			if err != nil {
				return "UTC"
			}
			return tz
		}})
		fo.SourceUntrusted = func(id string) bool {
			for _, e := range running() {
				if e.ID == id {
					return e.Row.Trust == "external"
				}
			}
			return false
		}
	}
	fe := flows.NewEngine(fo)
	exts := []reminders.Extension{fe}
	if o.Proactive != nil {
		exts = append(exts, o.Proactive)
	}
	we := watches.NewEngine(watches.Options{DB: o.DB, Log: log.Child(logx.Fields{"component": "watches"}), Clock: o.Clock, Check: WatchTurn(o.RunTurn), Serial: o.WatchesSerial})
	exts = append(exts, we)
	exts = append(append(exts, o.Extensions...), ob)
	sched := reminders.NewScheduler(reminders.SchedulerOptions{
		DB:     o.DB,
		Log:    log.Child(logx.Fields{"component": "scheduler"}),
		Clock:  o.Clock,
		Timers: o.Timers,
		OnJobDue: func(job db.Job) error {
			runner.Run(job)
			return nil
		},
		// Smart reminders run their prompt as a turn in the reminder's chat.
		OnSmartReminder: func(r db.Reminder) error { return RunSmartReminder(o.RunTurn, r) },
		Extensions:      exts,
	})
	clock := o.Clock
	b := &Built{Scheduler: sched, Runner: runner, Outbound: ob, Flows: fe, FlowTools: flowTools, Proactive: o.Proactive, Watches: we}
	we.OnPoke(sched.Poke)
	if o.Proactive != nil {
		o.Proactive.OnPoke(sched.Poke)
	}
	b.Deps = chat.Deps{
		ReminderActions:  reminders.Actions{},
		FlowActions:      flows.Actions{},
		ProactiveActions: proactive.Actions{},
		Flows:            flows.Router{},
		// Parse tokens count toward the daily cap like a turn's.
		ParseReminder: func(ctx context.Context, text, tz string, now int64, model models.LanguageModel) (chat.ParsedReminder, error) {
			return reminders.ParseReminder(ctx, text, tz, now, models.MeterToBudget(o.DB, model, clock), reminders.ParseOptions{}), nil
		},
		Scheduler: schedulerAdapter{sched},
		Jobs:      jobsAdapter{runner},
		Outbound:  outbound.Router{},
	}
	return b
}

type schedulerAdapter struct{ s *reminders.Scheduler }

func (a schedulerAdapter) Start() error                   { return a.s.Start() }
func (a schedulerAdapter) Stop(ctx context.Context) error { a.s.Stop(); return nil }
func (a schedulerAdapter) Poke()                          { a.s.Poke() }

type jobsAdapter struct{ r *jobs.Runner }

func (a jobsAdapter) RunNow(_ context.Context, jobID string) (chat.JobRunResult, error) {
	res, err := a.r.RunNow(jobID)
	return chat.JobRunResult{OK: res.OK, RunID: res.RunID, Detail: res.Detail}, err
}

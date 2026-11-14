// Package jobs is the jobs connector (connectors/src/jobs): a read-only job
// listing for the model (PLAN 7). Creating and editing jobs stays in the
// dashboard.
package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:        "jobs",
	Kind:      "skill",
	Name:      "Scheduled jobs",
	Icon:      "calendar-clock",
	Summary:   "Prompts that run on a schedule.",
	Builtin:   true,
	Setup:     []connectors.SetupStep{},
	PauseNote: "While paused, Palui can't list jobs. Jobs keep running.",
	Docs: &connectors.Docs{Notes: []string{
		"The heartbeat runs every 30 minutes from 08:00 to 22:00 and sends nothing when nothing matters.",
		"Each job has a daily token cap. When it's used up, runs are skipped until the next day.",
	}},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

var listJobsInput = zod.Object(
	zod.F("includePaused", zod.Default(zod.Bool(), true).Describe("Include paused jobs.")),
)

// Tools is jobTools(db, clock).
func Tools(d *db.DB, clock func() int64) *connectors.ToolSet {
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	set := connectors.NewToolSet()
	set.Set("list_jobs", connectors.PaluiTool{
		Class: "read",
		Title: "List scheduled jobs",
		Tool: kit.Tool("List scheduled jobs (morning briefing, heartbeat, custom prompts) with their schedule, next run and last result. Jobs are created and edited in the dashboard.", listJobsInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				opts := jobs.ListOptions{}
				if !in.True("includePaused") {
					on := true
					opts.Enabled = &on
				}
				rows, err := jobs.List(d, opts)
				if err != nil {
					return nil, err
				}
				list := make([]any, 0, len(rows))
				for _, j := range rows {
					schedule, err := jobs.DescribeSchedule(j, now)
					if err != nil {
						return nil, err
					}
					next, ok := jobs.DescribeNextRun(j, now)
					if !ok {
						next = "Dijeda"
						if j.Enabled {
							next = "Selesai (aturan ulangnya udah habis)"
						}
					}
					var last any
					if j.LastResult != nil && *j.LastResult != "" {
						last = strx.Slice16(*j.LastResult, 200)
					}
					list = append(list, kit.O(
						"id", j.ID, "name", j.Name, "kind", j.Kind, "enabled", j.Enabled,
						"schedule", schedule, "next", next, "lastStatus", kit.StrPtr(j.LastStatus), "lastResult", last,
					))
				}
				return kit.O("ok", true, "count", len(rows), "jobs", list,
					"message", kit.If(len(rows) == 0, "Belum ada jadwal. Bikin di dashboard, halaman Jobs.")), nil
			}),
	})
	return set
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		on := true
		rows, err := jobs.List(c.DB, jobs.ListOptions{Enabled: &on})
		if err != nil {
			return connectors.TestResult{}, err
		}
		if n := len(rows); n > 0 {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d active job%s", n, kit.Plural(n))}, nil
		}
		return connectors.TestResult{OK: true, Detail: "Ready, no active jobs"}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
				return Tools(c.DB, nil), nil
			},
		}, nil
	},
}

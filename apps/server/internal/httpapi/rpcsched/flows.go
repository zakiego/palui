package rpcsched

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/flows.ts and the list and detail views of
// server/flows.ts: read-only, flows are built and changed from chat.

const dayMs = int64(24 * 3600_000)

func init() {
	httpapi.Register(httpapi.Handler{
		Name: "getFlows", Method: http.MethodGet, Guard: httpapi.User,
		Fn: func(c *httpapi.Call) (any, error) {
			q := c.DB()
			list, err := flowList(q, c.Now())
			if err != nil {
				return nil, err
			}
			tz, err := settings.Timezone(q)
			if err != nil {
				return nil, err
			}
			return obj("flows", list, "timezone", tz), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "getFlowFn", Method: http.MethodGet, Guard: httpapi.User,
		Input: zod.Object(zod.F("id", zod.String().Min(1).Max(64))),
		Fn: func(c *httpapi.Call) (any, error) {
			q := c.DB()
			detail, err := flowDetail(q, inputID(c))
			if err != nil {
				return nil, err
			}
			tz, err := settings.Timezone(q)
			if err != nil {
				return nil, err
			}
			return obj("flow", detail, "timezone", tz), nil
		},
	})
}

// prettyJSON is server/flows.ts json(): JSON.stringify(v, null, 2), capped
// at 6000 UTF-16 units so one huge value can't swamp the page.
func prettyJSON(v any) string {
	s := zod.Indent(v)
	if strx.Len16(s) > 6000 {
		return strx.Slice16(s, 5999) + "…"
	}
	return s
}

// chatTitleOf is the chat's title column (null when unset or gone).
func chatTitleOf(q db.Q, chatID string) (*string, error) {
	var title *string
	_, _, err := db.One(q.QueryRow("SELECT title FROM chats WHERE id = ?", chatID), func(s db.Scanner) (bool, error) {
		return true, s.Scan(&title)
	})
	return title, err
}

// safeDef is the current version's definition, nil when it can't be read.
func safeDef(q db.Q, f *db.Flow) *flows.Definition {
	v, err := flows.CurrentVersion(q, f)
	if err != nil {
		return nil
	}
	def, err := flows.DefinitionOf(v)
	if err != nil {
		return nil
	}
	return def
}

type triggerSummary struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

type lastRun struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	SkipReason *string `json:"skipReason"`
	StartedAt  int64   `json:"startedAt"`
	Error      *string `json:"error"`
}

type flowListItem struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description *string          `json:"description"`
	Enabled     bool             `json:"enabled"`
	PausedUntil *int64           `json:"pausedUntil"`
	Failures    int64            `json:"failures"`
	LastError   *string          `json:"lastError"`
	Approval    string           `json:"approval"`
	ChatTitle   *string          `json:"chatTitle"`
	TZ          string           `json:"tz"`
	Triggers    []triggerSummary `json:"triggers"`
	Steps       int              `json:"steps"`
	NextAt      *int64           `json:"nextAt"`
	LastRun     *lastRun         `json:"lastRun"`
	Runs7d      int64            `json:"runs7d"`
}

// flowList is server/flows.ts flowList.
func flowList(q db.Q, now int64) ([]flowListItem, error) {
	rows, err := q.Query("SELECT " + db.FlowCols + " FROM flows ORDER BY created_at")
	list, err := db.All(rows, err, db.ScanFlow)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	rows, err = q.Query("SELECT flow_id, count(*) FROM flow_runs WHERE started_at >= ? AND dry_run = 0 AND parent_run_id IS NULL GROUP BY flow_id", now-7*dayMs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[id] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []flowListItem{}
	for i := range list {
		f := &list[i]
		def := safeDef(q, f)
		triggers, err := flows.TriggersOf(q, f.ID)
		if err != nil {
			return nil, err
		}
		var next []int64
		for _, t := range triggers {
			if t.NextFireAt != nil {
				next = append(next, *t.NextFireAt)
			}
		}
		sort.Slice(next, func(a, b int) bool { return next[a] < next[b] })
		last, ok, err := db.One(q.QueryRow("SELECT id, status, skip_reason, started_at, error FROM flow_runs WHERE flow_id = ? AND dry_run = 0 AND parent_run_id IS NULL ORDER BY started_at DESC LIMIT 1", f.ID), func(s db.Scanner) (lastRun, error) {
			var r lastRun
			err := s.Scan(&r.ID, &r.Status, &r.SkipReason, &r.StartedAt, &r.Error)
			return r, err
		})
		if err != nil {
			return nil, err
		}
		approval, err := flows.ApprovalState(q, f)
		if err != nil {
			return nil, err
		}
		title, err := chatTitleOf(q, f.ChatID)
		if err != nil {
			return nil, err
		}
		item := flowListItem{ID: f.ID, Name: f.Name, Enabled: f.Enabled, PausedUntil: f.PausedUntil, Failures: f.ConsecutiveFailures, LastError: f.LastError, Approval: approval, ChatTitle: title, TZ: f.TZ, Triggers: []triggerSummary{}, Runs7d: counts[f.ID]}
		if def != nil {
			item.Description = def.Description
			for j := range def.Triggers {
				t := &def.Triggers[j]
				item.Triggers = append(item.Triggers, triggerSummary{ID: t.ID, Kind: t.Kind, Summary: flows.DescribeTrigger(t)})
			}
			item.Steps = len(def.Steps)
		}
		if f.Enabled && len(next) > 0 {
			item.NextAt = &next[0]
		}
		if ok {
			item.LastRun = &last
		}
		out = append(out, item)
	}
	return out, nil
}

// stepSummary is server/flows.ts stepSummary: one line per step.
func stepSummary(s *flows.Step) string {
	switch s.Kind {
	case "tool":
		prefix := ""
		if s.ConnectorID != nil && *s.ConnectorID != "" {
			prefix = *s.ConnectorID + "."
		}
		return prefix + s.Tool
	case "if":
		return s.When
	case "message", "checkin":
		return s.Text
	case "wait":
		if s.Minutes != nil {
			return strconv.Itoa(*s.Minutes) + " min"
		}
		until := "undefined"
		if s.Until != nil {
			until = *s.Until
		}
		out := "until " + until
		if s.PlusMin != nil && *s.PlusMin != 0 {
			out += " + " + strconv.Itoa(*s.PlusMin) + " min"
		}
		return out
	case "prompt":
		return s.Prompt
	case "set":
		scope := "vars"
		if s.Scope == "flow" {
			scope = "flow"
		}
		var parts []string
		if s.Values != nil {
			for _, k := range s.Values.Keys() {
				v, _ := s.Values.Get(k)
				parts = append(parts, scope+"."+k+" = "+v)
			}
		}
		return strings.Join(parts, "; ")
	case "each":
		if s.Where != nil && *s.Where != "" {
			return s.Items + " where " + *s.Where
		}
		return s.Items
	}
	return ""
}

type triggerDetail struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Start     string  `json:"start"`
	Summary   string  `json:"summary"`
	NextAt    *int64  `json:"nextAt"`
	LastError *string `json:"lastError"`
}

type stepEdge struct {
	Label string  `json:"label"`
	To    *string `json:"to"`
}

type stepDetail struct {
	ID      string     `json:"id"`
	Kind    string     `json:"kind"`
	Name    *string    `json:"name"`
	Summary string     `json:"summary"`
	Write   bool       `json:"write"`
	StartOf []string   `json:"startOf"`
	Edges   []stepEdge `json:"edges"`
	Ends    bool       `json:"ends"`
}

type versionDetail struct {
	N         int64    `json:"n"`
	CreatedBy string   `json:"createdBy"`
	Note      *string  `json:"note"`
	Approved  bool     `json:"approved"`
	Writes    int      `json:"writes"`
	CreatedAt int64    `json:"createdAt"`
	Current   bool     `json:"current"`
	Diff      []string `json:"diff"`
}

type runItem struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	SkipReason  *string `json:"skipReason"`
	Trigger     string  `json:"trigger"`
	TriggerKind string  `json:"triggerKind"`
	StartedAt   int64   `json:"startedAt"`
	ScheduledAt *int64  `json:"scheduledAt"`
	Label       *string `json:"label"`
	Error       *string `json:"error"`
	Child       bool    `json:"child"`
	Answer      *string `json:"answer"`
}

type flowDetailView struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Enabled     bool            `json:"enabled"`
	PausedUntil *int64          `json:"pausedUntil"`
	Failures    int64           `json:"failures"`
	LastError   *string         `json:"lastError"`
	Approval    string          `json:"approval"`
	ChatID      string          `json:"chatId"`
	ChatTitle   *string         `json:"chatTitle"`
	TZ          string          `json:"tz"`
	CreatedAt   int64           `json:"createdAt"`
	Version     int64           `json:"version"`
	State       *string         `json:"state"`
	Triggers    []triggerDetail `json:"triggers"`
	Steps       []stepDetail    `json:"steps"`
	WriteCalls  []string        `json:"writeCalls"`
	Versions    []versionDetail `json:"versions"`
	Runs        []runItem       `json:"runs"`
}

// flowDetail is server/flows.ts flowDetail: one flow with its steps,
// versions and recent runs; nil when the flow or its versions are gone.
func flowDetail(q db.Q, id string) (*flowDetailView, error) {
	f, err := flows.GetFlow(q, id)
	if err != nil || f == nil {
		return nil, err
	}
	versions, err := flows.ListVersions(q, f.ID)
	if err != nil {
		return nil, err
	}
	var current *db.FlowVersion
	for i := range versions {
		if f.CurrentVersionID != nil && versions[i].ID == *f.CurrentVersionID {
			current = &versions[i]
			break
		}
	}
	if current == nil && len(versions) > 0 {
		current = &versions[0]
	}
	if current == nil {
		return nil, nil
	}
	def, err := flows.DefinitionOf(current)
	if err != nil {
		def = nil
	}
	triggerRows, err := flows.TriggersOf(q, f.ID)
	if err != nil {
		return nil, err
	}
	starts := map[string][]string{}
	writeSteps := map[string]bool{}
	for _, w := range current.WriteCalls {
		writeSteps[w.StepID] = true
	}
	rows, err := q.Query("SELECT "+db.FlowRunCols+" FROM flow_runs WHERE flow_id = ? AND dry_run = 0 ORDER BY started_at DESC LIMIT 30", f.ID)
	recent, err := db.All(rows, err, db.ScanFlowRun)
	if err != nil {
		return nil, err
	}
	approval, err := flows.ApprovalState(q, f)
	if err != nil {
		return nil, err
	}
	title, err := chatTitleOf(q, f.ChatID)
	if err != nil {
		return nil, err
	}
	v := &flowDetailView{
		ID: f.ID, Name: f.Name, Enabled: f.Enabled, PausedUntil: f.PausedUntil, Failures: f.ConsecutiveFailures, LastError: f.LastError,
		Approval: approval, ChatID: f.ChatID, ChatTitle: title, TZ: f.TZ, CreatedAt: f.CreatedAt, Version: current.N,
		Triggers: []triggerDetail{}, Steps: []stepDetail{}, WriteCalls: []string{}, Versions: []versionDetail{}, Runs: []runItem{},
	}
	// Flow variables as JSON text (values are data of any shape).
	if f.State.Values != nil && f.State.Values.Len() > 0 {
		s := prettyJSON(f.State.Values)
		v.State = &s
	}
	if def != nil {
		v.Description = def.Description
		for _, t := range def.Triggers {
			starts[t.Start] = append(starts[t.Start], t.ID)
		}
		for i := range def.Triggers {
			t := &def.Triggers[i]
			d := triggerDetail{ID: t.ID, Kind: t.Kind, Start: t.Start, Summary: flows.DescribeTrigger(t)}
			for _, r := range triggerRows {
				if r.TriggerID == t.ID {
					if f.Enabled {
						d.NextAt = r.NextFireAt
					}
					d.LastError = r.LastError
					break
				}
			}
			v.Triggers = append(v.Triggers, d)
		}
		for i := range def.Steps {
			s := &def.Steps[i]
			sd := stepDetail{ID: s.ID, Kind: s.Kind, Name: s.Name, Summary: stepSummary(s), Write: writeSteps[s.ID], StartOf: starts[s.ID], Edges: []stepEdge{}, Ends: true}
			if sd.StartOf == nil {
				sd.StartOf = []string{}
			}
			for _, e := range flows.EdgesOf(def, s) {
				to := e.To
				sd.Edges = append(sd.Edges, stepEdge{Label: e.Label, To: &to})
				if e.Label == "next" || e.Label == "then" || e.Label == "else" {
					sd.Ends = false
				}
			}
			// An if branch that ends the run shows as "else -> ends".
			if s.Kind == "if" {
				if s.Then.ID == nil {
					sd.Edges = append(sd.Edges, stepEdge{Label: "then"})
				}
				if s.Else.ID == nil {
					sd.Edges = append(sd.Edges, stepEdge{Label: "else"})
				}
			}
			v.Steps = append(v.Steps, sd)
		}
	}
	for _, w := range current.WriteCalls {
		v.WriteCalls = append(v.WriteCalls, flows.DescribeWriteCall(w))
	}
	for i := range versions {
		ver := &versions[i]
		diff := []string{}
		if i+1 < len(versions) {
			prev, err1 := flows.DefinitionOf(&versions[i+1])
			cur, err2 := flows.DefinitionOf(ver)
			if err1 == nil && err2 == nil {
				diff = flows.DiffDefinitions(prev, cur)
			}
		}
		v.Versions = append(v.Versions, versionDetail{N: ver.N, CreatedBy: ver.CreatedBy, Note: ver.Note, Approved: ver.ApprovedAt != nil || len(ver.WriteCalls) == 0, Writes: len(ver.WriteCalls), CreatedAt: ver.CreatedAt, Current: f.CurrentVersionID != nil && ver.ID == *f.CurrentVersionID, Diff: diff})
	}
	for i := range recent {
		r := &recent[i]
		item := runItem{ID: r.ID, Status: r.Status, SkipReason: r.SkipReason, Trigger: r.TriggerKind, TriggerKind: r.TriggerKind, StartedAt: r.StartedAt, ScheduledAt: r.ScheduledAt, Error: r.Error, Child: r.ParentRunID != nil && *r.ParentRunID != ""}
		if r.TriggerID != nil {
			item.Trigger = *r.TriggerID
		}
		label := ""
		if r.Trigger != nil {
			if l, ok := r.Trigger.Get("label"); ok && l != nil {
				label = jsString(l)
			}
		}
		if label != "" {
			item.Label = &label
		}
		if ci := flows.CheckinOf(r); ci != nil {
			item.Answer = ci.Answer
		}
		v.Runs = append(v.Runs, item)
	}
	return v, nil
}

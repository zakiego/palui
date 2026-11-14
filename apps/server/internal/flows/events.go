package flows

// Flow events (events.ts, RFC flows 4.1): checkin.answered, checkin.missed,
// run.done and run.failed start the flows whose `event` trigger listens for
// them. A run started by an event carries trigger.depth; past 3 it's logged as
// skipped (chain_depth).

import (
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

const MaxChainDepth = 3

// EventData is FlowEventData.
type EventData struct {
	Step   *string
	Answer *string
	Error  *string
}

func strOrNull(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// EmitFlowEvent is emitFlowEvent: one queued run per listening flow, written
// in the caller's transaction. Returns how many runs it started.
func EmitFlowEvent(q db.Q, name string, src *db.FlowRun, data EventData, now int64) (int, error) {
	if src.DryRun {
		return 0, nil
	}
	rows, err := q.Query("SELECT "+db.FlowTriggerCols+" FROM flow_triggers WHERE kind = 'event' AND match = ? ORDER BY rowid", name)
	listeners, err := db.All(rows, err, db.ScanFlowTrigger)
	if err != nil || len(listeners) == 0 {
		return 0, err
	}
	source, err := GetFlow(q, src.FlowID)
	if err != nil || source == nil {
		return 0, err
	}
	depth := 1.0
	if d, ok := objNum(src.Trigger, "depth"); ok {
		depth = d + 1
	}
	n := 0
	for _, row := range listeners {
		flow, err := GetFlow(q, row.FlowID)
		if err != nil {
			return n, err
		}
		if flow == nil || flow.ChatID != source.ChatID {
			continue
		}
		version, def, err := currentDef(q, flow)
		if err != nil {
			continue
		}
		t := def.Trigger(row.TriggerID)
		if t == nil || t.Kind != "event" {
			continue
		}
		target := source.ID == flow.ID
		if t.Flow != "self" {
			target = jsLower(t.Flow) == jsLower(source.Name)
		}
		if !target || (t.Flow == "self" && name == "run.failed") {
			continue
		}
		label, ok := src.Trigger.Get("label")
		if !ok {
			label = nil
		}
		payload := obj("kind", "event", "event", name, "flowId", source.ID, "flow", source.Name, "runId", src.ID, "step", strOrNull(data.Step), "answer", strOrNull(data.Answer), "error", strOrNull(data.Error), "label", label, "depth", depth)
		skip := ""
		if depth > MaxChainDepth {
			skip = "chain_depth"
		} else if !flow.Enabled {
			skip = "paused"
		}
		taint := []string{}
		if len(src.Taint) > 0 {
			taint = []string{"trigger"}
		}
		status := "queued"
		var skipReason, cursor *string
		var wakeAt, endedAt *int64
		if skip != "" {
			status, skipReason, endedAt = "skipped", &skip, &now
		} else {
			cursor, wakeAt = &t.Start, &now
		}
		key := name + ":" + src.ID + ":" + deref(data.Step, "")
		tid := t.ID
		res, err := q.Exec("INSERT INTO flow_runs (id, flow_id, version_id, chat_id, trigger_id, trigger_kind, run_key, status, skip_reason, trigger, taint, cursor, wake_at, scheduled_at, started_at, ended_at, updated_at) VALUES (?, ?, ?, ?, ?, 'event', ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?) ON CONFLICT DO NOTHING",
			secret.NewID("flr"), flow.ID, version.ID, flow.ChatID, tid, key, status, skipReason, db.JSON[any]{V: payload}, db.JSON[[]string]{V: taint}, cursor, wakeAt, now, endedAt, now)
		if err != nil {
			return n, err
		}
		if c, _ := res.RowsAffected(); c > 0 && skip == "" {
			n++
		}
	}
	if n > 0 {
		reminders.NotifyScheduleChanged()
	}
	return n, nil
}

var _ = jsonx.NewMap[any]

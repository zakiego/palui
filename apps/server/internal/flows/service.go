package flows

// Flows: create, edit (new immutable versions), pause, delete, approve, and
// the trigger rows derived from each version (service.ts). The engine runs
// them; this file owns the rows. Every write that moves a due time calls
// reminders.NotifyScheduleChanged so the scheduler re-arms at once.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

func logEvent(q db.Q, in events.Input, now int64) error {
	_, err := events.Log(q, in, now)
	return err
}

// --- chats and contacts a flow may write to -----------------------------------------------

// ContactResolver is contactResolver: the flow's own agent's saved contacts
// (nil agent is the default agent).
func ContactResolver(q db.Q, agentID *string, now int64) func(ref string) *ContactRef {
	return func(ref string) *ContactRef {
		agent := ""
		if agentID != nil {
			agent = *agentID
		} else if a, err := agents.GetDefault(q, now); err == nil {
			agent = a.ID
		}
		c, ok, err := outbound.FindContact(q, agent, ref)
		if err != nil || !ok {
			return nil
		}
		return &ContactRef{ID: c.ID, Name: c.Name}
	}
}

// ChatAccess is chatAccess: "own" for the flow's chat or one of the owner's
// DMs (or the web chat), "allowed" for a chat the owner allowed, "" else.
func ChatAccess(q db.Q, flowChatID string, now int64) func(chatID string) string {
	return func(chatID string) string {
		if chatID == flowChatID {
			return "own"
		}
		chat, ok, err := chats.Get(q, chatID)
		if err != nil || !ok {
			return ""
		}
		for _, ch := range []string{db.ChannelTelegram, db.ChannelWhatsApp, db.ChannelWeb} {
			if oc, ok, err := chats.OwnerChat(q, ch, now); err == nil && ok && oc.ID == chat.ID {
				return "own"
			}
		}
		if chat.Allowed {
			return "allowed"
		}
		return ""
	}
}

// --- triggers ------------------------------------------------------------------------------

// TriggerMatch is triggerMatch: the lookup key of a trigger row.
func TriggerMatch(t *Trigger) *string {
	switch t.Kind {
	case "webhook":
		return ptr(t.HookID)
	case "chat":
		if t.Command != nil && *t.Command != "" {
			return ptr(jsLower(*t.Command))
		}
		return ptr("phrases")
	case "event":
		return ptr(t.Event)
	}
	return nil
}

// ScheduleInput is scheduleInput.
func ScheduleInput(t *Trigger, tz string, sourceState *jsonx.Object) SourceInput {
	if sourceState == nil {
		sourceState = jsonx.NewMap[any]()
	}
	return SourceInput{Source: t.Source, TZ: tz, Days: t.Days, HasDays: t.HasDays, State: sourceState}
}

// NextScheduled is nextScheduled: after the cursor, never more than a day in
// the past.
func NextScheduled(t *Trigger, tz string, sourceState *jsonx.Object, cursor *Cursor, now int64) (*Occurrence, error) {
	c := Cursor{At: now}
	if cursor != nil {
		c = *cursor
	}
	floor := now - 24*3600_000
	if c.At < floor {
		c = Cursor{At: floor}
	}
	return NextOccurrenceAfter(ScheduleInput(t, tz, sourceState), c)
}

// DeriveTriggers is deriveTriggers: trigger rows for a version. A trigger
// whose config didn't change keeps its state; a changed one starts from now.
func DeriveTriggers(flow *db.Flow, def *Definition, prev []db.FlowTrigger, now int64, prevDef *Definition, tzChanged bool) []db.FlowTrigger {
	var out []db.FlowTrigger
	for ti := range def.Triggers {
		t := &def.Triggers[ti]
		if t.Kind == "manual" {
			continue
		}
		var old *db.FlowTrigger
		for i := range prev {
			if prev[i].TriggerID == t.ID && prev[i].Kind == t.Kind {
				old = &prev[i]
				break
			}
		}
		var before *Trigger
		if prevDef != nil {
			before = prevDef.Trigger(t.ID)
		}
		same := old != nil && !tzChanged && before != nil && CanonicalJSON(before.Raw) == CanonicalJSON(t.Raw)
		id := secret.NewID("flt")
		if old != nil {
			id = old.ID
		}
		row := db.FlowTrigger{ID: id, FlowID: flow.ID, TriggerID: t.ID, Kind: t.Kind, Match: TriggerMatch(t), Status: "idle", UpdatedAt: now}
		if same {
			if old.Cursor != nil {
				c := *old.Cursor
				row.Cursor = &c
			} else {
				row.Cursor = &Cursor{At: now}
			}
			row.SourceState, row.PollState, row.LastError = cloneObj(old.SourceState), cloneObj(old.PollState), old.LastError
		} else {
			at := now
			if old != nil && old.Cursor != nil && old.Cursor.At > at {
				at = old.Cursor.At
			}
			row.Cursor = &Cursor{At: at}
			row.SourceState, row.PollState = jsonx.NewMap[any](), jsonx.NewMap[any]()
		}
		if t.Kind == "schedule" {
			if t.Source.Kind == "tool" {
				n := now
				if same && old.NextRefreshAt != nil {
					n = *old.NextRefreshAt
				}
				row.NextRefreshAt = &n
			}
			if flow.Enabled {
				occ, err := NextScheduled(t, flow.TZ, row.SourceState, row.Cursor, now)
				if err != nil {
					row.LastError = ptr(strx.Slice16(err.Error(), 300))
				} else {
					row.NextOcc = occ
					if occ != nil {
						row.NextFireAt = ptr(occ.At)
					}
				}
			}
		}
		if t.Kind == "poll" && flow.Enabled {
			n := now
			if same && old.NextFireAt != nil {
				n = *old.NextFireAt
			}
			row.NextFireAt = &n
		}
		out = append(out, row)
	}
	return out
}

// --- create and edit -----------------------------------------------------------------------

// SaveInput is SaveInput.
type SaveInput struct {
	ChatID     string
	Definition any
	CreatedBy  string
	Note       *string
	AgentID    *string
	Validate   *ValidateContext
}

// SaveResult is SaveResult.
type SaveResult struct {
	Flow          *db.Flow
	Version       *db.FlowVersion
	Changed       bool
	NeedsApproval bool
	WriteCalls    []WriteCall
	Diff          []string
}

func check(q db.Q, chatID string, definition any, ctx *ValidateContext, selfName string, agentID *string, now int64) (Validated, error) {
	list, err := ListFlows(q, chatID)
	if err != nil {
		return Validated{}, err
	}
	names := []string{}
	for _, f := range list {
		if f.Name != selfName {
			names = append(names, f.Name)
		}
	}
	if selfName != "" {
		names = append(names, selfName)
	}
	vc := ValidateContext{ChatID: chatID, ChatAllowed: ChatAccess(q, chatID, now), ResolveContact: ContactResolver(q, agentID, now), FlowNames: names}
	if ctx != nil {
		if ctx.ResolveTool != nil {
			vc.ResolveTool = ctx.ResolveTool
		}
		if ctx.HookExists != nil {
			vc.HookExists = ctx.HookExists
		}
		if ctx.ChatID != "" {
			vc.ChatID = ctx.ChatID
		}
		if ctx.ChatAllowed != nil {
			vc.ChatAllowed = ctx.ChatAllowed
		}
		if ctx.ResolveContact != nil {
			vc.ResolveContact = ctx.ResolveContact
		}
		if ctx.FlowNames != nil {
			vc.FlowNames = ctx.FlowNames
		}
	}
	r := ValidateDefinition(definition, vc)
	if !r.OK || r.Definition == nil {
		lines := []string{}
		for i, p := range r.Problems {
			if i >= 8 {
				break
			}
			lines = append(lines, p.Path+": "+p.Message)
		}
		plural := "s"
		if len(r.Problems) == 1 {
			plural = ""
		}
		return r, &FlowError{Msg: fmt.Sprintf("The flow has %d problem%s: %s", len(r.Problems), plural, strings.Join(lines, " | ")), Problems: r.Problems}
	}
	return r, nil
}

func webhookIDs(def *Definition) []string {
	var out []string
	for _, t := range def.Triggers {
		if t.Kind == "webhook" {
			out = append(out, t.HookID)
		}
	}
	return out
}

// CreateFlow is createFlow.
func CreateFlow(q db.Q, in SaveInput, now int64) (*SaveResult, error) {
	r, err := check(q, in.ChatID, in.Definition, in.Validate, "", in.AgentID, now)
	if err != nil {
		return nil, err
	}
	def := r.Definition
	if _, ok, err := chats.Get(q, in.ChatID); err != nil {
		return nil, err
	} else if !ok {
		return nil, flowErr("No chat %s.", in.ChatID)
	}
	list, err := ListFlows(q, in.ChatID)
	if err != nil {
		return nil, err
	}
	if len(list) >= MaxFlowsPerChat {
		return nil, flowErr("This chat already has %d flows. Delete one first.", MaxFlowsPerChat)
	}
	tz := ""
	if def.TZ != nil {
		tz = *def.TZ
	} else if tz, err = settings.Timezone(q); err != nil {
		return nil, err
	}
	if !reminders.IsValidZone(tz) {
		return nil, flowErr(`Unknown time zone "%s".`, tz)
	}
	if dup, err := FindFlowByName(q, in.ChatID, def.Name); err != nil {
		return nil, err
	} else if dup != nil {
		return nil, flowErr(`A flow named "%s" already exists in this chat. Pick another name or change that one with update_flow.`, def.Name)
	}
	flowID, versionID := secret.NewID("flw"), secret.NewID("flv")
	needsApproval := len(r.WriteCalls) > 0
	flow := &db.Flow{ID: flowID, ChatID: in.ChatID, Name: def.Name, TZ: tz, Enabled: true, CurrentVersionID: &versionID, State: db.FlowState{Values: jsonx.NewMap[any](), Tainted: []string{}}, AgentID: in.AgentID, CreatedAt: now, UpdatedAt: now}
	if !needsApproval {
		flow.ApprovedVersionID = &versionID
	}
	version := &db.FlowVersion{ID: versionID, FlowID: flowID, N: 1, Definition: def.Raw, Hash: HashDefinition(def), WriteCalls: r.WriteCalls, CreatedBy: in.CreatedBy, Note: in.Note, CreatedAt: now}
	if !needsApproval {
		version.ApprovedAt, version.ApprovedVia = &now, ptr("no_writes")
	}
	err = q.Tx(func(tx db.Q) error {
		if err := insertFlow(tx, flow); err != nil {
			return err
		}
		if err := insertVersion(tx, version); err != nil {
			return err
		}
		if err := writeTriggers(tx, flowID, DeriveTriggers(flow, def, nil, now, nil, false)); err != nil {
			return err
		}
		if _, err := ClaimHooksForFlow(tx, webhookIDs(def), now); err != nil {
			return err
		}
		if needsApproval {
			return RequestVersionApproval(tx, flow, version, nil, now)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	wait := ""
	if needsApproval {
		wait = ", waiting for approval"
	}
	if err := logEvent(q, events.Input{Kind: "flow.created", Message: fmt.Sprintf(`Flow "%s" created (version 1%s).`, flow.Name, wait), Data: obj("flowId", flowID, "versionId", versionID)}, now); err != nil {
		return nil, err
	}
	reminders.NotifyScheduleChanged()
	return &SaveResult{Flow: flow, Version: version, Changed: true, NeedsApproval: needsApproval, WriteCalls: r.WriteCalls, Diff: []string{}}, nil
}

// ApplyPatch is applyPatch: RFC 7386 merge patch, where an object for steps
// or triggers patches entries by id (null removes one, a new id appends it).
func ApplyPatch(target, patch any) any {
	p, ok := patch.(*jsonx.Object)
	if !ok {
		return patch
	}
	out := jsonx.NewMap[any]()
	if t, ok := target.(*jsonx.Object); ok {
		out = t.Clone()
	}
	for _, k := range p.Keys() {
		v, _ := p.Get(k)
		cur, _ := out.Get(k)
		list, isList := cur.([]any)
		if vo, isObj := v.(*jsonx.Object); (k == "steps" || k == "triggers") && isObj && isList {
			next := append([]any{}, list...)
			for _, id := range vo.Keys() {
				pv, _ := vo.Get(id)
				idx := -1
				for i, x := range next {
					if objStr(x, "id") == id {
						idx = i
						break
					}
				}
				switch {
				case pv == nil:
					var kept []any
					for _, x := range next {
						if objStr(x, "id") != id {
							kept = append(kept, x)
						}
					}
					next = kept
				case idx >= 0:
					next[idx] = ApplyPatch(next[idx], pv)
				default:
					item := obj("id", id)
					if po, ok := pv.(*jsonx.Object); ok {
						item.Assign(po)
					}
					next = append(next, item)
				}
			}
			if next == nil {
				next = []any{}
			}
			out.Set(k, next)
		} else if v == nil {
			out.Delete(k)
		} else {
			out.Set(k, ApplyPatch(cur, v))
		}
	}
	return out
}

// UpdateInput is UpdateInput.
type UpdateInput struct {
	Definition any
	Patch      any
	CreatedBy  string
	Note       *string
	Validate   *ValidateContext
}

// UpdateFlow is updateFlow.
func UpdateFlow(q db.Q, id string, in UpdateInput, now int64) (*SaveResult, error) {
	flow, err := GetFlow(q, id)
	if err != nil {
		return nil, err
	}
	if flow == nil {
		return nil, flowErr("No flow %s.", id)
	}
	cur, curDef, err := currentDef(q, flow)
	if err != nil {
		return nil, err
	}
	if in.Definition == nil && in.Patch == nil {
		return nil, flowErr("Pass a definition or a patch.")
	}
	raw := in.Definition
	if raw == nil {
		patch, err := normalizeJSON(in.Patch)
		if err != nil {
			return nil, err
		}
		raw = ApplyPatch(curDef.Raw, patch)
	}
	r, err := check(q, flow.ChatID, raw, in.Validate, flow.Name, flow.AgentID, now)
	if err != nil {
		return nil, err
	}
	def := r.Definition
	hash := HashDefinition(def)
	if hash == cur.Hash {
		return &SaveResult{Flow: flow, Version: cur, Changed: false, NeedsApproval: !WritesApproved(cur), WriteCalls: r.WriteCalls, Diff: []string{}}, nil
	}
	if jsLower(def.Name) != jsLower(flow.Name) {
		if dup, err := FindFlowByName(q, flow.ChatID, def.Name); err != nil {
			return nil, err
		} else if dup != nil {
			return nil, flowErr(`A flow named "%s" already exists in this chat.`, def.Name)
		}
	}
	tz := flow.TZ
	if def.TZ != nil {
		tz = *def.TZ
	}
	if !reminders.IsValidZone(tz) {
		return nil, flowErr(`Unknown time zone "%s".`, tz)
	}
	var approved *db.FlowVersion
	if flow.ApprovedVersionID != nil {
		if approved, err = GetVersion(q, *flow.ApprovedVersionID); err != nil {
			return nil, err
		}
	}
	// Same write calls as the approved version: the approval carries over.
	carries := len(r.WriteCalls) == 0 || (approved != nil && approved.ApprovedAt != nil && SameWriteCalls(approved.WriteCalls, r.WriteCalls))
	versionID := secret.NewID("flv")
	version := &db.FlowVersion{ID: versionID, FlowID: flow.ID, N: cur.N + 1, Definition: def.Raw, Hash: hash, WriteCalls: r.WriteCalls, CreatedBy: in.CreatedBy, Note: in.Note, CreatedAt: now}
	if carries {
		version.ApprovedAt = &now
		if len(r.WriteCalls) > 0 {
			version.ApprovedVia = ptr("carried:" + approved.ID)
		} else {
			version.ApprovedVia = ptr("no_writes")
		}
	}
	next := *flow
	next.Name, next.TZ, next.CurrentVersionID, next.ConsecutiveFailures, next.LastError, next.UpdatedAt = def.Name, tz, &versionID, 0, nil, now
	if carries {
		next.ApprovedVersionID = &versionID
	}
	diff := DiffDefinitions(curDef, def)
	err = q.Tx(func(tx db.Q) error {
		if err := insertVersion(tx, version); err != nil {
			return err
		}
		if err := updateFlowRow(tx, &next); err != nil {
			return err
		}
		prev, err := TriggersOf(tx, flow.ID)
		if err != nil {
			return err
		}
		if err := writeTriggers(tx, flow.ID, DeriveTriggers(&next, def, prev, now, curDef, tz != flow.TZ)); err != nil {
			return err
		}
		if _, err := ClaimHooksForFlow(tx, webhookIDs(def), now); err != nil {
			return err
		}
		if !carries {
			return RequestVersionApproval(tx, &next, version, approved, now)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	wait := ""
	if !carries {
		wait = ", waiting for approval"
	}
	if err := logEvent(q, events.Input{Kind: "flow.updated", Message: fmt.Sprintf(`Flow "%s" changed to version %d%s.`, next.Name, version.N, wait), Data: obj("flowId", flow.ID, "versionId", versionID, "n", float64(version.N))}, now); err != nil {
		return nil, err
	}
	reminders.NotifyScheduleChanged()
	return &SaveResult{Flow: &next, Version: version, Changed: true, NeedsApproval: !carries, WriteCalls: r.WriteCalls, Diff: diff}, nil
}

// DiffDefinitions is diffDefinitions: step-level changes.
func DiffDefinitions(a, b *Definition) []string {
	out := []string{}
	type entry struct{ id, json string }
	steps := func(d *Definition) []entry {
		var es []entry
		for _, s := range d.Steps {
			es = append(es, entry{s.ID, CanonicalJSON(s.Raw)})
		}
		return es
	}
	trigs := func(d *Definition) []entry {
		var es []entry
		for _, t := range d.Triggers {
			es = append(es, entry{t.ID, CanonicalJSON(t.Raw)})
		}
		return es
	}
	// Map semantics: the last entry of an id wins, in the first one's position.
	toMap := func(es []entry) ([]string, map[string]string) {
		var order []string
		m := map[string]string{}
		for _, e := range es {
			if _, ok := m[e.id]; !ok {
				order = append(order, e.id)
			}
			m[e.id] = e.json
		}
		return order, m
	}
	cmp := func(kind string, ea, eb []entry) {
		ao, am := toMap(ea)
		bo, bm := toMap(eb)
		for _, id := range bo {
			if _, ok := am[id]; !ok {
				out = append(out, "+ "+kind+" "+id)
			}
		}
		for _, id := range ao {
			if _, ok := bm[id]; !ok {
				out = append(out, "- "+kind+" "+id)
			}
		}
		for _, id := range bo {
			if j, ok := am[id]; ok && j != bm[id] {
				out = append(out, "~ "+kind+" "+id)
			}
		}
	}
	cmp("step", steps(a), steps(b))
	cmp("trigger", trigs(a), trigs(b))
	if a.Name != b.Name {
		out = append(out, fmt.Sprintf(`~ name "%s" -> "%s"`, a.Name, b.Name))
	}
	return out
}

func signsContactMessages(q db.Q, v *db.FlowVersion) (bool, error) {
	s, err := outbound.Settings(q)
	if err != nil || !s.SignatureOn {
		return false, err
	}
	for _, w := range v.WriteCalls {
		if w.Kind == "message" && strings.HasPrefix(w.To, "contact:") && (w.Signature == nil || *w.Signature) {
			return true, nil
		}
	}
	return false, nil
}

// RequestVersionApproval is requestVersionApproval: one prompt per version
// with write calls.
func RequestVersionApproval(q db.Q, flow *db.Flow, v *db.FlowVersion, approved *db.FlowVersion, now int64) error {
	chat, ok, err := chats.Get(q, flow.ChatID)
	if err != nil || !ok {
		return err
	}
	lines := []string{fmt.Sprintf(`Flow "%s" versi %d mau jalanin aksi yang mengubah data tanpa nanya lagi tiap kali:`, flow.Name, v.N)}
	for _, w := range v.WriteCalls {
		lines = append(lines, "- "+DescribeWriteCall(w))
	}
	signs, err := signsContactMessages(q, v)
	if err != nil {
		return err
	}
	if signs {
		lines = append(lines, "", "Pesan ke kontak dikirim dengan tanda tangan dari halaman Outbound.")
	}
	if approved != nil {
		ad, err := DefinitionOf(approved)
		if err != nil {
			return err
		}
		vd, err := DefinitionOf(v)
		if err != nil {
			return err
		}
		if diff := DiffDefinitions(ad, vd); len(diff) > 0 {
			if len(diff) > 10 {
				diff = diff[:10]
			}
			lines = append(lines, "", fmt.Sprintf("Berubah dari versi %d yang disetujui: %s", approved.N, strings.Join(diff, ", ")))
		}
	}
	lines = append(lines, "", "Setujui? Balas ya atau batal (quote pesan ini). Sampai disetujui, bagian yang mengubah data nggak jalan; sisanya tetap jalan.")
	expires := now + 24*3600_000
	_, err = outbox.Enqueue(q, outbox.EnqueueInput{
		ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID,
		Payload: db.OutboxPayload{Text: strings.Join(lines, "\n"), Format: "plain", Actions: []db.OutboxAction{{ID: "flowver:approve:" + v.ID, Label: "Setujui"}, {ID: "flowver:deny:" + v.ID, Label: "Batal"}}},
		Source:  VersionApprovalSource + v.ID, ExpiresAt: &expires,
	}, now)
	if err != nil {
		return err
	}
	outbox.Notify()
	return nil
}

// ApproveResult is ApproveResult.
type ApproveResult struct {
	OK    bool
	Reply string
	Flow  *db.Flow
}

// DecideVersion is decideVersion: the owner's answer to a version prompt.
func DecideVersion(q db.Q, versionID string, approved bool, via string, now int64) (ApproveResult, error) {
	v, err := GetVersion(q, versionID)
	if err != nil {
		return ApproveResult{}, err
	}
	if v == nil {
		return ApproveResult{Reply: "Versi flow itu nggak ketemu."}, nil
	}
	flow, err := GetFlow(q, v.FlowID)
	if err != nil {
		return ApproveResult{}, err
	}
	if flow == nil {
		return ApproveResult{Reply: "Flow-nya udah dihapus."}, nil
	}
	if v.ApprovedAt != nil {
		return ApproveResult{Reply: fmt.Sprintf(`Versi %d flow "%s" udah disetujui.`, v.N, flow.Name), Flow: flow}, nil
	}
	if flow.CurrentVersionID == nil || *flow.CurrentVersionID != v.ID {
		return ApproveResult{Reply: fmt.Sprintf(`Itu versi lama flow "%s"; yang jalan sekarang versi lain.`, flow.Name), Flow: flow}, nil
	}
	rows, err := q.Query("SELECT "+db.FlowRunCols+" FROM flow_runs WHERE version_id = ? AND status = 'awaiting_approval' ORDER BY rowid", v.ID)
	waiting, err := db.All(rows, err, db.ScanFlowRun)
	if err != nil {
		return ApproveResult{}, err
	}
	if approved {
		err = q.Tx(func(tx db.Q) error {
			if _, err := tx.Exec("UPDATE flow_versions SET approved_at = ?, approved_via = ? WHERE id = ?", now, via, v.ID); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE flows SET approved_version_id = ?, updated_at = ? WHERE id = ?", v.ID, now, flow.ID); err != nil {
				return err
			}
			for _, r := range waiting {
				var err error
				// A run that waited more than a day is stale: its moment passed.
				if now-r.StartedAt > 24*3600_000 {
					_, err = tx.Exec("UPDATE flow_runs SET status = 'cancelled', skip_reason = NULL, error = ?, ended_at = ?, wait = NULL, updated_at = ? WHERE id = ?", "Approved more than a day after it stopped.", now, now, r.ID)
				} else {
					// The wait stays, so the step updates the row it left waiting.
					_, err = tx.Exec("UPDATE flow_runs SET status = 'queued', skip_reason = NULL, wake_at = ?, updated_at = ? WHERE id = ?", now, now, r.ID)
				}
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return ApproveResult{}, err
		}
		if err := logEvent(q, events.Input{Kind: "flow.approved", Message: fmt.Sprintf(`Flow "%s" version %d approved from %s.`, flow.Name, v.N, via), Data: obj("flowId", flow.ID, "versionId", v.ID)}, now); err != nil {
			return ApproveResult{}, err
		}
		reminders.NotifyScheduleChanged()
		more := ""
		if len(waiting) > 0 {
			more = fmt.Sprintf(" %d run yang nunggu lanjut sekarang.", len(waiting))
		}
		return ApproveResult{OK: true, Reply: fmt.Sprintf(`Sip, flow "%s" versi %d disetujui.%s`, flow.Name, v.N, more), Flow: flow}, nil
	}
	err = q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("UPDATE flow_versions SET approved_via = ? WHERE id = ?", "denied:"+via, v.ID); err != nil {
			return err
		}
		for _, r := range waiting {
			if _, err := tx.Exec("UPDATE flow_runs SET status = 'cancelled', error = ?, ended_at = ?, wait = NULL, updated_at = ? WHERE id = ?", "The owner didn't approve this version.", now, now, r.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ApproveResult{}, err
	}
	if err := logEvent(q, events.Input{Kind: "flow.denied", Message: fmt.Sprintf(`Flow "%s" version %d not approved.`, flow.Name, v.N), Data: obj("flowId", flow.ID, "versionId", v.ID)}, now); err != nil {
		return ApproveResult{}, err
	}
	return ApproveResult{OK: true, Reply: fmt.Sprintf(`Oke, versi %d flow "%s" nggak disetujui. Bagian yang mengubah data nggak akan jalan.`, v.N, flow.Name), Flow: flow}, nil
}

// WakeFlowRun is wakeFlowRun: a run waiting on a per-run approval wakes now
// and its step reads the decision.
func WakeFlowRun(q db.Q, runID string, now int64) error {
	if _, err := q.Exec("UPDATE flow_runs SET wake_at = ?, updated_at = ? WHERE id = ? AND status = 'waiting'", now, now, runID); err != nil {
		return err
	}
	if _, err := approvalsMarkResumed(q, runID, now); err != nil {
		return err
	}
	reminders.NotifyScheduleChanged()
	return nil
}

// --- pause and delete ----------------------------------------------------------------------

// SetFlowPaused is setFlowPaused.
func SetFlowPaused(q db.Q, id string, paused bool, until *int64, now int64, reason string) (*db.Flow, error) {
	flow, err := GetFlow(q, id)
	if err != nil {
		return nil, err
	}
	if flow == nil {
		return nil, flowErr("No flow %s.", id)
	}
	if paused && until != nil && *until <= now {
		return nil, flowErr("The pause end must be in the future.")
	}
	next := *flow
	next.Enabled = !paused
	next.PausedUntil = nil
	if paused {
		next.PausedUntil = until
		if reason != "" {
			next.LastError = &reason
		}
	} else {
		next.ConsecutiveFailures = 0
		next.LastError = nil
	}
	next.UpdatedAt = now
	_, def, err := currentDef(q, flow)
	if err != nil {
		return nil, err
	}
	rows, err := TriggersOf(q, id)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		t := &rows[i]
		if !paused {
			t.Cursor = &Cursor{At: now}
		}
		t.UpdatedAt = now
		spec := def.Trigger(t.TriggerID)
		if paused {
			t.NextFireAt, t.NextOcc = nil, nil
			continue
		}
		if spec != nil && spec.Kind == "schedule" {
			if spec.Source.Kind == "tool" {
				t.NextRefreshAt = ptr(now)
			}
			occ, err := NextScheduled(spec, flow.TZ, t.SourceState, t.Cursor, now)
			if err != nil {
				return nil, err
			}
			t.NextOcc, t.NextFireAt = occ, nil
			if occ != nil {
				t.NextFireAt = ptr(occ.At)
			}
		} else if spec != nil && spec.Kind == "poll" {
			t.NextFireAt = ptr(now)
		}
	}
	err = q.Tx(func(tx db.Q) error {
		if err := updateFlowRow(tx, &next); err != nil {
			return err
		}
		for i := range rows {
			if err := updateTriggerRow(tx, &rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if paused {
		msg := fmt.Sprintf(`Flow "%s" paused`, flow.Name)
		if until != nil {
			msg += " until a set time"
		}
		if reason != "" {
			msg += ": " + reason
		}
		if err := logEvent(q, events.Input{Kind: "flow.paused", Message: msg + ".", Data: obj("flowId", id)}, now); err != nil {
			return nil, err
		}
	}
	reminders.NotifyScheduleChanged()
	return &next, nil
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

func anyArgs(ids []string) []any {
	out := make([]any, len(ids))
	for i, s := range ids {
		out[i] = s
	}
	return out
}

// DeleteFlow is deleteFlow: the flow with its versions, triggers, runs and
// step log; pending per-run approvals expire.
func DeleteFlow(q db.Q, id string, now int64) error {
	err := q.Tx(func(tx db.Q) error {
		rows, err := tx.Query("SELECT id FROM flow_runs WHERE flow_id = ?", id)
		ids, err := db.All(rows, err, func(s db.Scanner) (string, error) {
			var x string
			return x, s.Scan(&x)
		})
		if err != nil {
			return err
		}
		for i := 0; i < len(ids); i += 500 {
			chunk := ids[i:min(len(ids), i+500)]
			if _, err := tx.Exec("DELETE FROM flow_step_runs WHERE run_id IN ("+placeholders(len(chunk))+")", anyArgs(chunk)...); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE pending_approvals SET status = 'expired', decided_at = ? WHERE run_id IN ("+placeholders(len(chunk))+") AND status = 'pending'", append([]any{now}, anyArgs(chunk)...)...); err != nil {
				return err
			}
		}
		for _, stmt := range []string{"DELETE FROM flow_runs WHERE flow_id = ?", "DELETE FROM flow_triggers WHERE flow_id = ?", "DELETE FROM flow_versions WHERE flow_id = ?", "DELETE FROM flows WHERE id = ?"} {
			if _, err := tx.Exec(stmt, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	reminders.NotifyScheduleChanged()
	return nil
}

// --- views ---------------------------------------------------------------------------------

// DescribeTrigger is describeTrigger.
func DescribeTrigger(t *Trigger) string {
	switch t.Kind {
	case "schedule":
		return DescribeSource(t.Source, t.Days, t.HasDays)
	case "webhook":
		return "Webhook " + t.HookID
	case "chat":
		if t.Command != nil && *t.Command != "" {
			return "Perintah " + *t.Command
		}
		parts := make([]string, len(t.Phrases))
		for i, p := range t.Phrases {
			parts[i] = jsonQuote(p)
		}
		return "Pesan: " + strings.Join(parts, ", ")
	case "poll":
		cid := ""
		if t.ConnectorID != nil {
			cid = *t.ConnectorID + "."
		}
		how := "kalau ada isinya"
		if t.Fire == "change" {
			how = "kalau berubah"
		}
		return fmt.Sprintf("Cek %s%s tiap %d menit (%s)", cid, t.Tool, t.EveryMin, how)
	case "event":
		from := fmt.Sprintf(`flow "%s"`, t.Flow)
		if t.Flow == "self" {
			from = "flow ini"
		}
		return fmt.Sprintf("Event %s dari %s", t.Event, from)
	case "manual":
		return "Manual"
	}
	return ""
}

// PreviewTime is one row of previewTimes.
type PreviewTime struct {
	Trigger string `json:"trigger"`
	At      int64  `json:"at"`
	Label   string `json:"label"`
	When    string `json:"when"`
}

// PreviewTimes is previewTimes: the next n times of every schedule trigger.
func PreviewTimes(q db.Q, flow *db.Flow, n int, now int64) ([]PreviewTime, error) {
	_, def, err := currentDef(q, flow)
	if err != nil {
		return nil, err
	}
	rows, err := TriggersOf(q, flow.ID)
	if err != nil {
		return nil, err
	}
	out := []PreviewTime{}
	for i := range def.Triggers {
		t := &def.Triggers[i]
		if t.Kind != "schedule" {
			continue
		}
		var state *jsonx.Object
		for _, r := range rows {
			if r.TriggerID == t.ID {
				state = r.SourceState
				break
			}
		}
		list, err := Upcoming(ScheduleInput(t, flow.TZ, state), Cursor{At: now}, n)
		if err != nil {
			continue
		}
		for _, o := range list {
			out = append(out, PreviewTime{Trigger: t.ID, At: o.At, Label: o.Label, When: reminders.FormatWhen(o.At, flow.TZ, now)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// ChatOfFlow is chatOfFlow.
func ChatOfFlow(q db.Q, flow *db.Flow) (db.Chat, bool, error) { return chats.Get(q, flow.ChatID) }

package rpcsetup

// The read-side queries of apps/app/src/server/queries.ts and
// events-stream.ts that these pages use. SQL mirrors what drizzle sends, so
// SQLite picks the same plan and rows tie the same way.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

func or(title *string, fallback string) string {
	if title != nil && *title != "" {
		return *title
	}
	return fallback
}

// startOfDay is startOfDay(tz, now).
func startOfDay(tz string, now int64) int64 {
	return zone.StartOfDay(now, zone.MustLoad(tz))
}

// chatTitles is chatTitles: a chat's title, or its channel id.
func chatTitles(q db.Q) (map[string]string, error) {
	rows, err := q.Query("SELECT id, title, external_id FROM chats")
	type row struct {
		id, ext string
		title   *string
	}
	list, err := db.All(rows, err, func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.id, &r.title, &r.ext)
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, r := range list {
		out[r.id] = or(r.title, r.ext)
	}
	return out, nil
}

func jobNames(q db.Q) (map[string]string, error) {
	rows, err := q.Query("SELECT id, name FROM jobs")
	type row struct{ id, name string }
	list, err := db.All(rows, err, func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.id, &r.name)
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, r := range list {
		out[r.id] = r.name
	}
	return out, nil
}

// --- reminders, chats, home checklist ---------------------------------------------

type nextReminder struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	FireAt int64   `json:"fireAt"`
	TZ     string  `json:"tz"`
	RRule  *string `json:"rrule"`
}

// nextReminders is nextReminders(db, n, now).
func nextReminders(q db.Q, n int, now int64) ([]nextReminder, error) {
	rows, err := q.Query("SELECT id, text, fire_at, tz, rrule FROM reminders WHERE status = 'pending' AND fire_at >= ? ORDER BY fire_at ASC LIMIT ?", now-3600_000, n)
	return db.All(rows, err, func(s db.Scanner) (nextReminder, error) {
		var r nextReminder
		return r, s.Scan(&r.ID, &r.Text, &r.FireAt, &r.TZ, &r.RRule)
	})
}

type chatOption struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Title   string `json:"title"`
}

// chatOptions is chatOptions: the allowed chats, for the hook form.
func chatOptions(q db.Q) ([]chatOption, error) {
	rows, err := q.Query("SELECT id, title, channel, external_id FROM chats WHERE allowed = 1 ORDER BY channel ASC, title ASC")
	return db.All(rows, err, func(s db.Scanner) (chatOption, error) {
		var c chatOption
		var title *string
		var ext string
		err := s.Scan(&c.ID, &title, &c.Channel, &ext)
		c.Title = or(title, ext)
		return c, err
	})
}

type facts struct{ passkeys, userMessages int64 }

// firstRunFacts is firstRunFacts.
func firstRunFacts(q db.Q) (facts, error) {
	var f facts
	if err := q.QueryRow("SELECT count(*) FROM passkey").Scan(&f.passkeys); err != nil {
		return f, err
	}
	err := q.QueryRow("SELECT count(*) FROM messages WHERE role = 'user' AND status <> 'ignored'").Scan(&f.userMessages)
	return f, err
}

// --- runs and usage ----------------------------------------------------------------

type runItem struct {
	ID        string  `json:"id"`
	Trigger   string  `json:"trigger"`
	Model     *string `json:"model"`
	Status    string  `json:"status"`
	Error     *string `json:"error"`
	TokensIn  int64   `json:"tokensIn"`
	TokensOut int64   `json:"tokensOut"`
	Tainted   bool    `json:"tainted"`
	StartedAt int64   `json:"startedAt"`
	Ms        *int64  `json:"ms"`
	Chat      *string `json:"chat"`
	Job       *string `json:"job"`
	ChatGone  bool    `json:"chatGone"`
	JobGone   bool    `json:"jobGone"`
}

func lookup(m map[string]string, id *string) (*string, bool) {
	if id == nil || *id == "" {
		return nil, false
	}
	v, ok := m[*id]
	if !ok {
		return nil, true
	}
	return &v, false
}

// recentRuns is recentRuns(db, limit).
func recentRuns(q db.Q, limit int) ([]runItem, error) {
	titles, err := chatTitles(q)
	if err != nil {
		return nil, err
	}
	jobs, err := jobNames(q)
	if err != nil {
		return nil, err
	}
	res, qerr := q.Query("SELECT "+db.RunCols+" FROM runs ORDER BY started_at DESC LIMIT ?", limit)
	rows, err := db.All(res, qerr, db.ScanRun)
	if err != nil {
		return nil, err
	}
	out := make([]runItem, 0, len(rows))
	for _, r := range rows {
		chat, chatGone := lookup(titles, r.ChatID)
		job, jobGone := lookup(jobs, r.JobID)
		out = append(out, runItem{ID: r.ID, Trigger: r.Trigger, Model: r.Model, Status: r.Status, Error: r.Error, TokensIn: r.TokensIn, TokensOut: r.TokensOut, Tainted: r.Tainted, StartedAt: r.StartedAt, Ms: r.Ms, Chat: chat, Job: job, ChatGone: chatGone, JobGone: jobGone})
	}
	return out, nil
}

func runCount(q db.Q) (int64, error) {
	var n int64
	return n, q.QueryRow("SELECT count(*) FROM runs").Scan(&n)
}

type tokens struct {
	TokensIn  int64 `json:"tokensIn"`
	TokensOut int64 `json:"tokensOut"`
	Runs      int64 `json:"runs"`
}

// tokensSince is tokensSince(db, since).
func tokensSince(q db.Q, since int64) (tokens, error) {
	var t tokens
	err := q.QueryRow("SELECT coalesce(sum(tokens_in), 0), coalesce(sum(tokens_out), 0), count(*) FROM runs WHERE started_at >= ?", since).Scan(&t.TokensIn, &t.TokensOut, &t.Runs)
	return t, err
}

// pretty is JSON.stringify(v, null, 2).
func pretty(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return "null"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return string(b)
	}
	return buf.String()
}

type toolCallView struct {
	ID          string  `json:"id"`
	Tool        string  `json:"tool"`
	ConnectorID *string `json:"connectorId"`
	Class       string  `json:"class"`
	Status      string  `json:"status"`
	Ms          *int64  `json:"ms"`
	Args        string  `json:"args"`
	Result      *string `json:"result"`
}

// argsText is the transcript's args: "" for none, else pretty JSON cut at
// 2000 UTF-16 units.
func argsText(raw db.RawJSON) string {
	if len(raw) == 0 {
		return ""
	}
	v, err := jsonx.Parse(raw)
	if err != nil || v == nil {
		return ""
	}
	return strx.Slice16(pretty(v), 2000)
}

type runMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"createdAt"`
}

type runDetailView struct {
	ID               string         `json:"id"`
	Trigger          string         `json:"trigger"`
	Model            *string        `json:"model"`
	Status           string         `json:"status"`
	Error            *string        `json:"error"`
	ResultPreview    *string        `json:"resultPreview"`
	TokensIn         int64          `json:"tokensIn"`
	TokensOut        int64          `json:"tokensOut"`
	TokensCached     int64          `json:"tokensCached"`
	TokensCacheWrite int64          `json:"tokensCacheWrite"`
	TokensCounted    int64          `json:"tokensCounted"`
	Steps            int64          `json:"steps"`
	Tainted          bool           `json:"tainted"`
	StartedAt        int64          `json:"startedAt"`
	Ms               *int64         `json:"ms"`
	ChatID           *string        `json:"chatId"`
	JobID            *string        `json:"jobId"`
	Chat             *string        `json:"chat"`
	Job              *string        `json:"job"`
	ChatGone         bool           `json:"chatGone"`
	JobGone          bool           `json:"jobGone"`
	Prompt           []runMessage   `json:"prompt"`
	Reply            []runMessage   `json:"reply"`
	Tools            []toolCallView `json:"tools"`
}

// runDetail is runDetail(db, id): nil for an unknown id.
func runDetail(q db.Q, id string) (*runDetailView, error) {
	r, found, err := db.One(q.QueryRow("SELECT "+db.RunCols+" FROM runs WHERE id = ?", id), db.ScanRun)
	if err != nil || !found {
		return nil, err
	}
	v := &runDetailView{ID: r.ID, Trigger: r.Trigger, Model: r.Model, Status: r.Status, Error: r.Error, ResultPreview: r.ResultPreview, TokensIn: r.TokensIn, TokensOut: r.TokensOut, TokensCached: r.TokensCached, TokensCacheWrite: r.TokensCacheWrite, Steps: r.Steps, Tainted: r.Tainted, StartedAt: r.StartedAt, Ms: r.Ms, Prompt: []runMessage{}, Reply: []runMessage{}, Tools: []toolCallView{}}
	cached := r.TokensCached
	v.TokensCounted = budget.Tokens(budget.Usage{TokensIn: r.TokensIn, TokensOut: r.TokensOut, TokensCached: &cached})
	if r.ChatID != nil && *r.ChatID != "" {
		var title *string
		var ext string
		switch err := q.QueryRow("SELECT title, external_id FROM chats WHERE id = ?", *r.ChatID).Scan(&title, &ext); {
		case err == nil:
			t := or(title, ext)
			v.Chat, v.ChatID = &t, r.ChatID
		case isNoRows(err):
			v.ChatGone = true
		default:
			return nil, err
		}
	}
	if r.JobID != nil && *r.JobID != "" {
		var name string
		switch err := q.QueryRow("SELECT name FROM jobs WHERE id = ?", *r.JobID).Scan(&name); {
		case err == nil:
			v.Job, v.JobID = &name, r.JobID
		case isNoRows(err):
			v.JobGone = true
		default:
			return nil, err
		}
	}
	res, qerr := q.Query("SELECT id, role, text, created_at FROM messages WHERE run_id = ? AND role IN ('user', 'assistant') ORDER BY created_at ASC", id)
	msgs, err := db.All(res, qerr, func(s db.Scanner) (runMessage, error) {
		var m runMessage
		return m, s.Scan(&m.ID, &m.Role, &m.Text, &m.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		if strx.Trim(m.Text) == "" {
			continue
		}
		switch m.Role {
		case "user":
			v.Prompt = append(v.Prompt, m)
		case "assistant":
			v.Reply = append(v.Reply, m)
		}
	}
	res, qerr = q.Query("SELECT "+db.ToolCallCols+" FROM tool_calls WHERE run_id = ? ORDER BY created_at ASC", id)
	calls, err := db.All(res, qerr, db.ScanToolCall)
	if err != nil {
		return nil, err
	}
	for _, c := range calls {
		v.Tools = append(v.Tools, toolCallView{ID: c.ID, Tool: c.Tool, ConnectorID: c.ConnectorID, Class: c.Class, Status: c.Status, Ms: c.Ms, Args: argsText(c.Args), Result: c.ResultPreview})
	}
	return v, nil
}

type usageDay struct {
	Day       string `json:"day"`
	TokensIn  int64  `json:"tokensIn"`
	TokensOut int64  `json:"tokensOut"`
}

type usageRow struct {
	Key    string `json:"key"`
	Tokens int64  `json:"tokens"`
	Runs   int64  `json:"runs"`
}

type usageTop struct {
	Rows  []usageRow `json:"rows"`
	Total int        `json:"total"`
}

type usageView struct {
	Days     []usageDay `json:"days"`
	PerModel usageTop   `json:"perModel"`
	PerAgent usageTop   `json:"perAgent"`
	PerChat  usageTop   `json:"perChat"`
	PerJob   usageTop   `json:"perJob"`
}

// tally is a Map<string, {key, tokens, runs}> in insertion order.
type tally struct {
	rows  []usageRow
	index map[string]int
}

func (t *tally) bump(key string, n int64) {
	if t.index == nil {
		t.index = map[string]int{}
	}
	i, ok := t.index[key]
	if !ok {
		i = len(t.rows)
		t.index[key] = i
		t.rows = append(t.rows, usageRow{Key: key})
	}
	t.rows[i].Tokens += n
	t.rows[i].Runs++
}

// top is the 20 biggest (a stable sort, ties keep their first-seen order)
// and how many there were.
func (t *tally) top() usageTop {
	rows := append([]usageRow{}, t.rows...)
	stableSortDesc(rows)
	if len(rows) > 20 {
		rows = rows[:20]
	}
	return usageTop{Rows: rows, Total: len(t.rows)}
}

func stableSortDesc(rows []usageRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Tokens > rows[j-1].Tokens; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// usage is usage(db, tz, days, now): tokens per day in the owner's zone and
// per model, agent, chat and job over the last `days` days.
func usage(q db.Q, tz string, days int, now int64) (usageView, error) {
	loc := zone.MustLoad(tz)
	since := zone.PlusDays(zone.StartOfDay(now, loc), loc, -(days - 1))
	res, qerr := q.Query("SELECT "+db.RunCols+" FROM runs WHERE started_at >= ?", since)
	rows, err := db.All(res, qerr, db.ScanRun)
	if err != nil {
		return usageView{}, err
	}
	dayOf := func(ms int64) string { return time.UnixMilli(ms).In(loc).Format("2006-01-02") }
	out := usageView{Days: make([]usageDay, 0, days)}
	dayIndex := map[string]int{}
	for i := 0; i < days; i++ {
		d := dayOf(zone.PlusDays(since, loc, i))
		if _, dup := dayIndex[d]; dup {
			continue
		}
		dayIndex[d] = len(out.Days)
		out.Days = append(out.Days, usageDay{Day: d})
	}
	titles, err := chatTitles(q)
	if err != nil {
		return usageView{}, err
	}
	jobs, err := jobNames(q)
	if err != nil {
		return usageView{}, err
	}
	agentRows, err := q.Query("SELECT id, name, is_default FROM agents")
	type agentRow struct {
		id, name  string
		isDefault bool
	}
	list, err := db.All(agentRows, err, func(s db.Scanner) (agentRow, error) {
		var a agentRow
		return a, s.Scan(&a.id, &a.name, &a.isDefault)
	})
	if err != nil {
		return usageView{}, err
	}
	// Runs from before agents (agent_id null) were all the default agent's.
	agentNames := map[string]string{}
	defaultAgent, found := "", false
	for _, a := range list {
		agentNames[a.id] = a.name
		if a.isDefault && !found {
			defaultAgent, found = a.name, true
		}
	}
	if !found {
		defaultAgent = agents.DefaultName
	}
	var perModel, perAgent, perChat, perJob tally
	for _, r := range rows {
		if i, ok := dayIndex[dayOf(r.StartedAt)]; ok {
			out.Days[i].TokensIn += r.TokensIn
			out.Days[i].TokensOut += r.TokensOut
		}
		total := r.TokensIn + r.TokensOut
		model := "unknown"
		if r.Model != nil {
			model = *r.Model
		}
		perModel.bump(model, total)
		agent := defaultAgent
		if r.AgentID != nil && *r.AgentID != "" {
			agent = agentNames[*r.AgentID]
		}
		perAgent.bump(agent, total)
		if r.ChatID != nil && *r.ChatID != "" {
			perChat.bump(titles[*r.ChatID], total)
		}
		if r.JobID != nil && *r.JobID != "" {
			perJob.bump(jobs[*r.JobID], total)
		}
	}
	out.PerModel, out.PerAgent, out.PerChat, out.PerJob = perModel.top(), perAgent.top(), perChat.top(), perJob.top()
	return out, nil
}

// --- events ------------------------------------------------------------------------

type eventFilter struct {
	connectorID string
	levels      []string
}

func (f eventFilter) where() (string, []any) {
	var parts []string
	var args []any
	if f.connectorID != "" {
		parts = append(parts, "connector_id = ?")
		args = append(args, f.connectorID)
	}
	if len(f.levels) > 0 {
		parts = append(parts, "level IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(f.levels)), ", ")+")")
		for _, l := range f.levels {
			args = append(args, l)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

type eventItem struct {
	ID          int64   `json:"id"`
	At          int64   `json:"at"`
	Level       string  `json:"level"`
	ConnectorID *string `json:"connectorId"`
	Kind        string  `json:"kind"`
	Message     string  `json:"message"`
}

// eventList is eventList: newest first.
func eventList(q db.Q, f eventFilter, limit int) ([]eventItem, error) {
	where, args := f.where()
	res, qerr := q.Query("SELECT "+db.EventCols+" FROM events"+where+" ORDER BY id DESC LIMIT ?", append(args, limit)...)
	rows, err := db.All(res, qerr, db.ScanEvent)
	if err != nil {
		return nil, err
	}
	out := make([]eventItem, 0, len(rows))
	for _, e := range rows {
		out = append(out, eventItem{ID: e.ID, At: e.At, Level: e.Level, ConnectorID: e.ConnectorID, Kind: e.Kind, Message: e.Message})
	}
	return out, nil
}

// eventCount is eventCount: the same filters, no cap.
func eventCount(q db.Q, f eventFilter) (int64, error) {
	where, args := f.where()
	var n int64
	return n, q.QueryRow("SELECT count(*) FROM events"+where, args...).Scan(&n)
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

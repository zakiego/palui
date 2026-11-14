package chat_test

import (
	"encoding/json"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// world is basic()'s result: the harness, the Telegram fake and the model.
type world struct {
	*chattest.Harness
	t  *testing.T
	tg *conntest.FakeChannel
	m  *models.ScriptedModel
}

type extra struct {
	registry      map[string]*connectors.Runtime
	rows          []chattest.Row
	turnTimeoutMs int64
	deps          chat.Deps
}

// basic is basic(): the owner on Telegram, cliproxy as the only model.
func basic(t *testing.T, steps []models.Step, x extra) *world {
	t.Helper()
	d := testutil.DB(t)
	chattest.OwnTelegram(t, d)
	chattest.ModelChain(t, d, "cliproxy")
	tg := conntest.NewFakeChannel("telegram")
	m := models.NewScripted(steps...)
	reg := map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", m), "telegram": conntest.ChannelRuntime("telegram", tg)}
	for k, v := range x.registry {
		reg[k] = v
	}
	rows := append([]chattest.Row{{"cliproxy", "model"}, {"telegram", "channel"}}, x.rows...)
	h := chattest.New(t, chattest.Options{DB: d, Registry: reg, Rows: rows, TurnTimeoutMs: x.turnTimeoutMs, Deps: x.deps})
	return &world{Harness: h, t: t, tg: tg, m: m}
}

func tgMsg(text string, mod ...func(m *connectors.InboundMessage)) connectors.InboundMessage {
	m := conntest.TgMessage(text)
	for _, f := range mod {
		f(&m)
	}
	return m
}

func (w *world) send(text string, mod ...func(m *connectors.InboundMessage)) *chat.InboundResult {
	w.t.Helper()
	r, err := w.Engine.Inbound(tgMsg(text, mod...))
	testutil.Must(w.t, err)
	return r
}

func (w *world) sendIdle(text string, mod ...func(m *connectors.InboundMessage)) *chat.InboundResult {
	w.t.Helper()
	r := w.send(text, mod...)
	w.Engine.Idle()
	return r
}

func outboxRows(t *testing.T, q db.Q) []db.Outbox {
	t.Helper()
	rows, err := q.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY rowid")
	return need(db.All(rows, err, db.ScanOutbox))
}

func outboxTexts(t *testing.T, q db.Q) []string {
	t.Helper()
	var out []string
	for _, r := range outboxRows(t, q) {
		out = append(out, r.Payload.Text)
	}
	return out
}

func lastText(t *testing.T, q db.Q) string {
	t.Helper()
	texts := outboxTexts(t, q)
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func scalar[T any](t *testing.T, q db.Q, query string, args ...any) T {
	t.Helper()
	var v T
	testutil.Must(t, q.QueryRow(query, args...).Scan(&v))
	return v
}

func count(t *testing.T, q db.Q, query string, args ...any) int {
	return scalar[int](t, q, "SELECT count(*) FROM "+query, args...)
}

func allRuns(t *testing.T, q db.Q) []db.Run {
	rows, err := q.Query("SELECT " + db.RunCols + " FROM runs ORDER BY rowid")
	return need(db.All(rows, err, db.ScanRun))
}

func allApprovals(t *testing.T, q db.Q, where string, args ...any) []db.Approval {
	if where == "" {
		where = "1 = 1"
	}
	rows, err := q.Query("SELECT "+db.ApprovalCols+" FROM pending_approvals WHERE "+where+" ORDER BY rowid", args...)
	return need(db.All(rows, err, db.ScanApproval))
}

func allToolCalls(t *testing.T, q db.Q) []db.ToolCall {
	rows, err := q.Query("SELECT " + db.ToolCallCols + " FROM tool_calls ORDER BY rowid")
	return need(db.All(rows, err, db.ScanToolCall))
}

func firstChat(t *testing.T, q db.Q) db.Chat {
	t.Helper()
	return need(db.ScanChat(q.QueryRow("SELECT " + db.ChatCols + " FROM chats ORDER BY rowid LIMIT 1")))
}

func chatWhere(t *testing.T, q db.Q, where string, args ...any) db.Chat {
	t.Helper()
	return need(db.ScanChat(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE "+where+" ORDER BY rowid LIMIT 1", args...)))
}

func messageByID(t *testing.T, q db.Q, id string) db.Message {
	t.Helper()
	return need(db.ScanMessage(q.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE id = ?", id)))
}

func prompt(m *models.ScriptedModel, i int) string {
	if i < 0 {
		i += len(m.Prompts)
	}
	return models.PromptText(m.Prompts[i])
}

func setBudget(t *testing.T, q db.Q, fn func(b *settings.Budgets)) {
	b := need(settings.GetBudgets(q))
	fn(&b)
	testutil.Must(t, settings.Set(q, "budgets", b, 1))
}

// tool is a PaluiTool over fnTool.
func tool(class string, fn func(in *jsonx.Object) (any, error)) connectors.PaluiTool {
	return connectors.PaluiTool{Tool: conntest.FnTool(class, fn), Class: class}
}

func ret(v any) func(*jsonx.Object) (any, error) {
	return func(*jsonx.Object) (any, error) { return v, nil }
}

func str(in *jsonx.Object, k string) string {
	v, _ := in.Get(k)
	s, _ := v.(string)
	return s
}

func tools(pairs ...any) func() *connectors.ToolSet {
	return func() *connectors.ToolSet { return conntest.Tools(pairs...) }
}

func toolsRT(id string, set func() *connectors.ToolSet) *connectors.Runtime {
	return conntest.ToolsRuntime(id, set, "", nil)
}

func objOf(kv ...any) *jsonx.Object { return jsonObj(kv...) }

var _ = json.Marshal

// found is (v, ok, err) to v; it panics when the row is missing or on err.
func found[T any](v T, ok bool, err error) T {
	if err != nil {
		panic(err)
	}
	if !ok {
		panic("not found")
	}
	return v
}

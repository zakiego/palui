package chat_test

// Turn retry after a temporary failure (rpt_0mwmcmitl94zpj4htnc: a TLS
// handshake timeout to the model gateway dropped a group message with "coba
// lagi nanti").

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func netDown() models.Step {
	return models.Fail(&models.APICallError{Message: `Cannot connect to API: Post "https://gateway.test/v1/messages": net/http: TLS handshake timeout`, IsRetryable: true})
}

func eventCount(t *testing.T, w *world, kind string) int {
	n := 0
	for _, e := range listEvents(t, w.DB) {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func countText(texts []string, sub string) int {
	n := 0
	for _, x := range texts {
		if strings.Contains(x, sub) {
			n++
		}
	}
	return n
}

func TestTurnRetry(t *testing.T) {
	t.Run("keeps the message on a network error and answers it once the model is back", func(t *testing.T) {
		w := basic(t, nil, extra{})
		w.m.WithFallback(netDown())
		r := w.sendIdle("halo")
		// The first failure is silent: a short blip shouldn't spam the chat.
		eq(t, len(outboxTexts(t, w.DB)), 0)
		eq(t, count(t, w.DB, "messages WHERE id = ? AND status = 'pending' AND run_id IS NULL", r.MessageID), 1)
		jsonEq(t, w.Engine.RetryWaiting(), fmt.Sprintf(`[%q]`, r.MessageID))
		eq(t, eventCount(t, w, "turn.retry"), 1)
		run := allRuns(t, w.DB)[0]
		eq(t, run.Status, "error")
		contains(t, *run.Error, "TLS handshake timeout")
		contains(t, *run.Error, "retry 1 in")

		w.m.WithFallback(models.Say("udah bisa, ini jawabannya"))
		w.Engine.RetryNow()
		w.Engine.Idle()
		jsonEq(t, outboxTexts(t, w.DB), `["udah bisa, ini jawabannya"]`)
		eq(t, count(t, w.DB, "messages WHERE id = ? AND status = 'handled'", r.MessageID), 1)
		eq(t, len(w.Engine.RetryWaiting()), 0)
		eq(t, allRuns(t, w.DB)[1].Status, "ok")
	})

	t.Run("tells the chat once on the second failure and gives up after the cap", func(t *testing.T) {
		w := basic(t, nil, extra{})
		w.m.WithFallback(netDown())
		w.sendIdle("halo")
		for range chat.RetryDelaysMs {
			w.Engine.RetryNow()
			w.Engine.Idle()
		}
		texts := outboxTexts(t, w.DB)
		eq(t, countText(texts, "Pesannya aku simpan"), 1)
		eq(t, countText(texts, "Waduh, gagal jawab"), 1)
		eq(t, len(texts), 2)
		eq(t, len(allRuns(t, w.DB)), len(chat.RetryDelaysMs)+1)
		eq(t, eventCount(t, w, "turn.retry"), len(chat.RetryDelaysMs))
		eq(t, eventCount(t, w, "turn.retry_exhausted"), 1)
		eq(t, count(t, w.DB, "messages WHERE status = 'pending'"), 0)
		eq(t, len(w.Engine.RetryWaiting()), 0)
	})

	t.Run("doesn't retry a permanent error", func(t *testing.T) {
		w := basic(t, nil, extra{})
		w.m.WithFallback(models.Fail(models.NewHTTPError(401)))
		w.sendIdle("halo")
		contains(t, lastText(t, w.DB), "Waduh, gagal jawab")
		eq(t, eventCount(t, w, "turn.retry"), 0)
		eq(t, len(w.Engine.RetryWaiting()), 0)
		eq(t, count(t, w.DB, "messages WHERE status = 'pending'"), 0)
	})

	t.Run("doesn't retry a turn that already ran a write tool", func(t *testing.T) {
		w := basic(t, []models.Step{models.CallTool("notes__add", map[string]any{"text": "beli gula"}, "c1")}, extra{
			registry: map[string]*connectors.Runtime{"notes": toolsRT("notes", tools("add", connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("add", ret("ok"))}))},
			rows:     []chattest.Row{{"notes", "tools"}},
		})
		w.m.WithFallback(netDown())
		w.sendIdle("catat beli gula")
		eq(t, count(t, w.DB, "tool_calls WHERE tool = 'notes__add' AND status = 'ok'"), 1)
		eq(t, eventCount(t, w, "turn.retry"), 0)
		contains(t, lastText(t, w.DB), "Waduh, gagal jawab")
	})

	t.Run("a new message joins the waiting retry and one reply covers both", func(t *testing.T) {
		w := basic(t, nil, extra{})
		w.m.WithFallback(netDown())
		first := w.sendIdle("halo")
		w.m.WithFallback(models.Say("jawab dua-duanya"))
		second := w.sendIdle("masih di sana?")
		jsonEq(t, outboxTexts(t, w.DB), `["jawab dua-duanya"]`)
		eq(t, len(w.Engine.RetryWaiting()), 0)
		runs := allRuns(t, w.DB)
		eq(t, len(runs), 2)
		eq(t, count(t, w.DB, "messages WHERE id IN (?, ?) AND run_id = ?", first.MessageID, second.MessageID, runs[1].ID), 2)
		p := models.PromptText(w.m.PromptsSeen()[len(w.m.PromptsSeen())-1])
		contains(t, p, "halo")
		contains(t, p, "masih di sana?")
		// The timer that was armed for the first message finds nothing to do.
		w.Engine.RetryNow()
		w.Engine.Idle()
		eq(t, len(allRuns(t, w.DB)), 2)
	})

	t.Run("drops a retried message that outlived the window", func(t *testing.T) {
		w := basic(t, nil, extra{})
		w.m.WithFallback(netDown())
		r := w.sendIdle("halo")
		calls := len(w.m.PromptsSeen())
		w.m.WithFallback(models.Say("telat"))
		w.Clock.Add(chat.RetryWindowMs + 60_000)
		w.Engine.RetryNow()
		w.Engine.Idle()
		eq(t, len(w.m.PromptsSeen()), calls)
		jsonEq(t, outboxTexts(t, w.DB), fmt.Sprintf(`[%q]`, chat.RetryExpiredText))
		eq(t, count(t, w.DB, "messages WHERE id = ? AND status = 'ignored' AND ignore_reason = 'retry window passed'", r.MessageID), 1)
		eq(t, eventCount(t, w, "turn.retry_expired"), 1)
	})

	t.Run("a group answer after a retry quotes the original message, with no busy text", func(t *testing.T) {
		h := agentsWorld(t, nil, extra{})
		h.owner("/agent chef")
		h.Engine.Idle()
		h.m.WithFallback(netDown())
		h.guest("@bot ini warung apa?", func(m *connectors.InboundMessage) { m.ChannelMsgID = "G1" })
		h.Engine.Idle()
		h.Engine.RetryNow()
		h.Engine.Idle()
		h.m.WithFallback(models.Say("Itu warung lalapan"))
		h.Engine.RetryNow()
		h.Engine.Idle()
		rows := outboxRows(t, h.DB)
		texts := outboxTexts(t, h.DB)
		eq(t, countText(texts, chat.SharedBusyText), 0)
		eq(t, countText(texts, chat.RetryHoldText), 1)
		last := rows[len(rows)-1]
		eq(t, last.Payload.Text, "Itu warung lalapan")
		eq(t, last.Payload.ReplyToChannelMsgID, "G1")
	})

	t.Run("a message waiting for a retry is answered after a restart", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.OwnTelegram(t, d)
		chattest.ModelChain(t, d, "cliproxy")
		down := models.NewScripted().WithFallback(netDown())
		rows := []chattest.Row{{"cliproxy", "model"}, {"telegram", "channel"}}
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", down), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}, Rows: rows})
		r := need(h.Engine.Inbound(tgMsg("halo")))
		h.Engine.Idle()
		eq(t, count(t, d, "messages WHERE id = ? AND status = 'pending'", r.MessageID), 1)
		// Deploy: the old process drains and stops, the new one starts.
		h.Engine.Stop(100)
		h.Sup.Stop()
		eq(t, count(t, d, "messages WHERE id = ? AND status = 'pending'", r.MessageID), 1)

		up := models.NewScripted(models.Say("udah nyala lagi"))
		h2 := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", up), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}, Rows: rows})
		testutil.Must(t, h2.Engine.Start())
		h2.Engine.Idle()
		eq(t, lastText(t, d), "udah nyala lagi")
		eq(t, count(t, d, "messages WHERE id = ? AND status = 'handled'", r.MessageID), 1)
	})
}

package chat

// Turn retry: a message turn that failed for a temporary reason (the model
// unreachable, rate limited or overloaded, or its connector still starting)
// isn't dropped. Its messages go back to `pending` and the engine runs them
// again with backoff, or as soon as a connector comes back on. A message
// that arrives in the meantime joins the same turn, so one reply covers both.

import (
	"fmt"
	"slices"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
)

const (
	// RetryWindowMs: a message older than this isn't retried any more; the
	// conversation has moved on.
	RetryWindowMs = 15 * 60_000
	// Sent once, on the second failure, so a short blip stays silent.
	RetryHoldText      = "Lagi ada gangguan koneksi. Pesannya aku simpan, nanti aku jawab sendiri begitu nyambung lagi."
	RetryHoldOwnerText = "Model lagi gak bisa dihubungi (%s). Pesannya aku simpan, nanti aku coba lagi otomatis."
	// Sent once, on the second failed transcription of the owner's voice note.
	RetryVoiceHoldText = "Voice note-mu belum bisa ditranskrip (%s). Audionya aku simpan, nanti aku coba lagi otomatis."
	// A retried message that outlived the window after a restart.
	RetryExpiredText = "Maaf, pesan tadi gak sempat kejawab karena gangguan. Kirim ulang ya kalau masih perlu."
)

// RetryDelaysMs are the waits before each retry; their count is the cap.
var RetryDelaysMs = []int64{20_000, 60_000, 120_000, 300_000}

// retryKind is the event kind of one scheduled retry.
const retryKind = "turn.retry"

func (c *EngineCtx) retryDelays() []int64 {
	if c.RetryDelaysMs != nil {
		return c.RetryDelaysMs
	}
	return RetryDelaysMs
}

// countRetries is how many times any of these messages was already queued
// again after a temporary failure.
func countRetries(q db.Q, ids []string) (int, error) {
	best := 0
	for _, id := range ids {
		var n int
		if err := q.QueryRow("SELECT count(*) FROM events WHERE kind = ? AND data LIKE ?", retryKind, "%\""+id+"\"%").Scan(&n); err != nil {
			return 0, err
		}
		best = max(best, n)
	}
	return best, nil
}

// batchTimes is the oldest and newest created_at of the messages.
func batchTimes(q db.Q, ids []string) (oldest, newest int64, err error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	err = q.QueryRow("SELECT coalesce(min(created_at), 0), coalesce(max(created_at), 0) FROM messages WHERE id IN ("+placeholders(len(ids))+")", args...).Scan(&oldest, &newest)
	return oldest, newest, err
}

// quoteTarget is the channel id of the oldest message in the batch: a retried
// answer in a group quotes it, since other talk came in between.
func quoteTarget(q db.Q, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var ref *string
	err := q.QueryRow("SELECT channel_msg_id FROM messages WHERE id IN ("+placeholders(len(ids))+") AND channel_msg_id IS NOT NULL ORDER BY created_at ASC, id ASC LIMIT 1", args...).Scan(&ref)
	if err != nil || ref == nil {
		return ""
	}
	return *ref
}

// wroteData says whether the run already ran a write or destructive tool, so
// running it again could do the thing twice.
func wroteData(q db.Q, runID string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	return exists(q, "SELECT id FROM tool_calls WHERE run_id = ? AND class IN ('write', 'destructive') AND status IN ('ok', 'error')", runID)
}

// modelStarting says whether the role's chain has an enabled model connector
// that isn't up yet (starting, restarting after an error): it will be back,
// so an empty chain is temporary. A chain that is off or missing is not.
func modelStarting(q db.Q, own db.AgentModels, role string) (bool, error) {
	roles, err := settings.Get[settings.ModelRoles](q, "modelRoles")
	if err != nil {
		return false, err
	}
	for _, link := range models.EffectiveChain(roles, own, role) {
		row, ok, err := connectors.Get(q, link.ConnectorID)
		if err != nil {
			return false, err
		}
		if ok && row.Enabled && (row.Status == "connecting" || row.Status == "error") {
			return true, nil
		}
	}
	return false, nil
}

// retryPlan is the verdict on a failed message turn.
type retryPlan struct {
	ok      bool
	attempt int // 1 for the first retry
	delayMs int64
	// Why it wasn't retried, for the log ("" when ok).
	reason string
}

// planRetry decides whether a failed message turn runs again.
func planRetry(c *EngineCtx, ids []string, runID string, cause error, transient bool) (retryPlan, error) {
	if len(ids) == 0 {
		return retryPlan{reason: "no messages"}, nil
	}
	if !transient {
		return retryPlan{reason: "permanent error"}, nil
	}
	wrote, err := wroteData(c.DB, runID)
	if err != nil {
		return retryPlan{}, err
	}
	if wrote {
		return retryPlan{reason: "a write tool already ran"}, nil
	}
	done, err := countRetries(c.DB, ids)
	if err != nil {
		return retryPlan{}, err
	}
	delays := c.retryDelays()
	if done >= len(delays) {
		return retryPlan{reason: fmt.Sprintf("gave up after %d retries", done)}, nil
	}
	_, newest, err := batchTimes(c.DB, ids)
	if err != nil {
		return retryPlan{}, err
	}
	if c.Clock()-newest > RetryWindowMs {
		return retryPlan{reason: fmt.Sprintf("message older than %d minutes", RetryWindowMs/60_000)}, nil
	}
	return retryPlan{ok: true, attempt: done + 1, delayMs: delays[done]}, nil
}

// requeue puts the messages back to `pending` and records the retry.
func requeue(c *EngineCtx, ids []string, runID string, plan retryPlan, errMsg string) error {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := c.DB.Exec("UPDATE messages SET status = 'pending', run_id = NULL WHERE id IN ("+placeholders(len(ids))+")", args...); err != nil {
		return err
	}
	what := "Turn"
	if runID != "" {
		what = "Run " + runID
	}
	c.event(events.Input{Kind: retryKind, Level: "warn",
		Message: fmt.Sprintf("%s failed for a temporary reason; %d message(s) retried in %ds (retry %d of %d): %s", what, len(ids), plan.delayMs/1000, plan.attempt, len(c.retryDelays()), shortError(errMsg)),
		Data:    obj("runId", runID, "messageIds", ids, "attempt", plan.attempt, "delayMs", plan.delayMs)}, c.Clock())
	return nil
}

// --- engine side ---------------------------------------------------------------------------

type retryWait struct {
	ids   []string
	timer *time.Timer
}

// scheduleRetry runs the messages again after delayMs. Nothing is armed while
// draining: the messages stay pending and the next start replays them.
func (e *Engine) scheduleRetry(chatID string, ids []string, delayMs int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Ctx.Draining() {
		return
	}
	if e.retrying == nil {
		e.retrying = map[string]*retryWait{}
	}
	w := e.retrying[chatID]
	if w == nil {
		w = &retryWait{}
		e.retrying[chatID] = w
	}
	for _, id := range ids {
		if !slices.Contains(w.ids, id) {
			w.ids = append(w.ids, id)
		}
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() { e.fireRetry(chatID) })
}

// takeRetry removes the chat's waiting messages and stops their timer.
func (e *Engine) takeRetry(chatID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	w := e.retrying[chatID]
	if w == nil {
		return nil
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	delete(e.retrying, chatID)
	return w.ids
}

func (e *Engine) fireRetry(chatID string) {
	ids := e.takeRetry(chatID)
	if e.Ctx.Draining() {
		return
	}
	for _, id := range ids {
		e.queue.PushMessage(chatID, id)
	}
}

// RetryNow runs every waiting retry at once: a connector just came back on.
func (e *Engine) RetryNow() {
	e.mu.Lock()
	var chatIDs, voiceIDs []string
	for id := range e.retrying {
		chatIDs = append(chatIDs, id)
	}
	for id := range e.mediaRetry {
		voiceIDs = append(voiceIDs, id)
	}
	e.mu.Unlock()
	for _, id := range voiceIDs {
		e.fireMediaRetry(id)
	}
	if len(chatIDs) > 0 {
		e.Ctx.Log.Info("connector back on; retrying failed turns now", logx.Fields{"chats": len(chatIDs)})
	}
	for _, id := range chatIDs {
		e.fireRetry(id)
	}
}

// RetryWaiting lists the message ids waiting for a retry (tests, dashboard).
func (e *Engine) RetryWaiting() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, w := range e.retrying {
		out = append(out, w.ids...)
	}
	for id := range e.mediaRetry {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (e *Engine) stopRetries() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, w := range e.retrying {
		if w.timer != nil {
			w.timer.Stop()
		}
	}
	e.retrying = nil
	for _, w := range e.mediaRetry {
		w.timer.Stop()
	}
	e.mediaRetry = nil
}

// --- voice notes -----------------------------------------------------------------------------
//
// An owner's voice note whose transcription failed for a temporary reason
// gets the same backoff, cap and window as a turn. It waits apart from the
// turn retries: it goes back through dispatch (PrepareMedia), never straight
// into a turn, so a newer message can't pull it in untranscribed.

type mediaWait struct {
	chatID string
	timer  *time.Timer
}

// retryTranscription queues the voice note for another try. False when the
// retries are used up (or the note is too old), so the caller reports the
// failure in the note instead.
func (e *Engine) retryTranscription(chat db.Chat, row db.Message, cause *TranscribeLaterError) (bool, error) {
	c := e.Ctx
	ids := []string{row.ID}
	plan, err := planRetry(c, ids, "", cause, true)
	if err != nil {
		return false, err
	}
	if !plan.ok {
		c.Log.Warn("voice note not transcribed; not retried", logx.Fields{"message_id": row.ID, "reason": plan.reason, "error": cause.Cause})
		c.event(events.Input{Kind: "turn.retry_exhausted", Level: "warn", Message: fmt.Sprintf("Voice note %s wasn't transcribed and wasn't retried: %s", row.ID, plan.reason), Data: obj("messageIds", ids)}, c.Clock())
		return false, nil
	}
	if err := requeue(c, ids, "", plan, cause.Error()); err != nil {
		return false, err
	}
	if plan.attempt == 2 {
		c.send(chat, fmt.Sprintf(RetryVoiceHoldText, shortError(cause.Cause)), "system", SendExtra{})
	}
	e.scheduleMediaRetry(chat.ID, row.ID, plan.delayMs)
	return true, nil
}

func (e *Engine) scheduleMediaRetry(chatID, messageID string, delayMs int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Ctx.Draining() {
		return
	}
	if e.mediaRetry == nil {
		e.mediaRetry = map[string]*mediaWait{}
	}
	if w := e.mediaRetry[messageID]; w != nil {
		w.timer.Stop()
	}
	e.mediaRetry[messageID] = &mediaWait{chatID: chatID, timer: time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() { e.fireMediaRetry(messageID) })}
}

// fireMediaRetry routes the voice note again. While its last dispatch is
// still winding down, it waits a moment, so the route isn't skipped.
func (e *Engine) fireMediaRetry(messageID string) {
	e.mu.Lock()
	w := e.mediaRetry[messageID]
	if w == nil {
		e.mu.Unlock()
		return
	}
	w.timer.Stop()
	if e.routed[messageID] {
		w.timer = time.AfterFunc(20*time.Millisecond, func() { e.fireMediaRetry(messageID) })
		e.mu.Unlock()
		return
	}
	delete(e.mediaRetry, messageID)
	e.mu.Unlock()
	if e.Ctx.Draining() {
		return
	}
	e.routeInOrder(w.chatID, messageID)
}

// dropExpired drops retried messages that outlived the retry window (a
// restart replays them long after): they're marked ignored and the chat is
// told once. It returns the messages still worth a turn.
func (e *Engine) dropExpired(chat db.Chat, ids []string) ([]string, error) {
	c := e.Ctx
	now := c.Clock()
	var keep, dropped []string
	for _, id := range ids {
		n, err := countRetries(c.DB, []string{id})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			keep = append(keep, id)
			continue
		}
		_, at, err := batchTimes(c.DB, []string{id})
		if err != nil {
			return nil, err
		}
		if now-at > RetryWindowMs {
			dropped = append(dropped, id)
		} else {
			keep = append(keep, id)
		}
	}
	if len(dropped) == 0 {
		return keep, nil
	}
	for _, id := range dropped {
		if _, err := c.DB.Exec("UPDATE messages SET status = 'ignored', ignore_reason = 'retry window passed' WHERE id = ?", id); err != nil {
			return nil, err
		}
	}
	c.event(events.Input{Kind: "turn.retry_expired", Level: "warn", Message: fmt.Sprintf("%d message(s) waited for a retry longer than %d minutes and were dropped", len(dropped), RetryWindowMs/60_000), Data: obj("chatId", chat.ID, "messageIds", dropped)}, now)
	if len(keep) == 0 {
		c.send(chat, RetryExpiredText, "system", SendExtra{})
	}
	return keep, nil
}

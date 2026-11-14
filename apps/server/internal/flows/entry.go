package flows

// Entry points that start flow runs without waiting for the timer (entry.ts,
// RFC flows 4.1, 7.1): a verified webhook in hook mode `flow`, and a `chat`
// trigger the router matched before the model.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// HookHeaders are the headers a webhook flow can read (trigger.headers).
var HookHeaders = []string{"content-type", "user-agent", "x-palui-delivery", "x-github-event", "x-github-delivery", "x-gitlab-event", "x-event-key"}

// HookPayload is hookPayload: the parsed JSON body, or {text} for anything else.
func HookPayload(raw string) any {
	if v, err := jsonx.Parse([]byte(raw)); err == nil {
		return v
	}
	return obj("text", strx.Slice16(raw, 8000))
}

var deliveryRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,120}$`)

// DeliveryKey is deliveryKey: the sender's delivery id when it sends one,
// else a hash of the body. headers holds lowercase names; a missing name is
// JS undefined.
func DeliveryKey(raw string, headers map[string]string) string {
	id, ok := headers["x-palui-delivery"]
	if !ok {
		id, ok = headers["x-github-delivery"]
	}
	if ok && deliveryRe.MatchString(id) {
		return "hook:" + id
	}
	sum := sha256.Sum256([]byte(raw))
	return "hook:" + hex.EncodeToString(sum[:])[:40]
}

// IngestResult is ingestFlowHook's answer.
type IngestResult struct {
	Started   int `json:"started"`
	Skipped   int `json:"skipped"`
	Listeners int `json:"listeners"`
}

// IngestFlowHook is ingestFlowHook: a verified request to a hook in mode
// `flow` starts one run per flow whose webhook trigger names the hook. The
// whole payload is untrusted (trigger is tainted). M8's POST /api/hooks/:slug
// calls it after hooks.VerifyRequest when hook.Mode is "flow".
func IngestFlowHook(q db.Q, hook db.Hook, raw string, headers map[string]string, now int64) (IngestResult, error) {
	rows, err := q.Query("SELECT "+db.FlowTriggerCols+" FROM flow_triggers WHERE kind = 'webhook' AND match = ? ORDER BY rowid", hook.ID)
	list, err := db.All(rows, err, db.ScanFlowTrigger)
	if err != nil {
		return IngestResult{}, err
	}
	kept := jsonx.NewMap[any]()
	for _, h := range HookHeaders {
		if v, ok := headers[h]; ok && v != "" {
			kept.Set(h, strx.Slice16(v, 200))
		}
	}
	payload := obj("kind", "webhook", "payload", HookPayload(raw), "headers", kept, "receivedAt", float64(now), "hook", hook.Name)
	key := DeliveryKey(raw, headers)
	out := IngestResult{Listeners: len(list)}
	for _, row := range list {
		tid := row.TriggerID
		r, err := StartFlowRun(q, StartInput{FlowID: row.FlowID, TriggerID: &tid, TriggerKind: "webhook", RunKey: key, Payload: payload, Taint: []string{"trigger"}, Now: now})
		if err != nil {
			return out, err
		}
		if r.Run != nil && r.Skipped == "" {
			out.Started++
		} else {
			out.Skipped++
		}
	}
	if _, err := q.Exec("UPDATE hooks SET last_at = ? WHERE id = ?", now, hook.ID); err != nil {
		return out, err
	}
	if len(list) == 0 {
		if err := logEvent(q, events.Input{Level: "warn", Kind: "hook.unrouted", Message: fmt.Sprintf(`Hook "%s" is in flow mode but no flow listens to it.`, hook.Name), Data: obj("hookId", hook.ID)}, now); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ChatInput is matchChatTrigger's input.
type ChatInput struct {
	ChatID    string
	Text      string
	MessageID string
	FromOwner bool
}

var atBot = regexp.MustCompile(`@\S+$`)
var wsSplit = regexp.MustCompile(`[` + strx.SpaceClass + `]+`)

// MatchChatTrigger is matchChatTrigger: the owner's command or phrase in the
// flow's chat. True when a flow took it (the message never reaches a turn).
func MatchChatTrigger(q db.Q, in ChatInput, now int64) (bool, error) {
	if !in.FromOwner {
		return false, nil
	}
	text := strx.Trim(in.Text)
	if text == "" {
		return false, nil
	}
	rows, err := q.Query("SELECT " + db.FlowTriggerCols + " FROM flow_triggers WHERE kind = 'chat' ORDER BY rowid")
	list, err := db.All(rows, err, db.ScanFlowTrigger)
	if err != nil {
		return false, err
	}
	hit := false
	for _, row := range list {
		flow, err := GetFlow(q, row.FlowID)
		if err != nil {
			return hit, err
		}
		if flow == nil || flow.ChatID != in.ChatID {
			continue
		}
		_, def, err := currentDef(q, flow)
		if err != nil {
			return hit, err
		}
		t := def.Trigger(row.TriggerID)
		if t == nil || t.Kind != "chat" {
			continue
		}
		var args *string
		if t.Command != nil && *t.Command != "" {
			words := wsSplit.Split(text, -1)
			if atBot.ReplaceAllString(jsLower(words[0]), "") == *t.Command {
				args = ptr(strings.Join(words[1:], " "))
			}
		} else {
			for _, p := range t.Phrases {
				if NormalizeAnswer(p) == NormalizeAnswer(text) {
					args = ptr("")
					break
				}
			}
		}
		if args == nil {
			continue
		}
		tid := t.ID
		payload := obj("kind", "chat", "text", strx.Slice16(text, 2000), "args", strx.Slice16(*args, 2000), "messageId", in.MessageID)
		if _, err := StartFlowRun(q, StartInput{FlowID: flow.ID, TriggerID: &tid, TriggerKind: "chat", RunKey: "chat:" + in.MessageID, Payload: payload, Now: now}); err != nil {
			return hit, err
		}
		hit = true
	}
	return hit, nil
}

// ClaimHooksForFlow is claimHooksForFlow: saving a flow with a webhook trigger
// puts that hook in mode `flow`.
func ClaimHooksForFlow(q db.Q, hookIDs []string, now int64) ([]string, error) {
	changed := []string{}
	for _, id := range hookIDs {
		h, ok, err := db.One(q.QueryRow("SELECT "+db.HookCols+" FROM hooks WHERE id = ?", id), db.ScanHook)
		if err != nil {
			return changed, err
		}
		if !ok || h.Mode == "flow" {
			continue
		}
		if _, err := q.Exec("UPDATE hooks SET mode = 'flow' WHERE id = ?", id); err != nil {
			return changed, err
		}
		what := "summarizing"
		if h.Mode == "forward" {
			what = "forwarding"
		}
		if err := logEvent(q, events.Input{Kind: "hook.mode", Message: fmt.Sprintf(`Hook "%s" now starts flows instead of %s its events.`, h.Name, what), Data: obj("hookId", id)}, now); err != nil {
			return changed, err
		}
		changed = append(changed, h.Name)
	}
	return changed, nil
}

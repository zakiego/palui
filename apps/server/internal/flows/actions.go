package flows

// What the router handles for flows before any model sees a message
// (actions.ts):
//   FromCallback  Telegram buttons "fl:<runId>:<answerId>" (check-in answers)
//                 and "flowver:approve|deny:<versionId>" (version approvals)
//   FromText      a reply quoting a check-in that matches one answer exactly,
//                 or "ya"/"batal" quoting a version prompt
//   FromChat      a `chat` trigger: the owner's command or phrase
// Actions implements chat.FlowActions.

import (
	"errors"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var (
	answerRe  = regexp.MustCompile(`^fl:([A-Za-z0-9_-]+):([a-z0-9_]{1,20})$`)
	versionRe = regexp.MustCompile(`^flowver:(approve|deny):([A-Za-z0-9_-]+)$`)
)

// BareYesWindowMs: a bare "ya" answers a version prompt only this soon after it went out.
const BareYesWindowMs = 30 * 60_000

func record(q db.Q, runID, answer, chatID string, now int64, text string) (string, error) {
	res, err := AnswerFlowCheckin(q, AnswerInput{ChatID: chatID, RunID: runID, Answer: answer, Text: text}, now)
	if err != nil {
		var fe *FlowError
		if errors.As(err, &fe) {
			return "Gagal: " + fe.Msg, nil
		}
		return "", err
	}
	return res.Reply, nil
}

func decideFromChat(q db.Q, versionID string, approved bool, chatID string, now int64) (string, error) {
	v, err := GetVersion(q, versionID)
	if err != nil {
		return "", err
	}
	var flow *db.Flow
	if v != nil {
		if flow, err = GetFlow(q, v.FlowID); err != nil {
			return "", err
		}
	}
	// Only the chat that was asked (the flow's own chat) can answer.
	if v == nil || flow == nil || flow.ChatID != chatID {
		return "Persetujuan flow itu gak ketemu di chat ini.", nil
	}
	r, err := DecideVersion(q, versionID, approved, "chat", now)
	return r.Reply, err
}

// Actions is flowActions.
type Actions struct{}

var _ chat.FlowActions = Actions{}

func (Actions) FromCallback(q db.Q, data, chatID string, now int64) (string, bool, error) {
	if m := answerRe.FindStringSubmatch(data); m != nil {
		r, err := record(q, m[1], m[2], chatID, now, "")
		return r, err == nil, err
	}
	if m := versionRe.FindStringSubmatch(data); m != nil {
		r, err := decideFromChat(q, m[2], m[1] == "approve", chatID, now)
		return r, err == nil, err
	}
	return "", false, nil
}

func (Actions) FromText(q db.Q, text, replyTo, chatID string, now int64) (string, bool, error) {
	if strx.Trim(text) == "" {
		return "", false, nil
	}
	if replyTo == "" {
		// A bare "ya"/"batal" right after a single version prompt answers it.
		yes, ok := chat.ParseYesNo(text)
		if !ok {
			return "", false, nil
		}
		rows, err := q.Query("SELECT source FROM outbox WHERE chat_id = ? AND source LIKE ? AND created_at >= ? ORDER BY rowid", chatID, VersionApprovalSource+"%", now-BareYesWindowMs)
		sources, err := db.All(rows, err, func(s db.Scanner) (string, error) {
			var x string
			return x, s.Scan(&x)
		})
		if err != nil {
			return "", false, err
		}
		var recent []string
		for _, src := range sources {
			id := strings.TrimPrefix(src, VersionApprovalSource)
			v, err := GetVersion(q, id)
			if err != nil {
				return "", false, err
			}
			if v == nil || v.ApprovedAt != nil || (v.ApprovedVia != nil && *v.ApprovedVia != "") {
				continue
			}
			f, err := GetFlow(q, v.FlowID)
			if err != nil {
				return "", false, err
			}
			if f != nil && f.CurrentVersionID != nil && *f.CurrentVersionID == v.ID {
				recent = append(recent, id)
			}
		}
		if len(recent) != 1 {
			return "", false, nil
		}
		r, err := decideFromChat(q, recent[0], yes, chatID, now)
		return r, err == nil, err
	}
	rows, err := q.Query("SELECT source, chat_id FROM outbox WHERE channel_msg_id = ? ORDER BY rowid", replyTo)
	type hit struct{ source, chatID string }
	list, err := db.All(rows, err, func(s db.Scanner) (hit, error) {
		var h hit
		return h, s.Scan(&h.source, &h.chatID)
	})
	if err != nil {
		return "", false, err
	}
	var matched []hit
	for _, h := range list {
		if strings.HasPrefix(h.source, "flow:") || strings.HasPrefix(h.source, VersionApprovalSource) {
			matched = append(matched, h)
		}
	}
	if len(matched) == 0 {
		return "", false, nil
	}
	pick := matched[0]
	for _, h := range matched {
		if h.chatID == chatID {
			pick = h
			break
		}
	}
	if strings.HasPrefix(pick.source, VersionApprovalSource) {
		yes, ok := chat.ParseYesNo(text)
		if !ok {
			return "", false, nil
		}
		r, err := decideFromChat(q, strings.TrimPrefix(pick.source, VersionApprovalSource), yes, chatID, now)
		return r, err == nil, err
	}
	run, err := GetRun(q, strings.TrimPrefix(pick.source, "flow:"))
	if err != nil || run == nil {
		return "", false, err
	}
	w := CheckinWaitOf(run)
	if w == nil {
		return "", false, nil
	}
	step, _, err := stepOf(q, run, w.StepID)
	if err != nil || step == nil {
		return "", false, err
	}
	// Not an exact answer ("udah tapi telat"): let the model read it.
	option := MatchAnswer(step.Answers, text)
	if option == nil {
		return "", false, nil
	}
	r, err := record(q, run.ID, option.ID, chatID, now, text)
	return r, err == nil, err
}

func (Actions) FromChat(q db.Q, in chat.ChatTrigger, now int64) (bool, error) {
	return MatchChatTrigger(q, ChatInput{ChatID: in.ChatID, Text: in.Text, MessageID: in.MessageID, FromOwner: in.FromOwner}, now)
}

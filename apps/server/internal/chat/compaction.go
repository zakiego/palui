package chat

// Compaction (compaction.ts, PLAN 8.9): past about 40 messages, the fast
// model folds the older ones into sessions.summary and they leave the context
// (they stay in the DB). Auto-save to the memory connector runs here too.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/memory"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	CompactAfter        = 40
	KeepAfterCompaction = 20
	// /compact: fold everything but the last few messages, however short the session.
	KeepAfterManual = 6
)

// CompactResult is CompactResult. Status is ok, short (nothing old enough to
// fold), no_model (no fast model running), failed or no_session.
type CompactResult struct {
	Status string
	Folded int
}

// GenerateText is generateText({model, system, prompt, maxRetries: 1,
// abortSignal: AbortSignal.timeout(timeout)}) for one step with no tools: the
// reply text, or the error the call failed with.
func GenerateText(ctx context.Context, model models.LanguageModel, system, prompt string, timeout time.Duration, retryDelay time.Duration) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var text strings.Builder
	var failure error
	aborted := false
	sys := []models.Message{}
	if system != "" {
		sys = append(sys, models.System(system))
	}
	RunLoop(tctx, LoopOptions{
		Model:      model,
		System:     sys,
		Messages:   []models.Message{models.User(prompt)},
		MaxRetries: 1,
		RetryDelay: retryDelay,
		Emit: func(p Part) {
			switch p.Type {
			case "start-step":
				text.Reset()
			case "text-delta":
				text.WriteString(p.Text)
			case "error":
				if failure == nil {
					failure = p.Err
				}
			case "abort":
				aborted = true
			}
		},
	})
	if failure != nil {
		return "", failure
	}
	if aborted {
		if err := context.Cause(tctx); err != nil {
			return "", err
		}
		return "", errors.New("aborted")
	}
	return text.String(), nil
}

// MaybeCompact is maybeCompact: after a turn, compact once the session is past CompactAfter.
func MaybeCompact(c *EngineCtx, sessionID string) (bool, error) {
	r, err := CompactSession(c, sessionID, CompactAfter, KeepAfterCompaction)
	return r.Status == "ok", err
}

// CompactSession is compactSession: fold the session's messages into its
// summary when there are more than after, keeping the newest keep in context.
func CompactSession(c *EngineCtx, sessionID string, after, keep int) (CompactResult, error) {
	q := c.DB
	session, ok, err := chats.GetSession(q, sessionID)
	if err != nil {
		return CompactResult{}, err
	}
	if !ok {
		return CompactResult{Status: "no_session"}, nil
	}
	var until0 int64
	if session.SummarizedUntil != nil {
		until0 = *session.SummarizedUntil
	}
	rows, err := q.Query("SELECT "+db.MessageCols+" FROM messages WHERE "+inSessionWhere+" ORDER BY created_at ASC, id ASC", sessionID, until0)
	all, err := db.All(rows, err, db.ScanMessage)
	if err != nil {
		return CompactResult{}, err
	}
	if len(all) <= after {
		return CompactResult{Status: "short"}, nil
	}
	fold := all[:len(all)-keep]
	until := fold[len(fold)-1].CreatedAt
	// The chat's agent may run its own fast model.
	var own db.AgentModels
	if row, found, err := chats.Get(q, session.ChatID); err != nil {
		return CompactResult{}, err
	} else if found {
		agent, err := agents.ForChat(q, row.AgentID, c.Clock())
		if err != nil {
			return CompactResult{}, err
		}
		own = agent.ModelRoles.V
	}
	chain, err := models.ResolveAgentChain(q, c.Host, own, "fast")
	if err != nil {
		return CompactResult{}, err
	}
	if len(chain) == 0 {
		c.Log.Warn("compaction skipped: no fast model running", logx.Fields{"session_id": sessionID})
		return CompactResult{Status: "no_model"}, nil
	}
	lines := make([]string, len(fold))
	for i, m := range fold {
		who := "Palui"
		if m.Role == "user" {
			who = SenderLabel(m.FromOwner, m.SenderName, m.SenderID)
		}
		lines[i] = fmt.Sprintf("[%s]: %s", who, m.Text)
	}
	earlier := ""
	if session.Summary != nil && *session.Summary != "" {
		earlier = fmt.Sprintf("Earlier summary:\n%s\n\n", *session.Summary)
	}
	fm, err := models.NewFailoverModel(chain, nil)
	if err != nil {
		return CompactResult{}, err
	}
	out, err := GenerateText(context.Background(), models.MeterToBudget(q, fm, c.Clock),
		"You compress a chat between the owner, anyone else in it, and their assistant Palui into a short running summary for Palui's context. Each line starts with who wrote it in brackets: [Owner] or [Owner (name)] is the owner, any other name is someone else in the chat, never the owner. Keep track of who said or asked what. Keep facts, decisions, open tasks, names, dates and preferences. Drop small talk. Write in the conversation's language, at most 250 words. Output only the summary.",
		earlier+"New messages:\n"+strings.Join(lines, "\n"), 60*time.Second, c.RetryDelay)
	if err != nil {
		c.Log.Warn("compaction failed", logx.Fields{"session_id": sessionID, "error": err})
		return CompactResult{Status: "failed"}, nil
	}
	summary := strx.Trim(out)
	if summary == "" {
		return CompactResult{Status: "failed"}, nil
	}
	if _, err := q.Exec("UPDATE sessions SET summary = ?, summarized_until = ? WHERE id = ?", summary, until, sessionID); err != nil {
		c.Log.Warn("compaction failed", logx.Fields{"session_id": sessionID, "error": err})
		return CompactResult{Status: "failed"}, nil
	}
	if _, err := memory.AutoSave(context.Background(), q, memory.Find(c.Host.Running()), sessionID, c.Log, &until, c.Clock()); err != nil {
		return CompactResult{}, err
	}
	return CompactResult{Status: "ok", Folded: len(fold)}, nil
}

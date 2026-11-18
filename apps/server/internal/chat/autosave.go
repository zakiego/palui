package chat

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

const (
	// How often auto-saved files past their retention are deleted.
	AutoSavePruneMs = 6 * 3600_000
	// A full quota is reported at most this often per chat.
	autoSaveQuotaWarnMs = 6 * 3600_000
)

// MediaSaved: whether a channel should download this message's media for the
// chat's auto-save (chats.save_media). The engine still saves only what passes
// the sender check, so a stranger's file is never kept.
func MediaSaved(q db.Q, msg connectors.InboundMessage) (bool, error) {
	if msg.FromMe {
		return false, nil
	}
	chat, found, err := chats.Find(q, msg.Channel, msg.ChatExternalID, ChatScope(msg.Channel, msg.ConnectorID))
	if err != nil || !found {
		return false, err
	}
	return chat.SaveMedia && chat.Allowed, nil
}

// autoSavable is the media of msg auto-save keeps: photos and documents with
// their bytes, never stickers or audio.
func autoSavable(media []connectors.InboundMedia) []connectors.InboundMedia {
	var out []connectors.InboundMedia
	for _, m := range media {
		if m.Data != "" && !m.Sticker && (m.Kind == "image" || m.Kind == "document") {
			out = append(out, m)
		}
	}
	return out
}

// autoSave keeps the files off the message path: a slow or failed save never
// holds up the reply, and a failure is only logged. The saved rows are data
// for later lookups; nothing here touches the turn, its taint or approvals.
func (e *Engine) autoSave(chatID, messageID string, msg connectors.InboundMessage, media []connectors.InboundMedia) {
	c := e.Ctx
	key := e.opts.MasterKey
	if key == "" {
		key, _ = config.MasterKey(config.Get())
	}
	e.track(func() {
		for _, m := range media {
			b, err := base64.StdEncoding.DecodeString(m.Data)
			if err != nil {
				c.Log.Warn("auto-save skipped a file it couldn't decode", logx.Fields{"chat_id": chatID, "message_id": messageID})
				continue
			}
			sentAt := msg.At
			if sentAt == 0 {
				sentAt = c.Clock()
			}
			saved, err := attachments.AutoSave(c.DB, attachments.AutoInput{
				ChatID: chatID, MessageID: messageID, Kind: m.Kind, MimeType: m.MimeType, Filename: m.Filename,
				Caption: m.Caption, SenderID: msg.SenderExternalID, SenderName: msg.SenderName, SentAt: sentAt, Bytes: b,
			}, key, c.Clock())
			switch {
			case err == nil:
				if !saved.Deduped {
					c.Log.Debug("auto-saved a file", logx.Fields{"chat_id": chatID, "attachment_id": saved.Row.ID, "size": saved.Row.Size})
				}
			case attachments.IsError(err):
				// A type or size the store refuses (a zip, a 12 MB scan): expected.
				c.Log.Debug("auto-save skipped a file", logx.Fields{"chat_id": chatID, "reason": err.Error()})
			case errors.Is(err, attachments.ErrQuota):
				if e.quotaWarnDue(chatID, c.Clock()) {
					c.event(events.Input{Level: "warn", ConnectorID: ptrOrNil(msg.ConnectorID), Kind: "attachments.autosave", Message: fmt.Sprintf("Auto-save skipped a file in chat %s: %s. Delete auto-saved files on the Attachments page or shorten how long they stay.", chatID, err.Error()), Data: map[string]any{"chatId": chatID}}, c.Clock())
				}
			default:
				c.Log.Error("auto-save failed", logx.Fields{"chat_id": chatID, "message_id": messageID, "error": err})
				c.event(events.Input{Level: "error", ConnectorID: ptrOrNil(msg.ConnectorID), Kind: "attachments.autosave", Message: "Auto-save failed: " + err.Error(), Data: map[string]any{"chatId": chatID}}, c.Clock())
			}
		}
	})
}

func (e *Engine) quotaWarnDue(chatID string, now int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.quotaWarned == nil {
		e.quotaWarned = map[string]int64{}
	}
	if last, ok := e.quotaWarned[chatID]; ok && now-last < autoSaveQuotaWarnMs {
		return false
	}
	e.quotaWarned[chatID] = now
	return true
}

// PruneAutoSaved deletes auto-saved files past their chat's retention, at most
// once per AutoSavePruneMs.
func (e *Engine) PruneAutoSaved(now int64) {
	e.mu.Lock()
	if e.lastAutoPrune != 0 && now-e.lastAutoPrune < AutoSavePruneMs {
		e.mu.Unlock()
		return
	}
	e.lastAutoPrune = now
	e.mu.Unlock()
	n, err := attachments.PruneAuto(e.Ctx.DB, now)
	if err != nil {
		e.Ctx.Log.Error("pruning auto-saved files failed", logx.Fields{"error": err})
		return
	}
	if n > 0 {
		e.Ctx.Log.Info("pruned auto-saved files past their retention", logx.Fields{"count": n})
	}
}

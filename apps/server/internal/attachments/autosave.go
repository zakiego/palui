package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Who saved a row.
const (
	OriginTool = "tool"
	OriginAuto = "auto"
)

const (
	// Auto-saved bytes one chat may hold. A file that would go over is skipped.
	AutoChatQuotaBytes = 500 * 1024 * 1024
	// Auto-saved bytes across every chat, so a few busy groups can't fill the disk.
	AutoTotalQuotaBytes = 2 * 1024 * 1024 * 1024
	// How long an auto-saved file stays when the owner picks nothing else.
	DefaultAutoDays = 30
)

// AutoDays are the retention choices, in days. 0 keeps files until deleted.
var AutoDays = []int64{7, 30, 90, 365, 0}

func ValidAutoDays(n int64) bool { return slices.Contains(AutoDays, n) }

// ErrQuota: the file was skipped because auto-saved files hit a quota.
var ErrQuota = errors.New("auto-save quota reached")

// AutoInput is one file from an inbound message in a chat that auto-saves.
type AutoInput struct {
	ChatID    string
	MessageID string
	// "image" or "document", as the channel reported it.
	Kind       string
	MimeType   string
	Filename   string
	Caption    string
	SenderID   string
	SenderName string
	SentAt     int64
	Bytes      []byte
}

// AutoTitle names an auto-saved file: a document's file name, else the first
// line of its caption, else what it is and who sent it.
func AutoTitle(in AutoInput) string {
	if in.Kind == "document" {
		if name := strx.Trim(in.Filename); name != "" {
			return CleanFilename(name)
		}
	}
	if first := strx.Trim(strings.SplitN(strx.Trim(in.Caption), "\n", 2)[0]); first != "" {
		return strx.Slice16(first, 80)
	}
	what := "Foto"
	if in.Kind == "document" {
		what = "Dokumen"
	}
	if name := strx.Trim(in.SenderName); name != "" {
		return what + " dari " + strx.Slice16(name, 60)
	}
	return what
}

// AutoUsage is how many auto-saved bytes this chat and all chats hold.
func AutoUsage(q db.Q, chatID string) (chat, total int64, err error) {
	err = q.QueryRow("SELECT coalesce(sum(CASE WHEN chat_id = ? THEN size ELSE 0 END), 0), coalesce(sum(size), 0) FROM saved_attachments WHERE origin = ?", chatID, OriginAuto).Scan(&chat, &total)
	return chat, total, err
}

// AutoSave keeps one inbound file. A file already saved in this chat (by
// either path) is Deduped and costs nothing. Refusals are *Error (type, size)
// or wrap ErrQuota.
func AutoSave(q db.Q, in AutoInput, masterKey string, now int64) (Saved, error) {
	saveMu.Lock()
	defer saveMu.Unlock()
	if _, err := CheckFile(in.MimeType, in.Bytes); err != nil {
		return Saved{}, err
	}
	sum := sha256.Sum256(in.Bytes)
	existing, ok, err := db.One(q.QueryRow("SELECT "+db.AttachmentMetaCols+" FROM saved_attachments WHERE chat_id = ? AND sha256 = ?", in.ChatID, hex.EncodeToString(sum[:])), db.ScanAttachmentMeta)
	if err != nil {
		return Saved{}, err
	}
	if ok {
		return Saved{Row: existing, Deduped: true}, nil
	}
	chat, total, err := AutoUsage(q, in.ChatID)
	if err != nil {
		return Saved{}, err
	}
	size := int64(len(in.Bytes))
	if chat+size > AutoChatQuotaBytes {
		return Saved{}, fmt.Errorf("%w: this chat already holds %s of auto-saved files (limit %s)", ErrQuota, FormatBytes(chat), FormatBytes(AutoChatQuotaBytes))
	}
	if total+size > AutoTotalQuotaBytes {
		return Saved{}, fmt.Errorf("%w: all chats already hold %s of auto-saved files (limit %s)", ErrQuota, FormatBytes(total), FormatBytes(AutoTotalQuotaBytes))
	}
	var messageID *string
	if in.MessageID != "" {
		messageID = &in.MessageID
	}
	filename := ""
	if in.Kind == "document" {
		filename = in.Filename
	}
	return save(q, SaveInput{
		ChatID: in.ChatID, MessageID: messageID, Title: AutoTitle(in), Description: in.Caption,
		MimeType: in.MimeType, Filename: filename, Bytes: in.Bytes,
		Origin: OriginAuto, SenderID: in.SenderID, SenderName: in.SenderName, SentAt: in.SentAt,
	}, masterKey, now)
}

// expiredAuto selects auto-saved files older than their chat's retention.
const expiredAuto = "SELECT a.id FROM saved_attachments a INNER JOIN chats c ON c.id = a.chat_id WHERE a.origin = 'auto' AND c.save_media_days > 0 AND a.created_at < ? - c.save_media_days * 86400000"

// PruneAuto deletes auto-saved files past their chat's retention, with their
// share links. Files saved with save_attachment are never pruned.
func PruneAuto(q db.Q, now int64) (int64, error) {
	var n int64
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("DELETE FROM attachment_shares WHERE attachment_id IN ("+expiredAuto+")", now); err != nil {
			return err
		}
		var err error
		n, err = db.Changes(tx.Exec("DELETE FROM saved_attachments WHERE id IN ("+expiredAuto+")", now))
		return err
	})
	return n, err
}

// DeleteAuto deletes every auto-saved file of a chat, with their share links.
func DeleteAuto(q db.Q, chatID string) (int64, error) {
	var n int64
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("DELETE FROM attachment_shares WHERE attachment_id IN (SELECT id FROM saved_attachments WHERE chat_id = ? AND origin = 'auto')", chatID); err != nil {
			return err
		}
		var err error
		n, err = db.Changes(tx.Exec("DELETE FROM saved_attachments WHERE chat_id = ? AND origin = 'auto'", chatID))
		return err
	})
	return n, err
}

// AutoCount is how many auto-saved files a chat holds and their total size.
func AutoCount(q db.Q, chatID string) (count, bytes int64, err error) {
	err = q.QueryRow("SELECT count(*), coalesce(sum(size), 0) FROM saved_attachments WHERE chat_id = ? AND origin = 'auto'", chatID).Scan(&count, &bytes)
	return count, bytes, err
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

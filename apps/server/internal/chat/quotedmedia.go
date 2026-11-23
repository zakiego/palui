package chat

// Photos a turn shows besides the batch's own (ADR 0003): the photo a batch
// message replies to, and the rest of the album a batch photo came in. Group
// talk keeps its photos only for ContextMediaKeepMs, so these are mostly
// someone else's photos from the last day, in the same chat.

import (
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

const (
	// How long a photo in group talk keeps its bytes.
	ContextMediaKeepMs = 24 * 3600_000
	// How often expired group talk photos are cleared.
	ContextMediaPruneMs = 3600_000
	// At most this many photos are attached to one batch message.
	MaxAttachedImages = 4
	// What PruneContextMedia leaves in StoredMedia.Note.
	mediaExpired = "expired"
	// Rows cleared per prune pass; the next pass takes the rest.
	contextPruneBatch = 500
)

// AttachedImages are photos shown with one batch message, after Note.
type AttachedImages struct {
	Note   string
	Images []StoredMedia
}

// keepsContextPhoto: whether group talk keeps this media's bytes. Only a
// photo (not a sticker) in a group, within ContextPhotoMaxBytes.
func keepsContextPhoto(msg connectors.InboundMessage, m connectors.InboundMedia) bool {
	return msg.ChatKind == "group" && m.Kind == "image" && !m.Sticker && m.Data != "" &&
		decodedSize(m.Data) <= connectors.ContextPhotoMaxBytes
}

// albumQuote is the quote of a WhatsApp album: its first caption, who sent
// it and how many photos it holds. Nil when no stored row is in that album.
func albumQuote(q db.Q, chatID, albumID string, msg connectors.InboundMessage) (*StoredQuote, error) {
	rows, err := albumRows(q, chatID, albumID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	text := ""
	images := 0
	for _, r := range rows {
		c := ParseContent(r.Content)
		if text == "" {
			text = strings.TrimSpace(c.Text)
		}
		for _, m := range c.Media {
			if m.Kind == "image" {
				images++
			}
		}
	}
	media := "[album]"
	if images == 1 {
		media = "[image]"
	} else if images > 1 {
		media = fmt.Sprintf("[album of %d photos]", images)
	}
	first := rows[0]
	return &StoredQuote{From: storedQuoteFrom(first.Role, first.SenderID, msg), Text: capQuote(text), Media: media}, nil
}

// albumRows are the stored user rows of one album in this chat, oldest first.
func albumRows(q db.Q, chatID, albumID string) ([]db.Message, error) {
	rows, err := q.Query("SELECT "+db.MessageCols+" FROM messages WHERE chat_id = ? AND role = 'user' AND json_extract(content, '$.album') = ? ORDER BY created_at ASC, id ASC LIMIT 30", chatID, albumID)
	return db.All(rows, err, db.ScanMessage)
}

// quotedRows are the stored user rows a reply points at: the message itself,
// or every photo of the album it quotes.
func quotedRows(q db.Q, chatID, channelMsgID string) ([]db.Message, error) {
	rows, err := q.Query("SELECT "+db.MessageCols+" FROM messages WHERE chat_id = ? AND role = 'user' AND channel_msg_id = ? ORDER BY created_at DESC LIMIT 1", chatID, channelMsgID)
	found, err := db.All(rows, err, db.ScanMessage)
	if err != nil || len(found) > 0 {
		return found, err
	}
	return albumRows(q, chatID, channelMsgID)
}

func hasImageData(c StoredContent) bool {
	for _, m := range c.Media {
		if m.Kind == "image" && m.Data != nil && *m.Data != "" {
			return true
		}
	}
	return false
}

func isStickerText(text string) bool {
	return strings.HasPrefix(text, "[sticker") || strings.HasPrefix(text, "[animated sticker")
}

// gathered counts what a set of rows holds: photos with bytes (up to the cap),
// photos past the cap, and photos whose bytes are gone (expired or never fetched).
type gathered struct {
	images          []StoredMedia
	overCap         int
	expired, absent int
}

func (g *gathered) add(rows []db.Message, skip map[string]bool, budget *int) {
	for _, r := range rows {
		if skip[r.ID] {
			continue
		}
		skip[r.ID] = true
		c := ParseContent(r.Content)
		own := r.Text
		if own == "" {
			own = c.Text
		}
		for _, m := range c.Media {
			if m.Kind != "image" {
				continue
			}
			switch {
			case m.Data != nil && *m.Data != "":
				if *budget > 0 {
					g.images = append(g.images, m)
					*budget--
				} else {
					g.overCap++
				}
			case isStickerText(own):
				// A sticker's label already says what it was.
			case m.Note != nil && *m.Note == mediaExpired:
				g.expired++
			default:
				g.absent++
			}
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// notes says what came with the photos and, honestly, what didn't.
func (g gathered) notes(what string) []string {
	var out []string
	if n := len(g.images); n > 0 {
		out = append(out, fmt.Sprintf("[%s attached]", plural(n, "the "+what+" photo is", "%d "+what+" photos are")))
	}
	if g.overCap > 0 {
		out = append(out, fmt.Sprintf("[%s not attached: at most %d photos go with one message]", plural(g.overCap, "1 more "+what+" photo was", "%d more "+what+" photos were"), MaxAttachedImages))
	}
	if g.expired > 0 {
		out = append(out, fmt.Sprintf("[%s can't be shown: photos from group talk are kept for 24 hours and this is older]", plural(g.expired, "the "+what+" photo", "%d "+what+" photos")))
	}
	if g.absent > 0 {
		out = append(out, fmt.Sprintf("[%s can't be shown: it was never downloaded]", plural(g.absent, "the "+what+" photo", "%d "+what+" photos")))
	}
	return out
}

// AttachQuotedImages finds, for each batch message, the photos it replies to
// and the rest of the album it came in, in this chat only. A message that
// carries its own photo already got the quoted one from the channel.
func AttachQuotedImages(q db.Q, chatID string, rows []db.Message, batch map[string]bool) (map[string]AttachedImages, error) {
	out := map[string]AttachedImages{}
	budget := MaxAttachedImages
	for _, r := range rows {
		if !batch[r.ID] || r.Role != "user" {
			continue
		}
		c := ParseContent(r.Content)
		// Batch rows show their own photos; nothing is attached twice.
		skip := map[string]bool{}
		for id := range batch {
			skip[id] = true
		}
		var notes []string
		var images []StoredMedia
		if r.ReplyToChannelMsgID != nil && *r.ReplyToChannelMsgID != "" && !hasImageData(c) {
			target, err := quotedRows(q, chatID, *r.ReplyToChannelMsgID)
			if err != nil {
				return nil, err
			}
			var g gathered
			g.add(target, skip, &budget)
			notes = append(notes, g.notes("quoted")...)
			images = append(images, g.images...)
		}
		if c.Album != "" {
			sibs, err := albumRows(q, chatID, c.Album)
			if err != nil {
				return nil, err
			}
			var g gathered
			g.add(sibs, skip, &budget)
			images = append(images, g.images...)
			if n := len(g.images); n > 0 {
				notes = append(notes, fmt.Sprintf("[%s from the same album attached]", plural(n, "1 more photo", "%d more photos")))
				g.images = nil
			}
			notes = append(notes, g.notes("album")...)
		}
		if len(notes) > 0 || len(images) > 0 {
			out[r.ID] = AttachedImages{Note: strings.Join(notes, "\n"), Images: images}
		}
	}
	return out, nil
}

// PruneContextMedia clears the bytes of group talk photos older than
// ContextMediaKeepMs, at most once per ContextMediaPruneMs. The row keeps
// the photo's entry, marked expired, so a reply to it says why it's gone.
func (e *Engine) PruneContextMedia(now int64) {
	e.mu.Lock()
	if e.lastCtxPrune != 0 && now-e.lastCtxPrune < ContextMediaPruneMs {
		e.mu.Unlock()
		return
	}
	e.lastCtxPrune = now
	e.mu.Unlock()
	n, err := PruneContextMedia(e.Ctx.DB, now)
	if err != nil {
		e.Ctx.Log.Error("clearing expired group photos failed", logx.Fields{"error": err})
		return
	}
	if n > 0 {
		e.Ctx.Log.Info("cleared group photos past their 24 hours", logx.Fields{"count": n})
	}
}

// PruneContextMedia is one pass of the prune: the rows it cleared.
func PruneContextMedia(q db.Q, now int64) (int, error) {
	type row struct {
		id      string
		content db.RawJSON
	}
	rs, err := q.Query("SELECT id, content FROM messages WHERE status = 'context' AND created_at < ? AND content LIKE '%\"data\":\"_%' LIMIT ?", now-ContextMediaKeepMs, contextPruneBatch)
	list, err := db.All(rs, err, func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.id, &r.content)
	})
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, r := range list {
		o, err := jsonx.ParseObject(r.content)
		if err != nil {
			continue
		}
		v, _ := o.Get("media")
		media, _ := v.([]any)
		changed := false
		for _, m := range media {
			mo, ok := m.(*jsonx.Object)
			if !ok {
				continue
			}
			if d, _ := mo.Get("data"); d != nil && d != "" {
				mo.Set("data", "")
				mo.Set("note", mediaExpired)
				changed = true
			}
		}
		if !changed {
			continue
		}
		raw, err := db.Raw(o)
		if err != nil {
			return cleared, err
		}
		if _, err := q.Exec("UPDATE messages SET content = ? WHERE id = ?", raw, r.id); err != nil {
			return cleared, err
		}
		cleared++
	}
	return cleared, nil
}

func hasAttachedImages(attached map[string]AttachedImages) bool {
	for _, a := range attached {
		if len(a.Images) > 0 {
			return true
		}
	}
	return false
}

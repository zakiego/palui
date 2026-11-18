package attachments

// send_media: put a photo, file, voice note, video or sticker that already
// exists into the turn's chat. The file is a public URL (downloaded now,
// through the SSRF guard) or a file saved in this chat. Nothing is generated.
//
// It is a chat-scoped write, so it follows PLAN 19.4 like save_attachment:
// guests in a chat set up for them can use it (it only ever sends to the chat
// they are in, and only files from that chat or the public web), owner turns
// ask first once the turn read something untrusted, and `guest_tools: none`
// takes it away.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/outmedia"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// SendTTLMs is how long a media row may wait for its channel (a reply's 24 h).
const SendTTLMs = 24 * 3600_000

const sendDescription = "Send a photo, file, voice note, video or sticker that already exists into this chat: a public URL (an image or file found on the web, or a link another tool returned) or a file saved in this chat (`attachment_id` from list_attachments). Use it when someone asks to see a picture or wants a file, instead of saying you can't send images. It can't make new images or speak text: only send what exists. Pass the direct link to the file itself (it usually ends in .jpg, .png, .pdf, .mp4, .ogg). To send a picture from the web: search_web, then fetch_page on a good result and pass one of its `images` links; a page link with kind image sends that page's own preview image. Pick `kind`: image (JPEG, PNG or WebP), video (MP4), voice_note (an existing Ogg/Opus recording; MP3 or M4A goes out as an audio file), file (any document: PDF, Office, text, zip) or sticker (WebP). A file that can't go as that kind is sent as a document. At most 16 MB (images 10 MB). The file is queued at once and arrives before your reply; don't paste the URL again in your reply. Never tell anyone a file was sent unless this tool returned ok."

var sendInput = zod.Object(
	zod.F("kind", zod.Enum(outmedia.WantKinds...).Describe("How it shows in the chat: image, video, voice_note, file or sticker.")),
	zod.F("url", zod.String().Min(1).Max(2000).Optional().Describe("Public http(s) link to the file itself. Give this or attachment_id.")),
	zod.F("attachment_id", zod.String().Min(1).Max(64).Optional().Describe("A file saved in this chat (att_...). Give this or url.")),
	zod.F("caption", zod.String().Max(outmedia.MaxCaption).Optional().Describe("Short text shown with the file, in the chat's language. Voice notes and stickers show none.")),
	zod.F("filename", zod.String().Max(200).Optional().Describe("File name shown for a file, e.g. \"jadwal-imunisasi.pdf\".")),
)

func sendPreview(raw json.RawMessage) string {
	out, err := zod.ParseJSON(sendInput, raw)
	o, ok := out.(*jsonx.Object)
	if err != nil || !ok {
		return strx.Slice16(string(raw), 300)
	}
	in := kit.Input{O: o}
	from := in.S("url")
	if from == "" {
		from = in.S("attachment_id")
	}
	text := fmt.Sprintf("Kirim %s: %s", in.S("kind"), from)
	if c := strx.Trim(in.S("caption")); c != "" {
		text += "\n" + c
	}
	return strx.Slice16(text, 500)
}

var kindWords = map[string]string{
	db.MediaImage: "an image", db.MediaVideo: "a video", db.MediaVoice: "a voice note", db.MediaAudio: "an audio file",
	db.MediaDocument: "a document", db.MediaSticker: "a sticker",
}

func sendMedia(ctx context.Context, d *db.DB, turn connectors.TurnInfo, in kit.Input, key string, clock func() int64, fetch outmedia.Fetcher) (any, error) {
	if turn.ChatID == "" {
		return kit.Fail("There is no chat to send to in this run."), nil
	}
	rawURL, attID := strx.Trim(in.S("url")), strx.Trim(in.S("attachment_id"))
	if (rawURL == "") == (attID == "") {
		return kit.Fail("Give exactly one of url or attachment_id."), nil
	}
	chat, ok, err := chats.Get(d, turn.ChatID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return kit.Fail("This chat no longer exists."), nil
	}
	var declared, name, source, ref, fromPage string
	var data []byte
	if rawURL != "" {
		if !strings.HasPrefix(strings.ToLower(rawURL), "http://") && !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
			return kit.Fail("url must be an http(s) link."), nil
		}
		f, err := outmedia.Fetch(ctx, rawURL, fetch)
		if err != nil {
			return failMedia(err)
		}
		// A web page asked for as a picture: send the page's own preview image
		// (or its first real picture), one hop, through the same guard.
		if want := in.S("kind"); (want == outmedia.WantImage || want == outmedia.WantSticker) && isPage(f) {
			imgs := webfetch.PageImages(string(f.Bytes), f.URL)
			if len(imgs) == 0 {
				return kit.Fail("That link is a web page with no picture to send. Find the direct link to the image (it usually ends in .jpg or .png), or call fetch_page and pick one from its `images`."), nil
			}
			page := f.URL
			if f, err = outmedia.Fetch(ctx, imgs[0].URL, fetch); err != nil {
				return failMedia(err)
			}
			fromPage = page
		}
		declared, name, data, source, ref = f.ContentType, f.Filename, f.Bytes, "url", f.URL
	} else {
		meta, ok, err := attachments.Get(d, attID)
		if err != nil {
			return nil, err
		}
		if !ok || meta.ChatID != chat.ID {
			return kit.Fail(fmt.Sprintf("No saved attachment %s in this chat. Call list_attachments to get the ids.", attID)), nil
		}
		file, _, err := attachments.Read(d, attID, key)
		if err != nil {
			return nil, err
		}
		declared, data, source, ref = meta.MimeType, file.Bytes, "attachment", meta.ID
		name = attachments.DownloadName(meta.Filename, meta.Title, meta.MimeType)
	}
	if n := strx.Trim(in.S("filename")); n != "" {
		name = n
	}
	p, err := outmedia.Prepare(in.S("kind"), declared, name, data)
	if err != nil {
		return failMedia(err)
	}
	now := clock()
	var row db.Outbox
	err = d.Tx(func(tx db.Q) error {
		media, err := outmedia.Store(tx, outmedia.StoreInput{ChatID: chat.ID, Media: p, Source: source, SourceRef: ref}, key, now)
		if err != nil {
			return err
		}
		exp := now + SendTTLMs
		row, err = outbox.Enqueue(tx, outbox.EnqueueInput{
			ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID,
			Payload: db.OutboxPayload{Text: strx.Trim(in.S("caption")), Media: &media},
			Source:  "media:" + turn.RunID, ExpiresAt: &exp,
		}, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	outbox.Notify()
	out := kit.O("ok", true, "queued", row.ID, "kind", p.Kind, "mimeType", p.MimeType, "filename", p.Filename, "size", attachments.FormatBytes(int64(len(p.Bytes))))
	var notes []string
	if fromPage != "" {
		out.Set("url", ref)
		notes = append(notes, "That link is a web page, so its own preview image was sent. If it doesn't show what was asked, call fetch_page on the page and send a better one from its `images`.")
	}
	if p.Fallback != "" {
		notes = append(notes, fmt.Sprintf("It can't go out as %s, so it is sent as %s.", kindWords[p.Fallback], kindWords[p.Kind]))
	}
	if len(notes) > 0 {
		out.Set("note", strings.Join(notes, " "))
	}
	return out, nil
}

func isPage(f outmedia.Fetched) bool {
	ct := strings.ToLower(f.ContentType)
	if ct == "text/html" || ct == "application/xhtml+xml" {
		return true
	}
	head := strings.ToLower(strings.TrimSpace(string(f.Bytes[:min(len(f.Bytes), 256)])))
	return ct == "" && (strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html"))
}

func failMedia(err error) (any, error) {
	if outmedia.IsError(err) {
		return kit.Fail(err.Error()), nil
	}
	if attachments.IsError(err) {
		return kit.Fail(err.Error()), nil
	}
	return nil, err
}

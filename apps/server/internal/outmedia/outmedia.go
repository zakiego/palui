// Package outmedia is outbound media: a photo, file, voice note, video or
// sticker an agent sends to a chat. Only media that already exists is sent: a
// public URL (fetched through the SSRF guard) or a saved attachment. Nothing
// is generated here.
//
// The bytes are checked by content (never by the URL's say-so), given the
// kind the channel sends them as, and stored encrypted in outbox_media. The
// outbox row only holds a reference, so a retry sends the same bytes and the
// row stays small. Rows nothing can send anymore are swept.
package outmedia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/sticker"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Size caps, the smaller of what WhatsApp and the Telegram Bot API take for
// an upload. A file over a kind's cap is sent as a document when that fits.
const (
	MaxBytes        = 16 * 1024 * 1024
	MaxImageBytes   = 10 * 1024 * 1024
	MaxStickerBytes = 500 * 1024
	// Caption cap: Telegram's 1024 characters.
	MaxCaption = 1024
)

// Requested kinds, as the send_media tool takes them.
const (
	WantImage   = "image"
	WantVideo   = "video"
	WantVoice   = "voice_note"
	WantFile    = "file"
	WantSticker = "sticker"
)

var WantKinds = []string{WantImage, WantVideo, WantVoice, WantFile, WantSticker}

// Error is a refusal whose text goes to the model as is.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func refuse(format string, args ...any) error { return &Error{Msg: fmt.Sprintf(format, args...)} }

// IsError reports whether err is a refusal.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// Prepared is media ready to store: the kind it goes out as, its real type
// and its bytes (converted when the channel needs another format).
type Prepared struct {
	Kind     string
	MimeType string
	Filename string
	Bytes    []byte
	// The kind asked for couldn't be used, so it goes out as Kind instead.
	Fallback string
}

var ooxml = map[string]bool{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
}

var ole = map[string]bool{"application/msword": true, "application/vnd.ms-excel": true, "application/vnd.ms-powerpoint": true}

var byExt = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
	".pdf": "application/pdf", ".txt": "text/plain", ".csv": "text/csv", ".zip": "application/zip",
	".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".mp3":  "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".ogg": "audio/ogg", ".opus": "audio/ogg", ".oga": "audio/ogg",
	".wav": "audio/wav", ".mp4": "video/mp4", ".webm": "video/webm",
}

var extFor = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp", "application/pdf": "pdf",
	"text/plain": "txt", "text/csv": "csv", "application/zip": "zip", "audio/mpeg": "mp3", "audio/mp4": "m4a",
	"audio/aac": "aac", "audio/ogg": "ogg", "audio/wav": "wav", "video/mp4": "mp4", "video/webm": "webm",
	"application/msword": "doc", "application/vnd.ms-excel": "xls", "application/vnd.ms-powerpoint": "ppt",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
}

func extMime(filename string) string {
	return byExt[strings.ToLower(path.Ext(filename))]
}

// isOpus reports whether an Ogg stream carries Opus, which a WhatsApp or
// Telegram voice note needs.
func isOpus(b []byte) bool {
	head := b[:min(len(b), 128)]
	return bytes.HasPrefix(b, []byte("OggS")) && bytes.Contains(head, []byte("OpusHead"))
}

// Sniff works out a file's real type from its bytes. declared (the server's
// content type or the attachment's) and the file name only decide between
// types the bytes can't tell apart, like an Office file and a zip. "" means
// the type isn't one Palui sends.
func Sniff(declared, filename string, b []byte) string {
	declared = attachments.NormalizeMime(declared)
	hint := declared
	if hint == "" || hint == "application/octet-stream" {
		hint = extMime(filename)
	}
	if e := extMime(filename); e != "" && (hint == "application/zip" || hint == "text/plain") {
		hint = e
	}
	sniffed := attachments.NormalizeMime(http.DetectContentType(b))
	switch sniffed {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf", "audio/mpeg", "video/webm":
		return sniffed
	case "audio/wave":
		return "audio/wav"
	case "application/ogg", "audio/ogg":
		return "audio/ogg"
	case "video/mp4":
		if hint == "audio/mp4" || hint == "audio/x-m4a" || hint == "audio/m4a" || hint == "audio/aac" {
			return "audio/mp4"
		}
		return "video/mp4"
	case "application/zip":
		if ooxml[hint] {
			return hint
		}
		return "application/zip"
	case "text/plain":
		if hint == "text/csv" {
			return "text/csv"
		}
		return "text/plain"
	}
	switch {
	case ole[hint] && bytes.HasPrefix(b, []byte{0xd0, 0xcf, 0x11, 0xe0}):
		return hint
	case (hint == "audio/mpeg" || hint == "audio/aac") && len(b) > 1 && b[0] == 0xff && b[1]&0xe0 == 0xe0:
		return hint
	case bytes.HasPrefix(b, []byte("ID3")):
		return "audio/mpeg"
	}
	return ""
}

func describeType(declared string) string {
	switch {
	case declared == "text/html" || declared == "application/xhtml+xml":
		return "a web page, not a file. Use the direct link to the image or file itself (it usually ends in .jpg, .png, .pdf, ...)"
	case declared != "":
		return declared + ", which can't be sent. Allowed: JPEG, PNG, WebP or GIF images, MP4 video, Ogg, MP3, M4A or WAV audio, PDF, plain text, CSV, zip and Office documents"
	}
	return "a file type that can't be sent. Allowed: JPEG, PNG, WebP or GIF images, MP4 video, Ogg, MP3, M4A or WAV audio, PDF, plain text, CSV, zip and Office documents"
}

// Prepare checks a file and picks the kind it goes out as. want is one of
// WantKinds. A file that can't go out as that kind falls back to a document.
func Prepare(want, declared, filename string, b []byte) (Prepared, error) {
	if len(b) == 0 {
		return Prepared{}, refuse("The file is empty.")
	}
	if len(b) > MaxBytes {
		return Prepared{}, refuse("The file is %s, over the %s limit.", attachments.FormatBytes(int64(len(b))), attachments.FormatBytes(MaxBytes))
	}
	mime := Sniff(declared, filename, b)
	if mime == "" {
		return Prepared{}, refuse("That is %s.", describeType(attachments.NormalizeMime(declared)))
	}
	p := Prepared{MimeType: mime, Bytes: b}
	fallback := func(from, to string) { p.Kind, p.Fallback = to, from }
	switch want {
	case WantSticker:
		switch {
		case mime == "image/webp" && len(b) <= MaxStickerBytes:
			p.Kind = db.MediaSticker
		case p.asImage():
			p.Fallback = db.MediaSticker
		default:
			fallback(db.MediaSticker, db.MediaDocument)
		}
	case WantImage:
		if !p.asImage() {
			fallback(db.MediaImage, db.MediaDocument)
		}
	case WantVideo:
		p.Kind = db.MediaVideo
		if mime != "video/mp4" {
			fallback(db.MediaVideo, db.MediaDocument)
		}
	case WantVoice:
		switch {
		case mime == "audio/ogg" && isOpus(b):
			p.Kind = db.MediaVoice
		case mime == "audio/mpeg" || mime == "audio/mp4" || mime == "audio/aac" || mime == "audio/ogg":
			fallback(db.MediaVoice, db.MediaAudio)
		default:
			fallback(db.MediaVoice, db.MediaDocument)
		}
	case WantFile, "":
		p.Kind = db.MediaDocument
	default:
		return Prepared{}, refuse("Unknown kind %q.", want)
	}
	p.Filename = Filename(filename, p.MimeType)
	return p, nil
}

// asImage keeps or converts p for an image message: JPEG and PNG as they
// are, WebP converted to PNG (a WhatsApp image must be one of those two).
// It sets Kind to image and reports whether that worked.
func (p *Prepared) asImage() bool {
	switch p.MimeType {
	case "image/jpeg", "image/png":
	case "image/webp":
		png, err := sticker.ToPNG(p.Bytes)
		if err != nil || len(png) > MaxImageBytes {
			return false
		}
		p.Bytes, p.MimeType = png, "image/png"
	default:
		return false
	}
	if len(p.Bytes) > MaxImageBytes {
		return false
	}
	p.Kind = db.MediaImage
	return true
}

// Filename is a clean file name with the extension of its type.
func Filename(name, mime string) string {
	name = strx.Trim(name)
	ext := extFor[mime]
	if name == "" {
		name = "file"
	}
	clean := attachments.CleanFilename(name)
	if ext != "" && !strings.EqualFold(strings.TrimPrefix(path.Ext(clean), "."), ext) && extMime(clean) != mime {
		clean = strings.TrimSuffix(clean, path.Ext(clean)) + "." + ext
	}
	return clean
}

// Fetcher is safenet.Fetch (injectable in tests).
type Fetcher func(ctx context.Context, url string, opts safenet.Options) (*safenet.Response, error)

// Fetched is a file downloaded from a public URL.
type Fetched struct {
	URL         string
	ContentType string
	Filename    string
	Bytes       []byte
}

// Fetch downloads a public URL through the SSRF guard, up to MaxBytes.
func Fetch(ctx context.Context, rawURL string, fetch Fetcher) (Fetched, error) {
	if fetch == nil {
		fetch = safenet.Fetch
	}
	res, err := fetch(ctx, rawURL, safenet.Options{
		TimeoutMs: 30_000,
		MaxBytes:  MaxBytes,
		Headers:   map[string]string{"accept": "image/*,video/*,audio/*,application/pdf,application/*;q=0.9,*/*;q=0.5"},
	})
	if err != nil {
		var se *safenet.Error
		if errors.As(err, &se) {
			switch se.Code {
			case "blocked_address", "blocked_host", "blocked_port", "blocked_protocol", "blocked_credentials":
				return Fetched{}, refuse("Refused by the SSRF guard: %s", se.Message)
			case "too_large":
				return Fetched{}, refuse("The file is over the %s limit.", attachments.FormatBytes(MaxBytes))
			case "timeout":
				return Fetched{}, refuse("The server took too long to send the file.")
			}
			return Fetched{}, refuse("%s", se.Message)
		}
		return Fetched{}, refuse("Couldn't download the file: %s", err.Error())
	}
	if !res.OK {
		return Fetched{}, refuse("The server answered %d.", res.Status)
	}
	name := ""
	if u, err := url.Parse(res.URL); err == nil {
		if base := path.Base(u.Path); base != "." && base != "/" {
			name, _ = url.PathUnescape(base)
		}
	}
	return Fetched{URL: res.URL, ContentType: res.ContentType, Filename: name, Bytes: res.Bytes}, nil
}

// StoreInput is one file to keep for an outbox row.
type StoreInput struct {
	ChatID    string
	Media     Prepared
	Source    string // "url" or "attachment"
	SourceRef string
}

// Store keeps the bytes encrypted and returns the payload reference.
func Store(q db.Q, in StoreInput, masterKey string, now int64) (db.OutboxMedia, error) {
	data, err := secret.EncryptBytes(in.Media.Bytes, masterKey)
	if err != nil {
		return db.OutboxMedia{}, err
	}
	sum := sha256.Sum256(in.Media.Bytes)
	var filename, ref *string
	if in.Media.Filename != "" {
		filename = &in.Media.Filename
	}
	if in.SourceRef != "" {
		r := strx.Slice16(in.SourceRef, 2000)
		ref = &r
	}
	row, err := db.ScanMediaBlob(q.WQueryRow("INSERT INTO outbox_media (id, chat_id, kind, mime_type, filename, size, sha256, source, source_ref, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.MediaBlobCols,
		secret.NewID("med"), in.ChatID, in.Media.Kind, in.Media.MimeType, filename, len(in.Media.Bytes), hex.EncodeToString(sum[:]), in.Source, ref, data, now))
	if err != nil {
		return db.OutboxMedia{}, err
	}
	return db.OutboxMedia{ID: row.ID, Kind: row.Kind, MimeType: row.MimeType, Filename: in.Media.Filename, Size: row.Size}, nil
}

// ErrGone is a media row that no longer exists (swept or never written).
var ErrGone = errors.New("the media file is gone")

// Load decrypts a stored file.
func Load(q db.Q, id, masterKey string) ([]byte, error) {
	var data []byte
	err := q.QueryRow("SELECT data FROM outbox_media WHERE id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, err
	}
	return secret.DecryptBytes(data, masterKey)
}

// SweepGraceMs keeps a fresh row even before an outbox row points at it.
const SweepGraceMs = 3600_000

// Sweep deletes rows no pending or sending outbox row points at anymore.
func Sweep(q db.Q, now int64) (int64, error) {
	return db.Changes(q.Exec(`DELETE FROM outbox_media WHERE created_at < ? AND NOT EXISTS (
		SELECT 1 FROM outbox WHERE outbox.status IN ('pending', 'sending') AND json_extract(outbox.payload, '$.media.id') = outbox_media.id)`, now-SweepGraceMs))
}

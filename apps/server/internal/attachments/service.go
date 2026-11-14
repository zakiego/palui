// Package attachments ports packages/core/src/attachments: saved attachments
// (files from a chat the agent decided to keep), file references in tool
// arguments, and temporary share links.
//
// Only files that came with this chat's own messages can be saved, never a URL.
// The bytes are checked against an allowlist of types and a size cap, then
// stored encrypted in SQLite.
package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const MaxBytes = 10 * 1024 * 1024

// AllowedMime is what may be saved. Anything else is refused.
var AllowedMime = map[string]string{
	"image/jpeg":         "image",
	"image/png":          "image",
	"image/webp":         "image",
	"image/gif":          "image",
	"image/heic":         "image",
	"image/heif":         "image",
	"application/pdf":    "document",
	"text/plain":         "document",
	"text/csv":           "document",
	"application/msword": "document",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "document",
	"application/vnd.ms-excel": "document",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "document",
	"application/vnd.ms-powerpoint":                                             "document",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "document",
}

var (
	ooxml = []byte{0x50, 0x4b, 0x03, 0x04}
	ole   = []byte{0xd0, 0xcf, 0x11, 0xe0}
)

// Magic bytes for the types that have them, so a renamed file can't pass.
var magic = map[string][][]byte{
	"image/jpeg":                    {{0xff, 0xd8, 0xff}},
	"image/png":                     {{0x89, 0x50, 0x4e, 0x47}},
	"image/gif":                     {{0x47, 0x49, 0x46, 0x38}},
	"image/webp":                    {{0x52, 0x49, 0x46, 0x46}},
	"application/pdf":               {{0x25, 0x50, 0x44, 0x46}},
	"application/msword":            {ole},
	"application/vnd.ms-excel":      {ole},
	"application/vnd.ms-powerpoint": {ole},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {ooxml},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {ooxml},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {ooxml},
}

// Error is a refusal whose text goes to the model or the owner as is.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func attErr(format string, args ...any) error { return &Error{Msg: fmt.Sprintf(format, args...)} }

// IsError reports whether err is an attachment refusal.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// Candidate is a file in the current turn's messages that could be saved.
type Candidate struct {
	// 1-based, in message order: what the model passes as `nomor`.
	Index     int     `json:"index"`
	MessageID string  `json:"messageId"`
	Kind      string  `json:"kind"`
	MimeType  string  `json:"mimeType"`
	Filename  *string `json:"filename"`
	Size      int64   `json:"size"`
	Data      string  `json:"data"`
}

// RecentCandidate is a file from an earlier message, with when it was sent and
// the text that came with it.
type RecentCandidate struct {
	Candidate
	SentAt  int64  `json:"sentAt"`
	Caption string `json:"caption"`
}

const (
	RecentFilesWindowMs = 24 * 3600_000
	RecentFilesMax      = 10
	captionMax          = 80
)

func NormalizeMime(mime string) string {
	return strings.ToLower(strx.Trim(strings.SplitN(mime, ";", 2)[0]))
}

// CheckFile returns the kind ("image" or "document") or a refusal.
func CheckFile(mimeType string, b []byte) (string, error) {
	mime := NormalizeMime(mimeType)
	kind, ok := AllowedMime[mime]
	if !ok {
		return "", attErr("File type %s can't be saved. Allowed: images (JPEG, PNG, WebP, GIF, HEIC), PDF, plain text, CSV and Office documents.", mime)
	}
	if len(b) == 0 {
		return "", attErr("The file is empty.")
	}
	if len(b) > MaxBytes {
		return "", attErr("The file is %s, over the %s limit.", FormatBytes(int64(len(b))), FormatBytes(MaxBytes))
	}
	if sigs, ok := magic[mime]; ok && !slices.ContainsFunc(sigs, func(m []byte) bool { return bytes.HasPrefix(b, m) }) {
		return "", attErr("The file says it is %s but its content doesn't match.", mime)
	}
	return kind, nil
}

// toFixed is Number.prototype.toFixed for the small non-negative values
// FormatBytes prints: a tie rounds up (1.25 -> "1.3"), where Go's 'f' would
// round to even.
func toFixed(f float64, digits int) string {
	p := math.Pow(10, float64(digits))
	return strconv.FormatFloat(math.Floor(f*p+0.5)/p, 'f', digits, 64)
}

func FormatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		d := 0
		if n < 10*1024 {
			d = 1
		}
		return toFixed(float64(n)/1024, d) + " KB"
	}
	return toFixed(float64(n)/(1024*1024), 1) + " MB"
}

// storedMedia is one entry of StoredContent.media (chat/context.ts).
type storedMedia struct {
	Kind     string  `json:"kind"`
	MimeType string  `json:"mimeType"`
	Filename *string `json:"filename"`
	Data     string  `json:"data"`
}

type storedContent struct {
	Text  string        `json:"text"`
	Media []storedMedia `json:"media"`
}

func readContent(raw db.RawJSON) storedContent {
	var c storedContent
	_ = json.Unmarshal(raw, &c)
	return c
}

func candidate(index int, messageID string, m storedMedia) Candidate {
	var filename *string
	if m.Kind == "document" {
		filename = m.Filename
	}
	return Candidate{
		Index: index, MessageID: messageID, Kind: m.Kind, MimeType: NormalizeMime(m.MimeType),
		Filename: filename, Size: int64(strx.Len16(m.Data) * 3 / 4), Data: m.Data,
	}
}

// TurnCandidates are images and documents (with their bytes still there) from
// the user messages a run answers.
func TurnCandidates(q db.Q, chatID, runID string) ([]Candidate, error) {
	rows, err := q.Query("SELECT id, content FROM messages WHERE chat_id = ? AND run_id = ? AND role = 'user' ORDER BY created_at ASC, id ASC", chatID, runID)
	type row struct {
		id      string
		content db.RawJSON
	}
	all, err := db.All(rows, err, func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.id, &r.content)
	})
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, r := range all {
		for _, m := range readContent(r.content).Media {
			if m.Kind == "audio" || m.Data == "" {
				continue
			}
			out = append(out, candidate(len(out)+1, r.id, m))
		}
	}
	return out, nil
}

// RecentCandidates are files with their bytes from this chat's user messages of
// the last RecentFilesWindowMs, newest first, at most RecentFilesMax.
func RecentCandidates(q db.Q, chatID string, now int64) ([]RecentCandidate, error) {
	rows, err := q.Query("SELECT id, content, created_at FROM messages WHERE chat_id = ? AND role = 'user' AND created_at >= ? ORDER BY created_at DESC, id DESC LIMIT 200", chatID, now-RecentFilesWindowMs)
	type row struct {
		id      string
		content db.RawJSON
		at      int64
	}
	all, err := db.All(rows, err, func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.id, &r.content, &r.at)
	})
	if err != nil {
		return nil, err
	}
	out := []RecentCandidate{}
	for _, r := range all {
		c := readContent(r.content)
		caption := strx.Slice16(strx.Trim(strx.CollapseSpace(c.Text)), captionMax)
		for _, m := range c.Media {
			if m.Kind == "audio" || m.Data == "" {
				continue
			}
			if len(out) >= RecentFilesMax {
				return out, nil
			}
			out = append(out, RecentCandidate{Candidate: candidate(len(out)+1, r.id, m), SentAt: r.at, Caption: caption})
		}
	}
	return out, nil
}

type SaveInput struct {
	ChatID      string
	RunID       *string
	MessageID   *string
	Title       string
	Description string
	MimeType    string
	Filename    string
	Bytes       []byte
}

// Saved is the stored row, Deduped when the same bytes were already saved in this chat.
type Saved struct {
	Row     db.AttachmentMeta `json:"row"`
	Deduped bool              `json:"deduped"`
}

func Save(q db.Q, in SaveInput, masterKey string, now int64) (Saved, error) {
	kind, err := CheckFile(in.MimeType, in.Bytes)
	if err != nil {
		return Saved{}, err
	}
	sum := sha256.Sum256(in.Bytes)
	hash := hex.EncodeToString(sum[:])
	existing, ok, err := db.One(q.QueryRow("SELECT "+db.AttachmentMetaCols+" FROM saved_attachments WHERE chat_id = ? AND sha256 = ?", in.ChatID, hash), db.ScanAttachmentMeta)
	if err != nil {
		return Saved{}, err
	}
	if ok {
		return Saved{Row: existing, Deduped: true}, nil
	}
	data, err := secret.EncryptBytes(in.Bytes, masterKey)
	if err != nil {
		return Saved{}, err
	}
	var filename *string
	if in.Filename != "" {
		f := CleanFilename(in.Filename)
		filename = &f
	}
	row, err := db.ScanAttachmentMeta(q.WQueryRow("INSERT INTO saved_attachments (id, chat_id, message_id, run_id, title, description, kind, mime_type, filename, size, sha256, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.AttachmentMetaCols,
		secret.NewID("att"), in.ChatID, in.MessageID, in.RunID,
		strx.Slice16(strx.Trim(in.Title), 200), strx.Slice16(strx.Trim(in.Description), 1000),
		kind, NormalizeMime(in.MimeType), filename, len(in.Bytes), hash, data, now))
	if err != nil {
		return Saved{}, err
	}
	return Saved{Row: row}, nil
}

type ListOptions struct {
	ChatID string
	Query  string
	// Default 50, at most 500.
	Limit int
}

// List never selects `data`. The query uses instr, so % and _ match literally.
func List(q db.Q, opts ListOptions) ([]db.AttachmentMeta, error) {
	var where []string
	var args []any
	if opts.ChatID != "" {
		where = append(where, "chat_id = ?")
		args = append(args, opts.ChatID)
	}
	if text := strx.Trim(opts.Query); text != "" {
		has := func(col string) string { return "instr(lower(coalesce(" + col + ", '')), lower(?)) > 0" }
		where = append(where, "("+has("title")+" or "+has("description")+" or "+has("filename")+")")
		args = append(args, text, text, text)
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 50
	}
	limit = min(limit, 500)
	query := "SELECT " + db.AttachmentMetaCols + " FROM saved_attachments"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	rows, err := q.Query(query+" ORDER BY created_at DESC, id DESC LIMIT ?", args...)
	return db.All(rows, err, db.ScanAttachmentMeta)
}

func Count(q db.Q) (int64, error) {
	var n int64
	err := q.QueryRow("SELECT count(*) FROM saved_attachments").Scan(&n)
	return n, err
}

func Get(q db.Q, id string) (db.AttachmentMeta, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.AttachmentMetaCols+" FROM saved_attachments WHERE id = ?", id), db.ScanAttachmentMeta)
}

// File is a saved attachment with its decrypted bytes.
type File struct {
	Meta  db.AttachmentMeta
	Bytes []byte
}

// Read decrypts a saved attachment. ok is false when there is no such row.
func Read(q db.Q, id, masterKey string) (File, bool, error) {
	type row struct {
		meta db.AttachmentMeta
		data []byte
	}
	r, ok, err := db.One(q.QueryRow("SELECT "+db.AttachmentMetaCols+", data FROM saved_attachments WHERE id = ?", id), func(s db.Scanner) (row, error) {
		var r row
		a := &r.meta
		err := s.Scan(&a.ID, &a.ChatID, &a.MessageID, &a.RunID, &a.Title, &a.Description, &a.Kind, &a.MimeType, &a.Filename, &a.Size, &a.Sha256, &a.CreatedAt, &r.data)
		return r, err
	})
	if err != nil || !ok {
		return File{}, ok, err
	}
	plain, err := secret.DecryptBytes(r.data, masterKey)
	if err != nil {
		return File{}, true, err
	}
	return File{Meta: r.meta, Bytes: plain}, true, nil
}

// Delete also kills every link to the file.
func Delete(q db.Q, id string) (bool, error) {
	deleted := false
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("DELETE FROM attachment_shares WHERE attachment_id = ?", id); err != nil {
			return err
		}
		n, err := db.Changes(tx.Exec("DELETE FROM saved_attachments WHERE id = ?", id))
		deleted = n > 0
		return err
	})
	return deleted, err
}

// PreviewKind is what a browser can show inline: "image", "pdf", "text" or "".
func PreviewKind(mimeType string) string {
	switch mimeType {
	case "application/pdf":
		return "pdf"
	case "text/plain", "text/csv":
		return "text"
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return "image"
	}
	return ""
}

var unsafeFilenameChars = regexp.MustCompile(`[\x00-\x1f\x7f"<>:|?*]`)

// CleanFilename is safe for a Content-Disposition header and a file system:
// no paths, quotes or control characters.
func CleanFilename(name string) string {
	parts := regexp.MustCompile(`[\\/]`).Split(name, -1)
	base := parts[len(parts)-1]
	clean := strx.Slice16(strx.Trim(unsafeFilenameChars.ReplaceAllString(base, "")), 200)
	if clean == "" {
		return "file"
	}
	return clean
}

var ext = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/webp":      "webp",
	"image/gif":       "gif",
	"image/heic":      "heic",
	"image/heif":      "heif",
	"application/pdf": "pdf",
	"text/plain":      "txt",
	"text/csv":        "csv",
}

// DownloadName is the original file name, or the title plus an extension.
func DownloadName(filename *string, title, mimeType string) string {
	if filename != nil && *filename != "" {
		return CleanFilename(*filename)
	}
	if e, ok := ext[mimeType]; ok {
		return CleanFilename(title + "." + e)
	}
	return CleanFilename(title)
}

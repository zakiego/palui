package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	// MaxResultFiles is how many files one tool result may save.
	MaxResultFiles = 10
	// MaxResultFileBytes is how many bytes one tool result may save in all.
	// Each file is also capped by attachments.MaxBytes.
	MaxResultFileBytes = 25 * 1024 * 1024
	// MaxResourceText is how much of an embedded text resource the model
	// still reads inline (UTF-16 units). The whole file is in the attachment.
	MaxResourceText = 4000
)

// ResultFiles says where the files in a tool result are saved.
type ResultFiles struct {
	// The turn's chat. Empty saves nothing and only says so.
	ChatID string
	RunID  string
	// The MCP tool's name, for the attachment's description.
	Tool      string
	MasterKey string
}

const resultFilesHint = "Saved files belong to this chat. To send one here, call send_media with its attachment_id (kind image for a picture, file for anything else)."

var mimeByExt = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp", "gif": "image/gif",
	"heic": "image/heic", "heif": "image/heif", "pdf": "application/pdf", "txt": "text/plain", "csv": "text/csv",
	"doc": "application/msword", "docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xls": "application/vnd.ms-excel", "xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"ppt": "application/vnd.ms-powerpoint", "pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

var extByMime = func() map[string]string {
	out := map[string]string{"image/jpeg": "jpg"}
	for e, m := range mimeByExt {
		if _, ok := out[m]; !ok {
			out[m] = e
		}
	}
	return out
}()

// WithResultFiles saves the files in an MCP tool result as attachments of the
// turn's chat, so the agent can send them with send_media. It reads only what
// the MCP spec defines: image and audio blocks (base64 `data`, `mimeType`) and
// embedded resources (`resource` with `uri`, `mimeType`, and `text` or base64
// `blob`). Image blocks stay for the model, followed by a line with the
// attachment id; resource and audio blocks become that line, so their bytes
// never reach the model as text.
//
// It saves in every audience, like save_attachment: the file lands in the chat
// the turn runs in, and send_media only sends a file back into its own chat.
// That holds for an external server too; its bytes are no more trusted than
// the rest of its result, which already taints the turn.
func WithResultFiles(t connectors.Tool, d *db.DB, turn *connectors.TurnInfo, tool string, key func() string, clock func() int64) connectors.Tool {
	exec := t.Execute
	if exec == nil {
		return t
	}
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	t.Execute = func(ctx context.Context, input json.RawMessage, call connectors.ToolCallInfo) (any, error) {
		out, err := exec(ctx, input, call)
		if err != nil || out == nil {
			return out, err
		}
		rf := ResultFiles{Tool: tool}
		if turn != nil {
			rf.ChatID, rf.RunID = turn.ChatID, turn.RunID
		}
		if rf.ChatID != "" {
			rf.MasterKey = key()
		}
		return SaveResultFiles(d, out, rf, clock())
	}
	return t
}

// resultFile is one file found in a result, before it is saved.
type resultFile struct {
	name, mime string
	data       []byte
	// Set when the file is refused before saving.
	skip string
	// Encoded size, for the line when the bytes were never decoded.
	size int64
}

// SaveResultFiles is WithResultFiles on one result. A result that isn't an
// MCP content list comes back as is.
func SaveResultFiles(q db.Q, out any, rf ResultFiles, now int64) (any, error) {
	o, ok := normalize(out).(*jsonx.Object)
	if !ok {
		return out, nil
	}
	raw, _ := o.Get("content")
	content, ok := raw.([]any)
	if !ok || !hasFiles(content) {
		return out, nil
	}
	o = o.Clone()
	next := make([]any, 0, len(content)+2)
	count, total, saved := 0, int64(0), false
	for _, item := range content {
		p, _ := item.(*jsonx.Object)
		f, keep, extra := fileOf(p, count+1)
		if f == nil {
			next = append(next, item)
			continue
		}
		count++
		if keep {
			next = append(next, item)
		}
		if f.skip == "" {
			switch {
			case count > MaxResultFiles:
				f.skip = fmt.Sprintf("over the limit of %d files per tool result", MaxResultFiles)
			case total+f.size > MaxResultFileBytes:
				f.skip = fmt.Sprintf("over the limit of %s of files per tool result", attachments.FormatBytes(MaxResultFileBytes))
			case rf.ChatID == "":
				f.skip = "this run has no chat to keep it in"
			}
		}
		line := ""
		if f.skip == "" {
			s, err := attachments.Save(q, attachments.SaveInput{
				ChatID: rf.ChatID, RunID: &rf.RunID, Title: f.name, Filename: f.name, MimeType: f.mime, Bytes: f.data,
				Description: fmt.Sprintf("Returned by the %s tool.", rf.Tool),
			}, rf.MasterKey, now)
			switch {
			case attachments.IsError(err):
				f.skip = err.Error()
			case err != nil:
				return nil, err
			default:
				total += f.size
				saved = true
				line = fmt.Sprintf("Saved file %s: %s (%s, %s).", s.Row.ID, f.name, s.Row.MimeType, attachments.FormatBytes(s.Row.Size))
			}
		}
		if f.skip != "" {
			line = fmt.Sprintf("File not saved: %s (%s, %s): %s", f.name, f.mime, attachments.FormatBytes(f.size), strings.TrimSuffix(f.skip, "."))
		}
		next = append(next, obj("type", "text", "text", line))
		if extra != "" {
			next = append(next, obj("type", "text", "text", extra))
		}
	}
	if saved {
		next = append(next, obj("type", "text", "text", resultFilesHint))
	}
	o.Set("content", next)
	return o, nil
}

func hasFiles(content []any) bool {
	for _, item := range content {
		p, _ := item.(*jsonx.Object)
		switch typ, _ := get(p, "type").(string); typ {
		case "image", "audio", "resource":
			return true
		}
	}
	return false
}

// fileOf reads the file in one content block: nil when it holds none. keep
// says the block stays for the model (an image); extra is text the model
// still reads (the start of a text resource).
func fileOf(p *jsonx.Object, n int) (f *resultFile, keep bool, extra string) {
	typ, _ := get(p, "type").(string)
	switch typ {
	case "image", "audio":
		data, ok := get(p, "data").(string)
		if !ok {
			return nil, false, ""
		}
		mime, _ := get(p, "mimeType").(string)
		f = decodeFile(data, attachments.NormalizeMime(mime), "")
		f.name = defaultName(typ, n, f.mime)
		return f, typ == "image", ""
	case "resource":
		r, _ := get(p, "resource").(*jsonx.Object)
		if r == nil {
			return nil, false, ""
		}
		uri, _ := get(r, "uri").(string)
		mime, _ := get(r, "mimeType").(string)
		mime = attachments.NormalizeMime(mime)
		name := nameFromURI(uri)
		if mime == "" || mime == "application/octet-stream" {
			if m, ok := mimeByExt[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]; ok {
				mime = m
			}
		}
		if blob, ok := get(r, "blob").(string); ok {
			f = decodeFile(blob, mime, name)
		} else if text, ok := get(r, "text").(string); ok {
			if mime == "" {
				mime = "text/plain"
			}
			f = &resultFile{mime: mime, data: []byte(text), size: int64(len(text))}
			extra = inlineText(text)
		} else {
			return nil, false, ""
		}
		f.name = name
		if f.name == "" {
			f.name = defaultName(typ, n, f.mime)
		}
		return f, false, extra
	}
	return nil, false, ""
}

// decodeFile decodes base64 bytes, refusing ones over the per-file cap
// before decoding them.
func decodeFile(b64, mime, name string) *resultFile {
	f := &resultFile{mime: mime, size: int64(base64.StdEncoding.DecodedLen(len(b64)))}
	if f.size > attachments.MaxBytes {
		f.skip = fmt.Sprintf("it is over the %s limit", attachments.FormatBytes(attachments.MaxBytes))
		if f.mime == "" {
			f.mime = "application/octet-stream"
		}
		return f
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(b64, "="))
	}
	if err != nil {
		f.skip = "its data isn't valid base64"
		if f.mime == "" {
			f.mime = "application/octet-stream"
		}
		return f
	}
	f.data, f.size = data, int64(len(data))
	if f.mime == "" || f.mime == "application/octet-stream" {
		f.mime = attachments.NormalizeMime(http.DetectContentType(data))
	}
	return f
}

func inlineText(text string) string {
	if strx.Len16(text) <= MaxResourceText {
		return text
	}
	return strx.Slice16(text, MaxResourceText) + fmt.Sprintf("\n[cut at %d characters; the whole file is in the attachment]", MaxResourceText)
}

// nameFromURI is the URI's last path segment, or "" when it has none.
func nameFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	p := u.Path
	if p == "" {
		p = u.Opaque
	}
	base := path.Base(strings.TrimRight(p, "/"))
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return attachments.CleanFilename(base)
}

func defaultName(typ string, n int, mime string) string {
	prefix := "file"
	if typ == "image" || strings.HasPrefix(mime, "image/") {
		prefix = "image"
	} else if typ == "audio" {
		prefix = "audio"
	}
	name := fmt.Sprintf("%s-%d", prefix, n)
	if e, ok := extByMime[mime]; ok {
		name += "." + e
	} else if _, sub, ok := strings.Cut(mime, "/"); ok && sub != "" && !strings.ContainsAny(sub, "+.-") {
		name += "." + sub
	}
	return name
}

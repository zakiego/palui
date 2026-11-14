package attachments

import (
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// File references in tool arguments. The model can't write a file's bytes into
// a tool call, so the attachment tools hand it a short reference instead. When
// a tool argument is exactly that reference, Palui swaps in the file as a
// base64 data URL just before the call leaves for the server. Logs and
// approvals keep the short reference, never the bytes.

const refPrefix = "palui:attachment/"

var refRe = regexp.MustCompile(`^palui:attachment/(att_[A-Za-z0-9]+)$`)

// MaxRefBytes is the most file bytes one tool call may carry, all refs together.
const MaxRefBytes = 15 * 1024 * 1024

func Ref(id string) string { return refPrefix + id }

// RefError is a refused reference.
type RefError struct{ Msg string }

func (e *RefError) Error() string { return e.Msg }

// mapStrings copies a JSON-like value with fn applied to every string.
func mapStrings(v any, fn func(string) string) any {
	switch t := v.(type) {
	case string:
		return fn(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = mapStrings(x, fn)
		}
		return out
	case *jsonx.Object:
		if t == nil {
			return t
		}
		out := jsonx.NewMap[any]()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			out.Set(k, mapStrings(x, fn))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = mapStrings(x, fn)
		}
		return out
	}
	return v
}

// FindRefs lists every referenced attachment id anywhere in the value, once each.
func FindRefs(v any) []string {
	seen := map[string]bool{}
	var ids []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			if m := refRe.FindStringSubmatch(t); m != nil && !seen[m[1]] {
				seen[m[1]] = true
				ids = append(ids, m[1])
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		case *jsonx.Object:
			for _, k := range t.Keys() {
				x, _ := t.Get(k)
				walk(x)
			}
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	return ids
}

// FileURL pairs a ref with the data URL it was swapped for.
type FileURL struct {
	Ref string `json:"ref"`
	URL string `json:"url"`
}

type ResolveOptions struct {
	ChatID    string
	MasterKey string
	// MaxRefBytes when 0.
	MaxBytes int64
}

func notInChat(id string) error {
	return &RefError{Msg: "No saved attachment " + id + " in this chat. Call list_attachments to get the ids."}
}

// ResolveFileRefs returns a copy of value with every reference replaced by its
// file's data URL. Only files saved in ChatID resolve, and all of them together
// must fit in MaxBytes; anything else is refused. A value without refs comes
// back as is.
func ResolveFileRefs(q db.Q, value any, opts ResolveOptions) (any, []FileURL, error) {
	ids := FindRefs(value)
	if len(ids) == 0 {
		return value, nil, nil
	}
	max := opts.MaxBytes
	if max == 0 {
		max = MaxRefBytes
	}
	var total int64
	for _, id := range ids {
		meta, ok, err := Get(q, id)
		if err != nil {
			return nil, nil, err
		}
		if !ok || meta.ChatID != opts.ChatID {
			return nil, nil, notInChat(id)
		}
		total += meta.Size
	}
	if total > max {
		return nil, nil, &RefError{Msg: "These files add up to " + FormatBytes(total) + ", over the " + FormatBytes(max) + " one call can carry. Send fewer at a time."}
	}
	urls := map[string]string{}
	var files []FileURL
	for _, id := range ids {
		f, ok, err := Read(q, id, opts.MasterKey)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, notInChat(id)
		}
		url := "data:" + f.Meta.MimeType + ";base64," + base64.StdEncoding.EncodeToString(f.Bytes)
		urls[id] = url
		files = append(files, FileURL{Ref: Ref(id), URL: url})
	}
	out := mapStrings(value, func(s string) string {
		if m := refRe.FindStringSubmatch(s); m != nil {
			if u, ok := urls[m[1]]; ok {
				return u
			}
		}
		return s
	})
	return out, files, nil
}

// ResolveRefs is ResolveFileRefs without the file list.
func ResolveRefs(q db.Q, value any, opts ResolveOptions) (any, error) {
	v, _, err := ResolveFileRefs(q, value, opts)
	return v, err
}

// ScrubFileURLs puts refs back where a result repeats a file's data URL, so a
// server that echoes its input doesn't pour base64 into the model's context.
func ScrubFileURLs(value any, files []FileURL) any {
	if len(files) == 0 {
		return value
	}
	return mapStrings(value, func(s string) string {
		out := s
		for _, f := range files {
			if strings.Contains(out, f.URL) {
				out = strings.ReplaceAll(out, f.URL, f.Ref)
			}
		}
		return out
	})
}

// LabelFileRefs shows each ref as the file's title and size, for approval prompts.
func LabelFileRefs(q db.Q, value any) (any, error) {
	if len(FindRefs(value)) == 0 {
		return value, nil
	}
	var firstErr error
	out := mapStrings(value, func(s string) string {
		m := refRe.FindStringSubmatch(s)
		if m == nil {
			return s
		}
		a, ok, err := Get(q, m[1])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if !ok {
			return s
		}
		return "[file: " + a.Title + ", " + FormatBytes(a.Size) + "]"
	})
	return out, firstErr
}

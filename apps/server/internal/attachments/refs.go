package attachments

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// File references in tool arguments. The model can't write a file's bytes into
// a tool call, so the attachment tools hand it a short reference instead. When
// a tool argument is exactly that reference, at any depth, Palui swaps in the
// file just before the call leaves for the server, as raw base64 or a data URL
// depending on the field's schema (refschema.go). Logs and approvals keep the
// short reference, never the bytes.

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

// FileURL pairs a ref with what it was swapped for: the data URL, and the raw
// base64 that sits inside it.
type FileURL struct {
	Ref    string `json:"ref"`
	URL    string `json:"url"`
	Base64 string `json:"-"`
}

type ResolveOptions struct {
	ChatID    string
	MasterKey string
	// MaxRefBytes when 0.
	MaxBytes int64
	// The tool's input schema. It picks raw base64 or a data URL for each
	// field; without it every ref becomes raw base64 unless the field's name
	// says URL.
	Schema json.RawMessage
}

func notInChat(id string) error {
	return &RefError{Msg: "No saved attachment " + id + " in this chat. Call list_attachments to get the ids."}
}

// HasRefText reports whether any string in the value mentions a ref, exact or
// not.
func HasRefText(v any) bool {
	found := false
	mapStrings(v, func(s string) string {
		if !found && strings.Contains(s, refPrefix) {
			found = true
		}
		return s
	})
	return found
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// checkLoose refuses a ref that isn't the whole string: one wrapped in other
// text, or one with a bad id. Palui would otherwise send the ref's text, which
// no app can read.
func checkLoose(v any, path string) error {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, refPrefix) && !refRe.MatchString(t) {
			at := path
			if at == "" {
				at = "the arguments"
			}
			return &RefError{Msg: "A fileRef in " + at + " isn't on its own. Pass it as the whole value of the file field, exactly as list_attachments gives it (palui:attachment/att_...), with no other text around it."}
		}
	case []any:
		for i, x := range t {
			if err := checkLoose(x, path+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
	case *jsonx.Object:
		if t == nil {
			return nil
		}
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			if err := checkLoose(x, joinPath(path, k)); err != nil {
				return err
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := checkLoose(t[k], joinPath(path, k)); err != nil {
				return err
			}
		}
	}
	return nil
}

// fill swaps every ref in v for its file, in the form the field's schema asks for.
type fill struct {
	s     *schema
	files map[string]*FileURL
}

func (f *fill) walk(v any, nodes []node, key, path string) (any, error) {
	switch t := v.(type) {
	case string:
		m := refRe.FindStringSubmatch(t)
		if m == nil {
			return t, nil
		}
		file := f.files[m[1]]
		form, maxLen := fieldForm(nodes, key)
		out := file.Base64
		if form == FormDataURL {
			out = file.URL
		}
		if maxLen >= 0 && len(out) > maxLen {
			return nil, &RefError{Msg: "The file for " + path + " is " + strconv.Itoa(len(out)) + " characters once encoded, over the " + strconv.Itoa(maxLen) + " that field takes. Use a smaller file."}
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			y, err := f.walk(x, f.s.item(nodes, i), key, path+"["+strconv.Itoa(i)+"]")
			if err != nil {
				return nil, err
			}
			out[i] = y
		}
		return out, nil
	case *jsonx.Object:
		if t == nil {
			return t, nil
		}
		out := jsonx.NewMap[any]()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			y, err := f.walk(x, f.s.prop(nodes, k), k, joinPath(path, k))
			if err != nil {
				return nil, err
			}
			out.Set(k, y)
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			y, err := f.walk(x, f.s.prop(nodes, k), k, joinPath(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = y
		}
		return out, nil
	}
	return v, nil
}

// ResolveFileRefs returns a copy of value with every reference replaced by its
// file: raw base64 or a data URL, whichever opts.Schema asks for at that
// field. Only files saved in ChatID resolve, and all of them together must fit
// in MaxBytes. A ref with other text around it, a bad id, a file from another
// chat or one too big is refused with a RefError and nothing is sent. A value
// without refs comes back as is.
func ResolveFileRefs(q db.Q, value any, opts ResolveOptions) (any, []FileURL, error) {
	if err := checkLoose(value, ""); err != nil {
		return nil, nil, err
	}
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
		what := "These files add up to "
		if len(ids) == 1 {
			what = "This file is "
		}
		return nil, nil, &RefError{Msg: what + FormatBytes(total) + ", over the " + FormatBytes(max) + " one call can carry. Send fewer or smaller files at a time."}
	}
	files := map[string]*FileURL{}
	for _, id := range ids {
		f, ok, err := Read(q, id, opts.MasterKey)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, notInChat(id)
		}
		b64 := base64.StdEncoding.EncodeToString(f.Bytes)
		files[id] = &FileURL{Ref: Ref(id), URL: "data:" + f.Meta.MimeType + ";base64," + b64, Base64: b64}
	}
	s := parseSchema(opts.Schema)
	fl := &fill{s: s, files: files}
	out, err := fl.walk(value, s.rootNodes(), "", "")
	if err != nil {
		return nil, nil, err
	}
	list := make([]FileURL, 0, len(ids))
	for _, id := range ids {
		list = append(list, *files[id])
	}
	return out, list, nil
}

// ResolveRefs is ResolveFileRefs without the file list.
func ResolveRefs(q db.Q, value any, opts ResolveOptions) (any, error) {
	v, _, err := ResolveFileRefs(q, value, opts)
	return v, err
}

const minScrubBase64 = 64

// ScrubFileURLs puts refs back where a result repeats a file's data URL or base64, so a
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
			// Raw base64 too, unless it's so short it could match by chance.
			if len(f.Base64) >= minScrubBase64 && strings.Contains(out, f.Base64) {
				out = strings.ReplaceAll(out, f.Base64, f.Ref)
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

package models

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Standardize is the AI SDK's convertToLanguageModelPrompt for what Palui
// sends: string content becomes a text part, image parts become file parts
// with normalized data ({type: "data", data: base64} or {type: "url", url}),
// empty user text parts and empty assistant text parts without
// providerOptions are dropped, approval requests and responses are dropped,
// consecutive tool messages merge, and a tool call without a result is an
// error. The result is the provider-facing prompt.
func Standardize(msgs []Message) ([]Message, error) {
	var out []Message
	for _, m := range msgs {
		c, err := standardizeMessage(m)
		if err != nil {
			return nil, err
		}
		if c.Role() == "tool" && len(out) > 0 && out[len(out)-1].Role() == "tool" {
			last := &out[len(out)-1]
			last.SetParts(append(last.Parts(), c.Parts()...))
			continue
		}
		out = append(out, c)
	}
	pending := map[string]bool{}
	var order []string
	for _, m := range out {
		switch m.Role() {
		case "assistant":
			for _, p := range m.Parts() {
				if p.Type() == "tool-call" && !p.ProviderExecuted() {
					if !pending[p.ToolCallID()] {
						order = append(order, p.ToolCallID())
					}
					pending[p.ToolCallID()] = true
				}
			}
		case "tool":
			for _, p := range m.Parts() {
				if p.Type() == "tool-result" {
					delete(pending, p.ToolCallID())
				}
			}
		case "user", "system":
			if len(pending) > 0 {
				return nil, missingResults(order, pending)
			}
		}
	}
	if len(pending) > 0 {
		return nil, missingResults(order, pending)
	}
	final := out[:0]
	for _, m := range out {
		if m.Role() == "tool" && len(m.Parts()) == 0 {
			continue
		}
		final = append(final, m)
	}
	return final, nil
}

// MissingToolResultsError is the AI SDK's MissingToolResultsError.
type MissingToolResultsError struct{ ToolCallIDs []string }

func (e *MissingToolResultsError) Error() string {
	return fmt.Sprintf("Tool results are missing for tool calls %s.", strings.Join(e.ToolCallIDs, ", "))
}

func missingResults(order []string, pending map[string]bool) error {
	var ids []string
	for _, id := range order {
		if pending[id] {
			ids = append(ids, id)
		}
	}
	return &MissingToolResultsError{ToolCallIDs: ids}
}

func standardizeMessage(m Message) (Message, error) {
	c := m.Clone()
	switch m.Role() {
	case "system":
		return c, nil
	case "user", "assistant":
		if text, ok := m.Text(); ok {
			c.SetParts([]Part{TextPart(text)})
			return c, nil
		}
		var parts []Part
		for _, p := range m.Parts() {
			if m.Role() == "user" {
				np, err := standardizeUserPart(p)
				if err != nil {
					return c, err
				}
				if np.Type() == "text" && np.Text() == "" {
					continue
				}
				parts = append(parts, np)
				continue
			}
			if p.Type() == "text" && p.Text() == "" && !p.Has("providerOptions") {
				continue
			}
			if p.Type() == "tool-approval-request" {
				continue
			}
			if p.Type() == "file" {
				np, err := normalizeFile(p, "data")
				if err != nil {
					return c, err
				}
				parts = append(parts, np)
				continue
			}
			parts = append(parts, p)
		}
		c.SetParts(parts)
		return c, nil
	case "tool":
		var parts []Part
		for _, p := range m.Parts() {
			if p.Type() == "tool-approval-response" && !p.ProviderExecuted() {
				continue
			}
			if p.Type() == "tool-result" {
				np, err := standardizeOutput(p)
				if err != nil {
					return c, err
				}
				p = np
			}
			parts = append(parts, p)
		}
		c.SetParts(parts)
		return c, nil
	}
	return c, fmt.Errorf("Invalid message role: %s", m.Role())
}

// standardizeOutput is mapToolResultOutput: file items of a "content" output
// get V4 file data.
func standardizeOutput(p Part) (Part, error) {
	out := p.Output()
	if out.Type() != "content" {
		return p, nil
	}
	var items []Part
	if json.Unmarshal(out.Value(), &items) != nil {
		return p, nil
	}
	for i, it := range items {
		if it.Type() == "file" {
			ni, err := normalizeFile(it, "data")
			if err != nil {
				return p, err
			}
			items[i] = ni
		}
	}
	o := out.Clone()
	o.Set("value", items)
	np := p.Clone()
	np.Set("output", o)
	return np, nil
}

func standardizeUserPart(p Part) (Part, error) {
	switch p.Type() {
	case "text":
		return p, nil
	case "image":
		f := newPart("file")
		raw, _ := p.Raw("image")
		f.Set("data", raw)
		mt := p.MediaType()
		if mt == "" {
			mt = "image"
		}
		f.Set("mediaType", mt)
		if po, ok := p.Raw("providerOptions"); ok {
			f.Set("providerOptions", po)
		}
		return normalizeFile(f, "data")
	case "file":
		return normalizeFile(p, "data")
	}
	return p, nil
}

// normalizeFile turns a file part's data into V4 file data and fills in an
// image media type from the bytes.
func normalizeFile(p Part, key string) (Part, error) {
	raw, _ := p.Raw(key)
	mediaType := p.MediaType()
	var data any
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.HasPrefix(s, "data:") {
			mt, b64, ok := parseDataURL(s)
			if !ok {
				return p, fmt.Errorf("Invalid data URL format in content")
			}
			if mt != "" {
				mediaType = mt
			}
			data = obj("type", "data", "data", b64)
		} else if u, err := url.Parse(s); err == nil && u.Scheme != "" && looksLikeURL(s) {
			data = obj("type", "url", "url", u.String())
		} else {
			data = obj("type", "data", "data", s)
		}
	} else {
		o, err := jsonx.ParseObject(raw)
		if err != nil {
			return p, fmt.Errorf("file part data must be a string or tagged file data")
		}
		data = o
	}
	var fd fileData
	_ = json.Unmarshal([]byte(jsonx.Stringify(data)), &fd)
	if fd.Type == "data" && fd.Data != "" {
		if mt := detectImage(fd.Data); mt != "" {
			mediaType = mt
		}
	}
	if mediaType == "" {
		return p, fmt.Errorf("Media type is missing for file part")
	}
	out := newPart("file")
	out.Set("mediaType", mediaType)
	if fn, ok := p.Raw("filename"); ok {
		out.Set("filename", fn)
	}
	out.Set("data", data)
	if po, ok := p.Raw("providerOptions"); ok {
		out.Set("providerOptions", po)
	}
	return out, nil
}

func looksLikeURL(s string) bool {
	i := strings.Index(s, ":")
	if i < 1 {
		return false
	}
	for _, c := range s[:i] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func parseDataURL(s string) (mediaType, b64 string, ok bool) {
	head, body, found := strings.Cut(s, ",")
	if !found {
		return "", "", false
	}
	head = strings.TrimPrefix(head, "data:")
	mt, _, _ := strings.Cut(head, ";")
	return mt, body, true
}

var imageSignatures = []struct {
	mediaType string
	prefix    []int
}{
	{"image/gif", []int{71, 73, 70, 56, 55, 97}},
	{"image/gif", []int{71, 73, 70, 56, 57, 97}},
	{"image/png", []int{137, 80, 78, 71}},
	{"image/jpeg", []int{255, 216}},
	{"image/webp", []int{82, 73, 70, 70, -1, -1, -1, -1, 87, 69, 66, 80}},
	{"image/bmp", []int{66, 77, -1, -1, -1, -1, 0, 0, 0, 0}},
	{"image/tiff", []int{73, 73, 42, 0}},
	{"image/tiff", []int{77, 77, 0, 42}},
	{"image/avif", []int{0, 0, 0, -1, 102, 116, 121, 112, 97, 118, 105, 102}},
	{"image/heic", []int{0, 0, 0, -1, 102, 116, 121, 112, 104, 101, 105, 99}},
}

// detectImage is detectMediaType({ data, topLevelType: "image" }) for base64 data.
func detectImage(b64 string) string {
	n := min(len(b64), 24)
	n -= n % 4
	bytes, err := base64.StdEncoding.DecodeString(b64[:n])
	if err != nil {
		bytes, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(b64[:n], "="))
	}
	for _, sig := range imageSignatures {
		if len(bytes) < len(sig.prefix) {
			continue
		}
		match := true
		for i, b := range sig.prefix {
			if b >= 0 && int(bytes[i]) != b {
				match = false
				break
			}
		}
		if match {
			return sig.mediaType
		}
	}
	return ""
}

func topLevel(mediaType string) string {
	if i := strings.Index(mediaType, "/"); i >= 0 {
		return mediaType[:i]
	}
	return mediaType
}

func isFullMediaType(mediaType string) bool {
	i := strings.Index(mediaType, "/")
	if i < 0 {
		return false
	}
	sub := mediaType[i+1:]
	return sub != "" && sub != "*"
}

// fileData is a standardized file part's data.
type fileData struct {
	Type string `json:"type"`
	Data string `json:"data"`
	URL  string `json:"url"`
	Text string `json:"text"`
}

func partFileData(p Part) fileData {
	raw, _ := p.Raw("data")
	var d fileData
	_ = json.Unmarshal(raw, &d)
	return d
}

// fullMediaType is resolveFullMediaType for a standardized file part.
func fullMediaType(p Part) (string, error) {
	mt := p.MediaType()
	if isFullMediaType(mt) {
		return mt, nil
	}
	d := partFileData(p)
	if d.Type == "data" && topLevel(mt) == "image" {
		if detected := detectImage(d.Data); detected != "" {
			return detected, nil
		}
		return "", fmt.Errorf("'file of media type \"%s\" must specify subtype since it could not be auto-detected' functionality not supported.", mt)
	}
	return "", fmt.Errorf("'file of media type \"%s\" must specify subtype since it is not passed as inline bytes' functionality not supported.", mt)
}

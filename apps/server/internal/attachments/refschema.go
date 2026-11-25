package attachments

import (
	"encoding/json"
	"regexp"
	"strings"
)

// A ref is filled in as raw base64 or as a data URL, whichever the field it
// sits in asks for. The field's JSON schema says so through contentEncoding,
// format, its description or its name; a field that says nothing gets raw
// base64, the usual way a JSON API carries a file's contents.

// Form is how a ref is filled in.
type Form int

const (
	FormBase64 Form = iota
	FormDataURL
)

type node = map[string]any

// schema walks a tool's input schema alongside its arguments.
type schema struct{ root node }

func parseSchema(raw json.RawMessage) *schema {
	if len(raw) == 0 {
		return nil
	}
	var m node
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil
	}
	return &schema{root: m}
}

// rootNodes is the schema's top level, with its branches.
func (s *schema) rootNodes() []node {
	if s == nil {
		return nil
	}
	return s.expand(s.root, 0)
}

// deref follows local $ref pointers (#/$defs/x, #/definitions/x).
func (s *schema) deref(n node) node {
	for i := 0; n != nil && i < 16; i++ {
		r, ok := n["$ref"].(string)
		if !ok || !strings.HasPrefix(r, "#") {
			return n
		}
		n = s.pointer(r[1:])
	}
	return n
}

func (s *schema) pointer(p string) node {
	var cur any = s.root
	if p == "" || p == "/" {
		return s.root
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(node)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	m, _ := cur.(node)
	return m
}

// expand is the node plus every allOf/anyOf/oneOf branch under it.
func (s *schema) expand(n node, depth int) []node {
	n = s.deref(n)
	if n == nil || depth > 8 {
		return nil
	}
	out := []node{n}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		arr, _ := n[k].([]any)
		for _, b := range arr {
			if bm, ok := b.(node); ok {
				out = append(out, s.expand(bm, depth+1)...)
			}
		}
	}
	return out
}

// prop is the schema of an object's property key.
func (s *schema) prop(nodes []node, key string) []node {
	if s == nil {
		return nil
	}
	var out []node
	for _, n := range nodes {
		if props, ok := n["properties"].(node); ok {
			if p, ok := props[key].(node); ok {
				out = append(out, s.expand(p, 0)...)
				continue
			}
		}
		if ap, ok := n["additionalProperties"].(node); ok {
			out = append(out, s.expand(ap, 0)...)
		}
	}
	return out
}

// item is the schema of an array's item at index i.
func (s *schema) item(nodes []node, i int) []node {
	if s == nil {
		return nil
	}
	var out []node
	for _, n := range nodes {
		if pi, ok := n["prefixItems"].([]any); ok && i < len(pi) {
			if m, ok := pi[i].(node); ok {
				out = append(out, s.expand(m, 0)...)
				continue
			}
		}
		switch it := n["items"].(type) {
		case node:
			out = append(out, s.expand(it, 0)...)
		case []any:
			if i < len(it) {
				if m, ok := it[i].(node); ok {
					out = append(out, s.expand(m, 0)...)
				}
			}
		}
	}
	return out
}

var (
	dataURLText = regexp.MustCompile(`data[ _-]?ur[li]|data:`)
	base64Text  = regexp.MustCompile(`base ?64`)
	urlText     = regexp.MustCompile(`\bur[li]s?\b`)
)

// urlKey is a property name like url, imageUrl, file_uri or urls.
func urlKey(key string) bool {
	k := strings.ToLower(strings.TrimSuffix(key, "s"))
	return strings.HasSuffix(k, "url") || strings.HasSuffix(k, "uri") ||
		strings.Contains(k, "url_") || strings.Contains(k, "url-")
}

// fieldForm picks how to fill a ref in at a field, and the field's maxLength
// (-1 when it has none).
func fieldForm(nodes []node, key string) (Form, int) {
	var text strings.Builder
	formats := map[string]bool{}
	encBase64 := false
	maxLen := -1
	for _, n := range nodes {
		for _, k := range []string{"description", "title"} {
			if s, ok := n[k].(string); ok {
				text.WriteString(strings.ToLower(s))
				text.WriteByte(' ')
			}
		}
		if f, ok := n["format"].(string); ok {
			formats[strings.ToLower(f)] = true
		}
		if e, ok := n["contentEncoding"].(string); ok && strings.EqualFold(e, "base64") {
			encBase64 = true
		}
		if m, ok := n["maxLength"].(float64); ok && m >= 0 && (maxLen < 0 || int(m) < maxLen) {
			maxLen = int(m)
		}
	}
	t := text.String()
	switch {
	case dataURLText.MatchString(t) || formats["data-url"] || formats["data-uri"]:
		return FormDataURL, maxLen
	case encBase64 || formats["byte"] || formats["base64"] || formats["binary"] || base64Text.MatchString(t):
		return FormBase64, maxLen
	case formats["uri"] || formats["url"] || formats["uri-reference"] || formats["iri"] || urlText.MatchString(t) || urlKey(key):
		return FormDataURL, maxLen
	}
	return FormBase64, maxLen
}

package models

import (
	"encoding/json"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// ResponseFormat is LanguageModelV4's responseFormat: generateText's
// Output.object asks for JSON matching Schema. M4 added it for the reminder
// parser.
//
// Anthropic (@ai-sdk/anthropic 4.0.99, structuredOutputMode "auto"): a model
// with native structured output gets output_config.format with the schema
// sanitized for the constrained decoder; any other model gets the JSON tool
// fallback (a tool named "json" it is forced to call). The client does not turn
// that tool call back into text: callers read ToolCalls[0].Input when the
// tool is named JSONToolName. OpenAI-compatible (@ai-sdk/openai-compatible
// 3.0.87 without supportsStructuredOutputs): response_format json_object.
type ResponseFormat struct {
	// "json" (the only type Palui sends).
	Type string
	// nil asks for any JSON.
	Schema      json.RawMessage
	Name        string
	Description string
}

// JSONToolName is the Anthropic JSON tool fallback's name.
const JSONToolName = "json"

// JSONToolDescription is that tool's description.
const JSONToolDescription = "Respond with a JSON object."

func (r *ResponseFormat) hasSchema() bool {
	return r != nil && r.Type == "json" && len(r.Schema) > 0
}

var supportedStringFormats = map[string]bool{"date-time": true, "time": true, "date": true, "duration": true, "email": true, "hostname": true, "uri": true, "ipv4": true, "ipv6": true, "uuid": true}

var descriptionConstraintKeys = []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "not"}

// SanitizeJSONSchema is sanitizeJsonSchema: the keywords Anthropic rejects in
// output_config.format.schema are dropped (constraints move into the
// description) and every object is closed.
func SanitizeJSONSchema(raw json.RawMessage) (any, error) {
	v, err := jsonx.Parse(raw)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return v, nil
	}
	return sanitizeSchema(o), nil
}

func sanitizeDefinition(v any) any {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return v
	}
	return sanitizeSchema(o)
}

func get(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

func has(o *jsonx.Object, k string) bool {
	v, ok := o.Get(k)
	return ok && v != nil
}

func sanitizeSchema(s *jsonx.Object) *jsonx.Object {
	out := jsonx.NewMap[any]()
	if has(s, "$ref") {
		out.Set("$ref", get(s, "$ref"))
		sanitizeDefs(s, out)
		return out
	}
	for _, k := range []string{"$schema", "$id", "title", "description"} {
		if has(s, k) {
			out.Set(k, get(s, k))
		}
	}
	for _, k := range []string{"default", "const"} {
		if v, ok := s.Get(k); ok {
			out.Set(k, v)
		}
	}
	for _, k := range []string{"enum", "type"} {
		if has(s, k) {
			out.Set(k, get(s, k))
		}
	}
	mapList := func(v any) any {
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		res := make([]any, len(arr))
		for i, d := range arr {
			res[i] = sanitizeDefinition(d)
		}
		return res
	}
	if has(s, "anyOf") {
		out.Set("anyOf", mapList(get(s, "anyOf")))
	} else if has(s, "oneOf") {
		out.Set("anyOf", mapList(get(s, "oneOf")))
	}
	if has(s, "allOf") {
		out.Set("allOf", mapList(get(s, "allOf")))
	}
	sanitizeDefs(s, out)
	if get(s, "type") == "object" || has(s, "properties") {
		if props, ok := get(s, "properties").(*jsonx.Object); ok {
			p := jsonx.NewMap[any]()
			for _, k := range props.Keys() {
				d, _ := props.Get(k)
				p.Set(k, sanitizeDefinition(d))
			}
			out.Set("properties", p)
		}
		out.Set("additionalProperties", false)
		if has(s, "required") {
			out.Set("required", get(s, "required"))
		}
	}
	if has(s, "items") {
		out.Set("items", mapList(sanitizeDefinitionOrList(get(s, "items"))))
	}
	if f, ok := get(s, "format").(string); ok && supportedStringFormats[f] {
		out.Set("format", f)
	}
	if c := constraintDescription(s); c != "" {
		if d, ok := get(out, "description").(string); ok {
			out.Set("description", d+"\n"+c)
		} else {
			out.Set("description", c)
		}
	}
	return out
}

func sanitizeDefinitionOrList(v any) any {
	if _, ok := v.([]any); ok {
		return v
	}
	return sanitizeDefinition(v)
}

func sanitizeDefs(s, out *jsonx.Object) {
	for _, k := range []string{"definitions", "$defs"} {
		defs, ok := get(s, k).(*jsonx.Object)
		if !ok {
			continue
		}
		d := jsonx.NewMap[any]()
		for _, name := range defs.Keys() {
			v, _ := defs.Get(name)
			d.Set(name, sanitizeDefinition(v))
		}
		out.Set(k, d)
	}
}

func constraintDescription(s *jsonx.Object) string {
	var parts []string
	for _, k := range descriptionConstraintKeys {
		v, ok := s.Get(k)
		if !ok || v == nil || v == false {
			continue
		}
		name := k
		var b strings.Builder
		for _, r := range name {
			if r >= 'A' && r <= 'Z' {
				b.WriteByte(' ')
				b.WriteRune(r + ('a' - 'A'))
			} else {
				b.WriteRune(r)
			}
		}
		val, isStr := v.(string)
		if !isStr {
			val = jsonx.Stringify(v)
		}
		parts = append(parts, b.String()+": "+val)
	}
	if f, ok := get(s, "format").(string); ok && !supportedStringFormats[f] {
		parts = append(parts, "format: "+f)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + "."
}

const maxOptionalProperties = 10

// nativeSchemaOK is supportsNativeStructuredOutputSchema for the schemas
// Palui sends (no $ref): no open objects and at most 10 optional properties.
func nativeSchemaOK(raw json.RawMessage) bool {
	v, err := jsonx.Parse(raw)
	if err != nil {
		return false
	}
	optional, open := schemaStats(v)
	return !open && optional <= maxOptionalProperties
}

func schemaStats(node any) (optional int, open bool) {
	o, ok := node.(*jsonx.Object)
	if !ok {
		return 0, false
	}
	add := func(child any) {
		n, op := schemaStats(child)
		optional += n
		open = open || op
	}
	if isOpenObject(o) {
		open = true
	}
	if props, ok := get(o, "properties").(*jsonx.Object); ok {
		req := map[string]bool{}
		if arr, ok := get(o, "required").([]any); ok {
			for _, r := range arr {
				if s, ok := r.(string); ok {
					req[s] = true
				}
			}
		}
		for _, k := range props.Keys() {
			if !req[k] {
				optional++
			}
		}
	}
	for _, k := range []string{"additionalProperties", "contains", "else", "if", "items", "not", "then"} {
		v := get(o, k)
		if arr, ok := v.([]any); ok {
			for _, x := range arr {
				add(x)
			}
		} else {
			add(v)
		}
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if arr, ok := get(o, k).([]any); ok {
			for _, x := range arr {
				add(x)
			}
		}
	}
	for _, k := range []string{"dependentSchemas", "patternProperties", "properties"} {
		if m, ok := get(o, k).(*jsonx.Object); ok {
			for _, kk := range m.Keys() {
				x, _ := m.Get(kk)
				add(x)
			}
		}
	}
	return optional, open
}

func isOpenObject(o *jsonx.Object) bool {
	ap, hasAP := o.Get("additionalProperties")
	if ap == true {
		return true
	}
	if _, ok := ap.(*jsonx.Object); ok {
		return true
	}
	if pp, ok := get(o, "patternProperties").(*jsonx.Object); ok && pp.Len() > 0 {
		return true
	}
	isObj := get(o, "type") == "object"
	if arr, ok := get(o, "type").([]any); ok {
		for _, t := range arr {
			if t == "object" {
				isObj = true
			}
		}
	}
	return isObj && !has(o, "properties") && !(hasAP && ap == false)
}

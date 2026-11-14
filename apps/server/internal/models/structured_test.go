package models_test

import (
	"encoding/json"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
)

func TestSanitizeJSONSchema(t *testing.T) {
	in := `{"$schema":"x","type":"object","properties":{"a":{"type":"string","minLength":2,"pattern":"^a","format":"email"},"b":{"type":"string","format":"phone","description":"B."},"c":{"oneOf":[{"type":"number","maximum":3},{"type":"null"}]},"d":{"type":"array","items":{"type":"object","properties":{"x":{"type":"boolean"}}},"maxItems":4}},"required":["a"],"$defs":{"n":{"type":"integer","exclusiveMinimum":0}}}`
	got, err := models.SanitizeJSONSchema(json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"$schema":"x","type":"object","$defs":{"n":{"type":"integer","description":"exclusive minimum: 0."}},"properties":{"a":{"type":"string","format":"email","description":"min length: 2; pattern: ^a."},"b":{"description":"B.\nformat: phone.","type":"string"},"c":{"anyOf":[{"type":"number","description":"maximum: 3."},{"type":"null"}]},"d":{"type":"array","items":{"type":"object","properties":{"x":{"type":"boolean"}},"additionalProperties":false},"description":"max items: 4."}},"additionalProperties":false,"required":["a"]}`
	if s := jsonx.Stringify(got); s != want {
		t.Fatalf("\n got %s\nwant %s", s, want)
	}
}

func rf(schema string) *models.ResponseFormat {
	return &models.ResponseFormat{Type: "json", Schema: json.RawMessage(schema)}
}

func anthropicBody(t *testing.T, model string, o models.CallOptions) (*jsonx.Object, map[string]string) {
	t.Helper()
	o.Prompt = []models.Message{models.User("hi")}
	r, err := models.NewAnthropic(models.AnthropicConfig{BaseURL: "http://x/v1", APIKey: "k"}, model).BuildRequest(o)
	if err != nil {
		t.Fatal(err)
	}
	return r.Body, r.Headers
}

func get(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	return jsonx.Stringify(v)
}

func TestAnthropicResponseFormat(t *testing.T) {
	closed := `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"additionalProperties":false}`
	t.Run("native structured output goes in output_config with the effort", func(t *testing.T) {
		b, h := anthropicBody(t, "claude-sonnet-4-6", models.CallOptions{ResponseFormat: rf(closed), Reasoning: "low"})
		if get(b, "output_config") != `{"effort":"low","format":{"type":"json_schema","schema":{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false,"required":["a"]}}}` {
			t.Fatal(get(b, "output_config"))
		}
		if _, ok := b.Get("tools"); ok || h["anthropic-beta"] != "" {
			t.Fatal("tools or beta")
		}
	})
	t.Run("an older model gets the forced JSON tool", func(t *testing.T) {
		b, _ := anthropicBody(t, "claude-3-5-haiku-20241022", models.CallOptions{ResponseFormat: rf(closed), Tools: []models.ToolDef{{Name: "other", InputSchema: json.RawMessage(`{"type":"object"}`)}}, ToolChoice: &models.ToolChoice{Type: "none"}})
		if get(b, "tool_choice") != `{"type":"any","disable_parallel_tool_use":true}` {
			t.Fatal(get(b, "tool_choice"))
		}
		tools, _ := b.Get("tools")
		if len(tools.([]any)) != 1 || get(tools.([]any)[0].(*jsonx.Object), "name") != `"json"` || get(tools.([]any)[0].(*jsonx.Object), "description") != `"Respond with a JSON object."` {
			t.Fatal(get(b, "tools"))
		}
		if _, ok := b.Get("output_config"); ok {
			t.Fatal("output_config")
		}
	})
	t.Run("an open object falls back to the tool, auto when forced tool use is refused", func(t *testing.T) {
		b, _ := anthropicBody(t, "claude-opus-5-5", models.CallOptions{ResponseFormat: rf(`{"type":"object"}`)})
		if get(b, "tool_choice") != `{"type":"auto","disable_parallel_tool_use":true}` {
			t.Fatal(get(b, "tool_choice"))
		}
	})
	t.Run("a model that refuses forced tool use keeps native output for a closed schema", func(t *testing.T) {
		b, _ := anthropicBody(t, "claude-opus-5-5", models.CallOptions{ResponseFormat: rf(closed)})
		if _, ok := b.Get("output_config"); !ok {
			t.Fatal("no output_config")
		}
	})
	t.Run("no schema sends nothing extra", func(t *testing.T) {
		b, _ := anthropicBody(t, "claude-sonnet-4-6", models.CallOptions{ResponseFormat: &models.ResponseFormat{Type: "json"}})
		if _, ok := b.Get("output_config"); ok {
			t.Fatal("output_config")
		}
	})
}

func TestOpenAIResponseFormat(t *testing.T) {
	r, err := models.NewOpenAI(models.OpenAIConfig{BaseURL: "http://x/v1", Name: "cliproxy"}, "m").BuildRequest(models.CallOptions{Prompt: []models.Message{models.User("hi")}, ResponseFormat: rf(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if get(r.Body, "response_format") != `{"type":"json_object"}` {
		t.Fatal(get(r.Body, "response_format"))
	}
}

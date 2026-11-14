package registry_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// TestChannelManifestParsesMatchTheTSBuild: the bot token and phone checks,
// with zod's issues, from testdata/channels.json.
func TestChannelManifestParsesMatchTheTSBuild(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/channels.json")
	testutil.Must(t, err)
	var g struct {
		Manifest []struct {
			Type   string          `json:"type"`
			Kind   string          `json:"kind"`
			Key    string          `json:"key"`
			Input  json.RawMessage `json:"input"`
			Result json.RawMessage `json:"result"`
		} `json:"manifest"`
	}
	testutil.Must(t, json.Unmarshal(raw, &g))
	if len(g.Manifest) < 20 {
		t.Fatal("too few cases")
	}
	for _, c := range g.Manifest {
		input, err := jsonx.Parse(c.Input)
		testutil.Must(t, err)
		var out any
		var perr error
		if c.Kind == "secret" {
			var schema zod.Schema
			for _, f := range telegram.SecretZod {
				if f.Key == c.Key {
					schema = f.Schema
				}
			}
			out, perr = zod.Parse(schema, input)
		} else {
			m, _ := registry.Manifest(c.Type)
			out, perr = m.ParseConfig(input.(*jsonx.Object))
		}
		got := jsonx.NewMap[any]()
		if perr != nil {
			ze := perr.(*zod.Error)
			issues := make([]any, len(ze.Issues))
			for i, is := range ze.Issues {
				issues[i] = is.JSON()
			}
			got.Set("ok", false)
			got.Set("issues", issues)
		} else {
			got.Set("ok", true)
			got.Set("value", out)
		}
		want, _ := jsonx.Parse(c.Result)
		if jsonx.Canonical(got) != jsonx.Canonical(want) {
			t.Errorf("%s %s %s:\n got  %s\n want %s", c.Type, c.Key, c.Input, jsonx.Canonical(got), jsonx.Canonical(want))
		}
	}
}

package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/aigateway"
	"github.com/zakiego/palui/apps/server/internal/connectors/cliproxy"
	"github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	"github.com/zakiego/palui/apps/server/internal/connectors/httpreq"
	"github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/connectors/printer"
	"github.com/zakiego/palui/apps/server/internal/connectors/recall"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/connectors/t3code"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/connectors/voice"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var configSchemas = map[string]*zod.ObjectS{
	"telegram":   telegram.ConfigZod,
	"tgbot":      telegram.ConfigZod,
	"whatsapp":   whatsapp.ConfigZod,
	"cliproxy":   cliproxy.GatewayConfigZod("http://host.docker.internal:8317"),
	"ai-gateway": cliproxy.GatewayConfigZod("http://host.docker.internal:8793"),
	"recall":     recall.ConfigZod,
	"mcp":        mcp.ConfigZod,
	"http":       httpreq.ConfigZod,
	"gcal":       gcal.ConfigZod,
	"t3code":     t3code.ConfigZod,
	"printer":    printer.ConfigZod,
	"voice":      voice.ConfigZod,
}

var secretSchemas = map[string][]zod.Field{
	"telegram":   telegram.SecretZod,
	"tgbot":      telegram.SecretZod,
	"cliproxy":   cliproxy.SecretZod,
	"ai-gateway": aigateway.SecretZod,
	"recall":     recall.SecretZod,
	"mcp":        mcp.SecretZod,
	"http":       httpreq.SecretZod,
	"gcal":       gcal.SecretZod,
	"t3code":     t3code.SecretZod,
	"voice":      voice.SecretZod,
}

func TestManifestsMatchTheTSBuildInCatalogOrder(t *testing.T) {
	g := loadGolden(t)
	var wantOrder []string
	for _, raw := range g.Manifests {
		var m struct {
			ID string `json:"id"`
		}
		testutil.Must(t, json.Unmarshal(raw, &m))
		wantOrder = append(wantOrder, m.ID)
	}
	var gotOrder []string
	for _, m := range registry.Manifests() {
		gotOrder = append(gotOrder, m.ID)
	}
	if jsonx.Stringify(wantOrder) != jsonx.Stringify(gotOrder) {
		t.Fatalf("catalog order\n want %v\n got  %v", wantOrder, gotOrder)
	}
	if len(registry.Catalog()) != len(g.Manifests) {
		t.Errorf("catalog has %d slots, TS has %d manifests", len(registry.Catalog()), len(g.Manifests))
	}

	for _, raw := range g.Manifests {
		v, err := jsonx.Parse(raw)
		testutil.Must(t, err)
		want := v.(*jsonx.Object)
		id, _ := want.Get("id")
		m, ok := registry.Manifest(id.(string))
		if !ok {
			t.Errorf("no manifest %s", id)
			continue
		}
		label := id.(string)

		plain := want.Clone()
		for _, k := range []string{"configKeys", "configDefaults", "secretFields"} {
			plain.Delete(k)
		}
		b, err := jsonx.Marshal(m)
		testutil.Must(t, err)
		gotPlain, err := jsonx.Parse(b)
		testutil.Must(t, err)
		if w, g := jsonx.Canonical(plain), jsonx.Canonical(gotPlain); w != g {
			t.Errorf("%s: manifest\n want %s\n got  %s\n %s", label, w, g, testutil.DiffAt(w, g))
		}

		schema := configSchemas[label]
		if schema == nil {
			schema = zod.Object()
		}
		var keys []any
		for _, f := range schema.Fields() {
			keys = append(keys, f.Key)
		}
		wantKeys, _ := want.Get("configKeys")
		if keys == nil {
			keys = []any{}
		}
		if jsonx.Stringify(wantKeys) != jsonx.Stringify(keys) {
			t.Errorf("%s: config keys %s, got %s", label, jsonx.Stringify(wantKeys), jsonx.Stringify(keys))
		}
		defaults, _ := want.Get("configDefaults")
		out, perr := m.ParseConfig(nil)
		var got *jsonx.Object
		if perr != nil {
			ze := perr.(*zod.Error)
			issues := make([]any, len(ze.Issues))
			for i, is := range ze.Issues {
				issues[i] = is.JSON()
			}
			got = jsonx.NewMap[any]()
			got.Set("ok", false)
			got.Set("issues", issues)
		} else {
			got = jsonx.NewMap[any]()
			got.Set("ok", true)
			got.Set("value", out)
		}
		if jsonx.Stringify(defaults) != jsonx.Stringify(got) {
			t.Errorf("%s: config defaults\n want %s\n got  %s", label, jsonx.Stringify(defaults), jsonx.Stringify(got))
		}

		var secrets []any
		for _, f := range secretSchemas[label] {
			_, err := zod.Parse(f.Schema, zod.Undefined)
			o := jsonx.NewMap[any]()
			o.Set("name", f.Key)
			o.Set("optional", err == nil)
			secrets = append(secrets, o)
		}
		if secrets == nil {
			secrets = []any{}
		}
		wantSecrets, _ := want.Get("secretFields")
		if jsonx.Stringify(wantSecrets) != jsonx.Stringify(secrets) {
			t.Errorf("%s: secrets %s, got %s", label, jsonx.Stringify(wantSecrets), jsonx.Stringify(secrets))
		}
		var names []any
		for _, n := range m.SecretFields {
			names = append(names, n)
		}
		var wantNames []any
		for _, s := range wantSecrets.([]any) {
			n, _ := s.(*jsonx.Object).Get("name")
			wantNames = append(wantNames, n)
		}
		if jsonx.Stringify(names) != jsonx.Stringify(wantNames) {
			t.Errorf("%s: manifest.SecretFields %v, want %v", label, names, wantNames)
		}
	}
}

func TestRuntimesCoverEveryPortedManifest(t *testing.T) {
	rts := registry.Runtimes()
	for _, m := range registry.Manifests() {
		rt, ok := rts[m.ID]
		if !ok || rt.Test == nil || rt.Start == nil || rt.Manifest.ID != m.ID {
			t.Errorf("%s: runtime missing or incomplete", m.ID)
		}
	}
	for _, e := range registry.Catalog() {
		if e.Runtime == nil {
			t.Errorf("%s has no runtime", e.ID)
		}
	}
}

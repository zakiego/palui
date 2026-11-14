package registry_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type goldenFile struct {
	Version   string                              `json:"version"`
	Now       int64                               `json:"now"`
	SeedNow   int64                               `json:"seedNow"`
	Manifests []json.RawMessage                   `json:"manifests"`
	Catalogs  map[string][]goldenVariant          `json:"catalogs"`
	Inputs    map[string]map[string][]goldenInput `json:"inputs"`
	Calls     json.RawMessage                     `json:"calls"`
	Helpers   json.RawMessage                     `json:"helpers"`
}

type goldenVariant struct {
	Variant string       `json:"variant"`
	Tools   []goldenTool `json:"tools"`
}

type goldenTool struct {
	Name          string          `json:"name"`
	Title         *string         `json:"title"`
	Description   *string         `json:"description"`
	Class         string          `json:"class"`
	Untrusted     bool            `json:"untrusted"`
	GuestSafe     bool            `json:"guestSafe"`
	FlowApproval  *string         `json:"flowApproval"`
	NeedsApproval bool            `json:"needsApproval"`
	Preview       bool            `json:"preview"`
	InputSchema   json.RawMessage `json:"inputSchema"`
}

type goldenInput struct {
	Input  json.RawMessage `json:"input"`
	Parsed json.RawMessage `json:"parsed"`
	Error  *string         `json:"error"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/connectors.json")
	testutil.Must(t, err)
	var g goldenFile
	testutil.Must(t, json.Unmarshal(raw, &g))
	return g
}

// ordered re-reads JSON keeping key order (jsonx), for exact text comparison.
func ordered(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	v, err := jsonx.Parse(raw)
	testutil.Must(t, err)
	return jsonx.Stringify(v)
}

func describeTools(set *connectors.ToolSet) []goldenTool {
	var out []goldenTool
	for _, name := range set.Keys() {
		pt, _ := set.Get(name)
		var title *string
		if pt.Title != "" {
			s := pt.Title
			title = &s
		}
		desc := pt.Tool.Description
		var fa *string
		if pt.FlowApproval != "" {
			s := pt.FlowApproval
			fa = &s
		}
		out = append(out, goldenTool{Name: name, Title: title, Description: &desc, Class: pt.Class, Untrusted: pt.Untrusted, GuestSafe: pt.GuestSafe, FlowApproval: fa, NeedsApproval: pt.NeedsApproval != nil, Preview: pt.Preview != nil, InputSchema: pt.Tool.InputSchema})
	}
	return out
}

func compareCatalog(t *testing.T, label string, want []goldenTool, got []goldenTool) {
	t.Helper()
	if len(want) != len(got) {
		names := func(l []goldenTool) string {
			var s []string
			for _, x := range l {
				s = append(s, x.Name)
			}
			return strings.Join(s, ",")
		}
		t.Errorf("%s: want tools %s, got %s", label, names(want), names(got))
		return
	}
	for i := range want {
		w, g := want[i], got[i]
		at := label + "." + w.Name
		if w.Name != g.Name {
			t.Errorf("%s: name %q, got %q", at, w.Name, g.Name)
			continue
		}
		if ptr(w.Title) != ptr(g.Title) {
			t.Errorf("%s: title %q, got %q", at, ptr(w.Title), ptr(g.Title))
		}
		if ptr(w.Description) != ptr(g.Description) {
			t.Errorf("%s: description\n want %q\n got  %q", at, ptr(w.Description), ptr(g.Description))
		}
		if w.Class != g.Class || w.Untrusted != g.Untrusted || w.GuestSafe != g.GuestSafe || ptr(w.FlowApproval) != ptr(g.FlowApproval) || w.NeedsApproval != g.NeedsApproval || w.Preview != g.Preview {
			t.Errorf("%s: flags\n want %+v\n got  %+v", at, w, g)
		}
		ws, gs := ordered(t, w.InputSchema), ordered(t, g.InputSchema)
		if ws != gs {
			t.Errorf("%s: input schema\n want %s\n got  %s\n %s", at, ws, gs, testutil.DiffAt(ws, gs))
		}
	}
}

func ptr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

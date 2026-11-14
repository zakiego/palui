package registry_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	attachmentsconn "github.com/zakiego/palui/apps/server/internal/connectors/attachments"
	chatsconn "github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	flowsconn "github.com/zakiego/palui/apps/server/internal/connectors/flows"
	jobsconn "github.com/zakiego/palui/apps/server/internal/connectors/jobs"
	notesconn "github.com/zakiego/palui/apps/server/internal/connectors/notes"
	outboundconn "github.com/zakiego/palui/apps/server/internal/connectors/outbound"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	remindersconn "github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/connectors/remotemcp"
	settingsconn "github.com/zakiego/palui/apps/server/internal/connectors/settings"
	webfetchconn "github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/version"
)

const APP = "https://palui.example.com"

type goldenCalls struct {
	Version string `json:"version"`
	SeedNow int64  `json:"seedNow"`
	Calls   []struct {
		Name      string         `json:"name"`
		Connector string         `json:"connector"`
		Turn      map[string]any `json:"turn"`
		Steps     []struct {
			Tool   string          `json:"tool"`
			Kind   string          `json:"kind"`
			Input  json.RawMessage `json:"input"`
			Result string          `json:"result"`
		} `json:"steps"`
	} `json:"calls"`
	Tests map[string]json.RawMessage `json:"tests"`
}

var goldenIDRe = regexp.MustCompile(`\b([a-z]{2,6})_[0-9a-z]{19}\b`)
var shareRe = regexp.MustCompile(`/api/share/[A-Za-z0-9_-]{32}`)

func normText(s string) string {
	s = shareRe.ReplaceAllString(s, "/api/share/<token>")
	seen := map[string]string{}
	return goldenIDRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := seen[m]; ok {
			return v
		}
		v := goldenIDRe.FindStringSubmatch(m)[1] + "#" + strconv.Itoa(len(seen)+1)
		seen[m] = v
		return v
	})
}

func seedCopy(t *testing.T) *db.DB {
	t.Helper()
	src, err := os.Open("../../../testdata/seeded.db")
	testutil.Must(t, err)
	defer src.Close()
	path := filepath.Join(t.TempDir(), "palui.db")
	dst, err := os.Create(path)
	testutil.Must(t, err)
	_, err = io.Copy(dst, src)
	testutil.Must(t, err)
	testutil.Must(t, dst.Close())
	d, err := db.Open(path, db.OpenOptions{})
	testutil.Must(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

func goldenConfig(t *testing.T, version_ string) {
	prev := config.Get()
	next := prev
	next.Mode, next.AppURL, next.MasterKey = "dev", APP, config.DevMasterKey
	config.Set(&next)
	prevVersion := version.Version
	version.Version = version_
	t.Cleanup(func() {
		config.Set(&prev)
		version.Version = prevVersion
	})
}

func privateResolver(context.Context, string) ([]safenet.ResolvedAddress, error) {
	return []safenet.ResolvedAddress{{Address: "10.0.0.5", Family: 4}}, nil
}

func goldenTools(t *testing.T, d *db.DB, connector string, turn connectors.TurnInfo, now int64) *connectors.ToolSet {
	clock := func() int64 { return now }
	must := func(s *connectors.ToolSet, err error) *connectors.ToolSet {
		testutil.Must(t, err)
		return s
	}
	switch connector {
	case "reminders":
		return remindersconn.Tools(d, turn, clock)
	case "jobs":
		return jobsconn.Tools(d, clock)
	case "notes":
		return must(notesconn.Tools(d, clock, turn.AgentID))
	case "attachments":
		return attachmentsconn.Tools(d, turn, attachmentsconn.Options{Clock: clock, Key: func() string { return config.DevMasterKey }, AppURL: func() string { return APP }})
	case "chats":
		return must(chatsconn.Tools(d, turn, clock))
	case "settings":
		return must(settingsconn.Tools(d, clock, turn.AgentID))
	case "outbound":
		return must(outboundconn.Tools(d, turn, clock))
	case "flows":
		set := flowsconn.Tools(d, turn, flowsconn.Deps{Clock: clock})
		set.Set("recap", flowsconn.RecapTool(d, turn, clock))
		return set
	case "remote-mcp":
		return remotemcp.Tools(remotemcp.Deps{DB: d, MasterKey: config.DevMasterKey, Clock: clock, Resolver: privateResolver})
	case "webfetch":
		set := connectors.NewToolSet()
		set.Set("fetch_page", connectors.PaluiTool{Tool: webfetchconn.FetchPageTool(nil), Class: "read"})
		return set
	}
	t.Fatalf("no tools for %s", connector)
	return nil
}

var fillRe = regexp.MustCompile(`^\{\{(\d+)\.(.+)\}\}$`)

func fill(v any, results []conntest.Value) any {
	switch x := v.(type) {
	case string:
		if m := fillRe.FindStringSubmatch(x); m != nil {
			n, _ := strconv.Atoi(m[1])
			return results[n].Get(m[2])
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, y := range x {
			out[i] = fill(y, results)
		}
		return out
	case *jsonx.Object:
		out := jsonx.NewMap[any]()
		for _, k := range x.Keys() {
			y, _ := x.Get(k)
			out.Set(k, fill(y, results))
		}
		return out
	}
	return v
}

func TestScriptedCallsMatchTheTSBuild(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/connectors.json")
	testutil.Must(t, err)
	var g goldenCalls
	testutil.Must(t, json.Unmarshal(raw, &g))
	goldenConfig(t, g.Version)
	steps := 0
	for _, sc := range g.Calls {
		t.Run(sc.Name, func(t *testing.T) {
			d := seedCopy(t)
			turn := connectors.TurnInfo{RunID: "run_calls", ChatID: sc.Turn["chatId"].(string), Channel: "web", Trigger: "message", TZ: "Asia/Makassar", Now: g.SeedNow, Tainted: func() bool { return false }}
			if v, ok := sc.Turn["agentId"].(string); ok {
				turn.AgentID = v
			}
			if v, ok := sc.Turn["origin"].(string); ok {
				turn.Origin = v
			}
			if v, ok := sc.Turn["mode"].(string); ok {
				turn.Mode = v
			}
			if v, ok := sc.Turn["ownerText"].(string); ok {
				turn.OwnerText = &v
			}
			var results []conntest.Value
			for i, st := range sc.Steps {
				steps++
				set := goldenTools(t, d, sc.Connector, turn, g.SeedNow)
				pt, ok := set.Get(st.Tool)
				if !ok {
					t.Fatalf("step %d: no tool %s", i, st.Tool)
				}
				in, err := jsonx.Parse(st.Input)
				testutil.Must(t, err)
				input := fill(in, results)
				inputJSON := []byte(jsonx.Stringify(input))
				var got conntest.Value
				switch st.Kind {
				case "preview":
					got = conntest.Of(pt.Preview(inputJSON))
				case "needsApproval":
					got = conntest.Of(pt.NeedsApproval(inputJSON))
				default:
					if parsed, err := pt.Tool.Parse(input); err == nil {
						inputJSON = []byte(jsonx.Stringify(parsed))
					}
					out, err := pt.Tool.Execute(context.Background(), inputJSON, connectors.ToolCallInfo{ToolCallID: "tc_1"})
					if err != nil {
						got = conntest.Of(map[string]string{"thrown": err.Error()})
					} else {
						got = conntest.Of(out)
					}
				}
				results = append(results, got)
				if gotText := normText(got.String()); gotText != st.Result {
					t.Errorf("step %d %s %s\n want %s\n got  %s\n %s", i, st.Tool, string(st.Input), st.Result, gotText, testutil.DiffAt(st.Result, gotText))
				}
			}
		})
	}
	if steps < 100 {
		t.Fatalf("only %d steps", steps)
	}
}

func TestBuiltinTestDetailsMatchTheTSBuildOnTheSeededDatabase(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/connectors.json")
	testutil.Must(t, err)
	var g goldenCalls
	testutil.Must(t, json.Unmarshal(raw, &g))
	goldenConfig(t, g.Version)
	d := seedCopy(t)
	for id, want := range g.Tests {
		if id == "hooks.live" {
			continue
		}
		rt := registry.Runtimes()[id]
		c := conntest.Ctx(t, d, id, nil, nil)
		res, err := rt.Test(c)
		testutil.Must(t, err)
		if got := conntest.Of(res).String(); got != ordered(t, want) {
			t.Errorf("%s: test()\n want %s\n got  %s", id, ordered(t, want), got)
		}
		if id == "hooks" {
			h, err := rt.Start(c)
			testutil.Must(t, err)
			if got := conntest.Of(h.Live()).String(); got != ordered(t, g.Tests["hooks.live"]) {
				t.Errorf("hooks live\n want %s\n got  %s", ordered(t, g.Tests["hooks.live"]), got)
			}
		}
	}
	_ = strings.TrimSpace
}

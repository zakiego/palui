package remotemcp_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/connectors/remotemcp"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const KEY = "test-master-key-test-master-key-0000"

func public(context.Context, string) ([]safenet.ResolvedAddress, error) {
	return []safenet.ResolvedAddress{{Address: "93.184.216.34", Family: 4}}, nil
}

func private(context.Context, string) ([]safenet.ResolvedAddress, error) {
	return []safenet.ResolvedAddress{{Address: "10.0.0.5", Family: 4}}, nil
}

func register(s *sdk.Server) {
	conntest.AddRawTool(s, "jadwal", "Prayer times", `{"type":"object","properties":{"kota":{"type":"string"}},"required":["kota"]}`, &sdk.ToolAnnotations{ReadOnlyHint: true}, func(a map[string]any) *sdk.CallToolResult {
		return conntest.TextResult("subuh " + a["kota"].(string))
	})
	conntest.AddRawTool(s, "mystery", "No hints", "", nil, func(map[string]any) *sdk.CallToolResult { return conntest.TextResult("?") })
}

// toLocal sends https://sholat.example.com/... to the local test server.
type toLocal struct{ target *url.URL }

func (t toLocal) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if r.URL.Host == "sholat.example.com" {
		r.URL.Scheme, r.URL.Host, r.Host = t.target.Scheme, t.target.Host, t.target.Host
	}
	return http.DefaultTransport.RoundTrip(r)
}

type countingTransport struct{ n *atomic.Int32 }

func (c countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return nil, http.ErrUseLastResponse
}

func TestCheckServerURLAcceptsPublicHTTPSOnly(t *testing.T) {
	ctx := context.Background()
	if _, err := remotemcp.CheckServerURL(ctx, "https://sholat.example.com/mcp", public); err != nil {
		t.Fatal(err)
	}
	cases := map[string]*regexp.Regexp{
		"http://sholat.example.com/mcp":    regexp.MustCompile(`https`),
		"https://127.0.0.1/mcp":            regexp.MustCompile(`(?i)loopback|internal|private`),
		"https://localhost/mcp":            regexp.MustCompile(`internal`),
		"https://host.docker.internal/mcp": regexp.MustCompile(`.`),
	}
	for in, re := range cases {
		if _, err := remotemcp.CheckServerURL(ctx, in, public); err == nil || !re.MatchString(err.Error()) {
			t.Fatal(in, err)
		}
	}
	if _, err := remotemcp.CheckServerURL(ctx, "https://sneaky.example.com/mcp", private); err == nil || !strings.Contains(err.Error(), "10.0.0.5") {
		t.Fatal(err)
	}
}

func TestAddsListsAndRemovesAServerAddedFromChat(t *testing.T) {
	d := testutil.DB(t)
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register})
	target, _ := url.Parse(srv.URL)
	var reconciles atomic.Int32
	set := remotemcp.Tools(remotemcp.Deps{DB: d, MasterKey: KEY, Reconcile: func() { reconciles.Add(1) }, Resolver: public, Client: &http.Client{Transport: toLocal{target}}, Clock: func() int64 { return 1000 }})
	for name, class := range map[string]string{"add_mcp_server": "destructive", "remove_mcp_server": "destructive", "list_mcp_servers": "read"} {
		if pt, _ := set.Get(name); pt.Class != class {
			t.Fatal(name)
		}
	}
	added := conntest.Call(t, set, "add_mcp_server", `{"name":"jadwal-sholat","url":"https://sholat.example.com/mcp"}`)
	if !added.B("ok") {
		t.Fatal(added)
	}
	var classes []string
	for _, x := range added.List("tools") {
		v := conntest.Value{V: x}
		classes = append(classes, v.S("name")+":"+v.S("class"))
	}
	if strings.Join(classes, ",") != "mcp-jadwal-sholat__jadwal:read,mcp-jadwal-sholat__mystery:destructive" {
		t.Fatal(classes)
	}
	row, found, err := connectors.Get(d, "mcp-jadwal-sholat")
	testutil.Must(t, err)
	if !found || row.Type != "mcp" || row.Trust != "external" || !row.Enabled || row.Status != "connecting" || row.Secrets != nil {
		t.Fatalf("%+v", row)
	}
	cfg := mcpconn.ConfigFrom(row.Config)
	if cfg.URL != "https://sholat.example.com/mcp" || cfg.AuthMode != "none" || !cfg.AddedFromChat {
		t.Fatalf("%+v", cfg)
	}
	if reconciles.Load() != 1 {
		t.Fatal(reconciles.Load())
	}
	if conntest.Call(t, set, "add_mcp_server", `{"name":"jadwal-sholat","url":"https://sholat.example.com/mcp"}`).B("ok") {
		t.Fatal("added twice")
	}
	list := conntest.Call(t, set, "list_mcp_servers", `{}`)
	if got := list.JSON("servers"); got != `[{"id":"mcp-jadwal-sholat","name":"jadwal-sholat","url":"https://sholat.example.com/mcp","status":"connecting","auth":"none","toolsEnabled":2,"addedFromChat":true}]` {
		t.Fatal(got)
	}
	if !conntest.Call(t, set, "remove_mcp_server", `{"name":"jadwal-sholat"}`).B("ok") {
		t.Fatal("remove")
	}
	if _, found, _ := connectors.Get(d, "mcp-jadwal-sholat"); found {
		t.Fatal("still there")
	}
	if reconciles.Load() != 2 {
		t.Fatal(reconciles.Load())
	}
}

func TestRefusesPrivateTargetsWithoutConnecting(t *testing.T) {
	d := testutil.DB(t)
	var n atomic.Int32
	set := remotemcp.Tools(remotemcp.Deps{DB: d, MasterKey: KEY, Resolver: private, Client: &http.Client{Transport: countingTransport{&n}}})
	r := conntest.Call(t, set, "add_mcp_server", `{"name":"evil","url":"https://evil.example.com/mcp"}`)
	if r.B("ok") || !strings.Contains(r.S("error"), "URL ditolak") {
		t.Fatal(r)
	}
	if n.Load() != 0 {
		t.Fatal("connected")
	}
	if _, found, _ := connectors.Get(d, "mcp-evil"); found {
		t.Fatal("saved")
	}
}

func TestWontRemoveAServerSetUpInTheDashboardAndNeverListsItsToken(t *testing.T) {
	d := testutil.DB(t)
	cfg, _ := jsonx.ParseObject([]byte(`{"url":"https://vorneo.id/api/mcp/?key=abc","authMode":"bearer"}`))
	sec := jsonx.NewMap[string]()
	sec.Set("token", "zkg_secret")
	_, err := connectors.Upsert(d, connectors.UpsertInput{ID: "mcp-vorneo", Manifest: mcpconn.Manifest.Row(), Config: cfg, Secrets: sec}, KEY, 1)
	testutil.Must(t, err)
	set := remotemcp.Tools(remotemcp.Deps{DB: d, MasterKey: KEY, Resolver: public})
	list := conntest.Call(t, set, "list_mcp_servers", `{}`)
	if strings.Contains(list.String(), "zkg_secret") || list.S("servers.0.url") != "https://vorneo.id/api/mcp/" {
		t.Fatal(list)
	}
	if conntest.Call(t, set, "remove_mcp_server", `{"name":"mcp-vorneo"}`).B("ok") {
		t.Fatal("removed")
	}
	if _, found, _ := connectors.Get(d, "mcp-vorneo"); !found {
		t.Fatal("gone")
	}
}

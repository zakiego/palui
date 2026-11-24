package registry_test

import (
	"testing"

	"context"
	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	attachmentsconn "github.com/zakiego/palui/apps/server/internal/connectors/attachments"
	chatsconn "github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	flowsconn "github.com/zakiego/palui/apps/server/internal/connectors/flows"
	gcalconn "github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	hooksconn "github.com/zakiego/palui/apps/server/internal/connectors/hooks"
	"github.com/zakiego/palui/apps/server/internal/connectors/httpreq"
	icsconn "github.com/zakiego/palui/apps/server/internal/connectors/ics"
	jobsconn "github.com/zakiego/palui/apps/server/internal/connectors/jobs"
	notesconn "github.com/zakiego/palui/apps/server/internal/connectors/notes"
	outboundconn "github.com/zakiego/palui/apps/server/internal/connectors/outbound"
	printerconn "github.com/zakiego/palui/apps/server/internal/connectors/printer"
	proactiveconn "github.com/zakiego/palui/apps/server/internal/connectors/proactive"
	recallconn "github.com/zakiego/palui/apps/server/internal/connectors/recall"
	remindersconn "github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/connectors/remotemcp"
	settingsconn "github.com/zakiego/palui/apps/server/internal/connectors/settings"
	t3conn "github.com/zakiego/palui/apps/server/internal/connectors/t3code"
	webfetchconn "github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const WITA = "Asia/Makassar"

type catalogEnv struct {
	db      *db.DB
	chatID  string
	agentID string
	owner   connectors.TurnInfo
	now     int64
}

func newCatalogEnv(t *testing.T, now int64) *catalogEnv {
	d := testutil.DB(t)
	c, err := chats.EnsureWebChat(d, now)
	testutil.Must(t, err)
	a, err := agents.Create(d, agents.Input{Slug: "kerja", Name: testutil.Ptr("Kerja"), Persona: testutil.Ptr("Asisten kerja.")}, now)
	testutil.Must(t, err)
	owner := connectors.TurnInfo{RunID: "run_golden", ChatID: c.ID, Channel: "web", Trigger: "message", TZ: WITA, Now: now, Origin: "message", Mode: "owner", Tainted: func() bool { return false }, OwnerText: testutil.Ptr("halo")}
	return &catalogEnv{db: d, chatID: c.ID, agentID: a.ID, owner: owner, now: now}
}

// catalogBuilders builds every golden catalog variant: connector -> variant -> tools.
func catalogBuilders(t *testing.T, e *catalogEnv) map[string]map[string]*connectors.ToolSet {
	must := func(s *connectors.ToolSet, err error) *connectors.ToolSet {
		testutil.Must(t, err)
		return s
	}
	clock := func() int64 { return e.now }
	started := func(rt *connectors.Runtime) *connectors.ToolSet {
		h, err := rt.Start(conntest.Ctx(t, e.db, "x", nil, nil))
		testutil.Must(t, err)
		set, err := h.Tools(context.Background(), e.owner)
		testutil.Must(t, err)
		return set
	}
	with := func(pairs ...any) *connectors.ToolSet { return conntest.Tools(pairs...) }
	unattended := e.owner
	unattended.Origin = "schedule"
	flowTurn := e.owner
	flowTurn.Trigger, flowTurn.FlowID = "flow", "flw_x"
	owner := flowsconn.Tools(e.db, e.owner, flowsconn.Deps{Clock: clock})
	owner.Set("recap", flowsconn.RecapTool(e.db, e.owner, clock))
	api := recallconn.NewAPI("http://r", "rcl_x", nil)
	tokens := &gcalconn.TokenCache{ClientID: "c", ClientSecret: "s", RefreshToken: "r"}
	gcalCfg := gcalconn.Config{CalendarIDs: []string{"primary"}}
	t3 := func(cfg *jsonx.Object, untrusted bool) *connectors.ToolSet {
		w := t3conn.NewWatcher(conntest.Ctx(t, e.db, "t3code", cfg, nil), nil, nil)
		return t3conn.Tools(t3conn.ToolsInput{Client: nil, Config: t3conn.ParseConfig(cfg), Turn: e.owner, Watcher: w, Untrusted: untrusted})
	}
	httpCfg := func(policy string) httpreq.Config {
		return httpreq.Config{Domains: []httpreq.Domain{{Host: "api.example.com"}}, Auth: "header", HeaderName: "Authorization", ValueTemplate: "Bearer {value}", Policy: policy}
	}
	return map[string]map[string]*connectors.ToolSet{
		"http": {
			"ask":   httpreq.Tools(httpCfg("ask"), "Example", "", httpreq.Options{}),
			"allow": httpreq.Tools(httpCfg("allow"), "Example", "", httpreq.Options{}),
		},
		"attachments": {"default": attachmentsconn.Tools(e.db, e.owner, attachmentsconn.Options{Clock: clock})},
		"chats":       {"default": must(chatsconn.Tools(e.db, e.owner, clock))},
		"flows":       {"owner": owner, "flow": with("recap", flowsconn.RecapTool(e.db, flowTurn, clock))},
		"hooks":       {"default": started(hooksconn.Runtime)},
		"jobs":        {"default": jobsconn.Tools(e.db, clock, "")},
		"proactive":   {"default": proactiveconn.Tools(e.db, clock, e.owner)},
		"notes":       {"default": must(notesconn.Tools(e.db, clock, "")), "agent": must(notesconn.Tools(e.db, clock, e.agentID))},
		"printer":     {"default": printerconn.Tools(e.db, e.owner, printerconn.Config{URL: "ipp://host.docker.internal:631/printers/My_Printer"}, printerconn.Options{})},
		"outbound":    {"owner": must(outboundconn.Tools(e.db, e.owner, clock)), "unattended": must(outboundconn.Tools(e.db, unattended, clock))},
		"reminders":   {"default": remindersconn.Tools(e.db, e.owner, clock)},
		"remote-mcp":  {"default": remotemcp.Tools(remotemcp.Deps{DB: e.db, MasterKey: testutil.TestKey, Clock: clock})},
		"settings":    {"default": must(settingsconn.Tools(e.db, clock, "")), "agent": must(settingsconn.Tools(e.db, clock, e.agentID))},
		"webfetch":    {"default": started(webfetchconn.Runtime)},
		"recall": {"rest": with(
			"forget_memory", connectors.PaluiTool{Tool: recallconn.ForgetTool(api, nil), Class: "destructive", Title: "Forget memory"},
			"forget_document", connectors.PaluiTool{Tool: recallconn.ForgetDocumentTool(api, nil), Class: "destructive", Title: "Delete document"},
		)},
		"ics":    {"default": icsconn.Tools(icsconn.NewFeed("https://calendar.example.com/private-abc/basic.ics", nil, icsconn.Options{}), e.owner, icsconn.Options{})},
		"gcal":   {"default": with("get_calendar", connectors.PaluiTool{Tool: gcalconn.CalendarTool(tokens, gcalCfg, e.owner, nil, clock), Class: "read", Untrusted: true, Title: "Check the calendar"})},
		"t3code": {"default": t3(nil, false), "custom": t3(jsonx.NewMap[any]().Assign(kitObj("baseRef", "develop")), true)},
	}
}

func kitObj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func TestToolCatalogsMatchTheTSBuild(t *testing.T) {
	g := loadGolden(t)
	e := newCatalogEnv(t, g.Now)
	built := catalogBuilders(t, e)
	for connector, variants := range g.Catalogs {
		have, ok := built[connector]
		if !ok {
			t.Errorf("no Go catalog for %s", connector)
			continue
		}
		for _, v := range variants {
			set, ok := have[v.Variant]
			if !ok {
				t.Errorf("no Go catalog for %s/%s", connector, v.Variant)
				continue
			}
			compareCatalog(t, connector+"/"+v.Variant, v.Tools, describeTools(set))
		}
	}
}

func TestToolInputsParseLikeTheAISDK(t *testing.T) {
	g := loadGolden(t)
	e := newCatalogEnv(t, g.Now)
	built := catalogBuilders(t, e)
	for connector, perTool := range g.Inputs {
		have, ok := built[connector]
		if !ok {
			t.Errorf("no Go tools for %s", connector)
			continue
		}
		var set *connectors.ToolSet
		for _, s := range have {
			set = s
			break
		}
		if s, ok := have["default"]; ok {
			set = s
		} else if s, ok := have["owner"]; ok {
			set = s
		} else if s, ok := have["rest"]; ok {
			set = s
		}
		for name, cases := range perTool {
			pt, ok := set.Get(name)
			if !ok {
				t.Errorf("%s: no tool %s", connector, name)
				continue
			}
			for _, c := range cases {
				v, err := jsonx.Parse(c.Input)
				testutil.Must(t, err)
				out, perr := pt.Tool.Parse(v)
				label := connector + "." + name + " " + string(c.Input)
				if c.Error != nil {
					if perr == nil {
						t.Errorf("%s: want error, got %s", label, jsonx.Stringify(out))
						continue
					}
					got := chat.ErrorString(&chat.InvalidToolInputError{ToolName: name, Cause: perr})
					if got != *c.Error {
						t.Errorf("%s:\n want %s\n got  %s\n %s", label, *c.Error, got, testutil.DiffAt(*c.Error, got))
					}
					continue
				}
				if perr != nil {
					t.Errorf("%s: unexpected error %v", label, perr)
					continue
				}
				if want, got := ordered(t, c.Parsed), jsonx.Stringify(out); want != got {
					t.Errorf("%s: parsed\n want %s\n got  %s", label, want, got)
				}
			}
		}
	}
}

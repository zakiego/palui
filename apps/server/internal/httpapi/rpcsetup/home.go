package rpcsetup

// apps/app/src/lib/fns/home.ts: the shell (status, command menu data) and the
// Home page.

import (
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/manage"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/settings"
)

type shellConnector struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind"`
}

func getShell(c *httpapi.Call) (any, error) {
	q := c.DB()
	s, err := settings.All(q)
	if err != nil {
		return nil, err
	}
	summary, err := httpapi.GetStatusSummary(q)
	if err != nil {
		return nil, err
	}
	items, err := manage.ListItems(q, c.MasterKey())
	if err != nil {
		return nil, err
	}
	list := make([]shellConnector, 0, len(items))
	for _, it := range items {
		list = append(list, shellConnector{it.ID, it.Label, it.Status, it.Enabled, it.Kind})
	}
	return struct {
		Viewer     any                   `json:"viewer"`
		Summary    httpapi.StatusSummary `json:"summary"`
		Connectors []shellConnector      `json:"connectors"`
		UILanguage string                `json:"uiLanguage"`
		Timezone   string                `json:"timezone"`
	}{c.Viewer, summary, list, s.UILanguage, s.Timezone}, nil
}

type checklist struct {
	Passkey   bool `json:"passkey"`
	Model     bool `json:"model"`
	Channel   bool `json:"channel"`
	Hi        bool `json:"hi"`
	Recall    bool `json:"recall"`
	MCP       bool `json:"mcp"`
	Dismissed bool `json:"dismissed"`
}

type homeConnector struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	Status       string  `json:"status"`
	StatusDetail *string `json:"statusDetail"`
	Enabled      bool    `json:"enabled"`
}

func getHome(c *httpapi.Call) (any, error) {
	q, now := c.DB(), c.Now()
	s, err := settings.All(q)
	if err != nil {
		return nil, err
	}
	items, err := manage.ListItems(q, c.MasterKey())
	if err != nil {
		return nil, err
	}
	f, err := firstRunFacts(q)
	if err != nil {
		return nil, err
	}
	live := func(it manage.Item) bool { return it.Status == "on" || it.Status == "connecting" }
	var cl checklist
	cl.Passkey = f.passkeys > 0
	cl.Hi = s.FirstRun.SaidHi || f.userMessages > 0
	cl.Dismissed = s.FirstRun.Dismissed
	conns := make([]homeConnector, 0, len(items))
	for _, it := range items {
		cl.Model = cl.Model || (it.Kind == "model" && live(it))
		cl.Channel = cl.Channel || (it.Kind == "channel" && it.Type != "web" && live(it))
		cl.Recall = cl.Recall || (it.Type == "recall" && it.Status != "not_set_up")
		cl.MCP = cl.MCP || (it.Type == "mcp" && it.Status != "not_set_up")
		conns = append(conns, homeConnector{it.ID, it.Type, it.Kind, it.Label, it.Status, it.StatusDetail, it.Enabled})
	}
	_, up := runtimeUp(c)
	reminders, err := nextReminders(q, 3, now)
	if err != nil {
		return nil, err
	}
	runs, err := recentRuns(q, 5)
	if err != nil {
		return nil, err
	}
	toks, err := tokensSince(q, startOfDay(s.Timezone, now))
	if err != nil {
		return nil, err
	}
	// Tokens per day for the last 7 days, today last.
	week, err := usage(q, s.Timezone, 7, now)
	if err != nil {
		return nil, err
	}
	web, err := chats.EnsureWebChat(q, now)
	if err != nil {
		return nil, err
	}
	return struct {
		Checklist  checklist       `json:"checklist"`
		Connectors []homeConnector `json:"connectors"`
		Reminders  []nextReminder  `json:"reminders"`
		Runs       []runItem       `json:"runs"`
		Tokens     tokens          `json:"tokens"`
		Week       []usageDay      `json:"week"`
		Timezone   string          `json:"timezone"`
		WebChatID  string          `json:"webChatId"`
		RuntimeUp  bool            `json:"runtimeUp"`
		AppURL     string          `json:"appUrl"`
	}{cl, conns, reminders, runs, toks, week.Days, s.Timezone, web.ID, up, c.AppURL()}, nil
}

func dismissChecklist(c *httpapi.Call) (any, error) {
	v, err := settingValue(c.DB(), "firstRun")
	if err != nil {
		return nil, err
	}
	next := jsonx.NewMap[any]()
	if o, ok := v.(*jsonx.Object); ok {
		next = o.Clone()
	}
	next.Set("dismissed", true)
	if err := settings.Set(c.DB(), "firstRun", next, c.Now()); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func getStatusSummary(c *httpapi.Call) (any, error) {
	return httpapi.GetStatusSummary(c.DB())
}

func init() {
	httpapi.Register(httpapi.Handler{Name: "getShell", Method: http.MethodGet, Guard: httpapi.User, Fn: getShell})
	httpapi.Register(httpapi.Handler{Name: "getHome", Method: http.MethodGet, Guard: httpapi.User, Fn: getHome})
	httpapi.Register(httpapi.Handler{Name: "dismissChecklist", Method: http.MethodPost, Guard: httpapi.Writer, Fn: dismissChecklist})
	httpapi.Register(httpapi.Handler{Name: "getStatusSummary", Method: http.MethodGet, Guard: httpapi.User, Fn: getStatusSummary})
}

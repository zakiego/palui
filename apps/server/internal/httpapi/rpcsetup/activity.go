package rpcsetup

// apps/app/src/lib/fns/activity.ts: the Activity page (events, runs, usage).

import (
	"net/http"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

type connectorLabel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func getEvents(c *httpapi.Call) (any, error) {
	q := c.DB()
	var in struct {
		ConnectorID string   `json:"connectorId"`
		Levels      []string `json:"levels"`
	}
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	f := eventFilter{connectorID: in.ConnectorID, levels: in.Levels}
	list, err := eventList(q, f, 300)
	if err != nil {
		return nil, err
	}
	total, err := eventCount(q, f)
	if err != nil {
		return nil, err
	}
	rows, err := connectors.List(q)
	if err != nil {
		return nil, err
	}
	labels := make([]connectorLabel, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, connectorLabel{r.ID, r.Label})
	}
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	return struct {
		Events     []eventItem      `json:"events"`
		Total      int64            `json:"total"`
		Connectors []connectorLabel `json:"connectors"`
		Timezone   string           `json:"timezone"`
	}{list, total, labels, tz}, nil
}

func getRuns(c *httpapi.Call) (any, error) {
	q := c.DB()
	runs, err := recentRuns(q, 100)
	if err != nil {
		return nil, err
	}
	total, err := runCount(q)
	if err != nil {
		return nil, err
	}
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	return struct {
		Runs     []runItem `json:"runs"`
		Total    int64     `json:"total"`
		Timezone string    `json:"timezone"`
	}{runs, total, tz}, nil
}

func getRun(c *httpapi.Call) (any, error) {
	q, id := c.DB(), str(c, "id")
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	type out struct {
		Run      *runDetailView `json:"run"`
		FlowRun  *flowRunView   `json:"flowRun"`
		Timezone string         `json:"timezone"`
	}
	// A flow run (flr_...) shows its step log on the same page.
	if strings.HasPrefix(id, "flr_") {
		fr, err := flowRunDetail(q, id)
		if err != nil {
			return nil, err
		}
		return out{nil, fr, tz}, nil
	}
	r, err := runDetail(q, id)
	if err != nil {
		return nil, err
	}
	return out{r, nil, tz}, nil
}

func getUsage(c *httpapi.Call) (any, error) {
	q := c.DB()
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	u, err := usage(q, tz, 14, c.Now())
	if err != nil {
		return nil, err
	}
	return struct {
		usageView
		Timezone string `json:"timezone"`
	}{u, tz}, nil
}

func init() {
	level := zod.Enum("debug", "info", "warn", "error")
	httpapi.Register(httpapi.Handler{Name: "getEvents", Method: http.MethodGet, Guard: httpapi.User, Input: zod.Object(
		zod.F("connectorId", zod.String().Max(64).Optional()),
		zod.F("levels", zod.Array(level).Max(4).Optional()),
	), Fn: getEvents})
	httpapi.Register(httpapi.Handler{Name: "getRuns", Method: http.MethodGet, Guard: httpapi.User, Fn: getRuns})
	httpapi.Register(httpapi.Handler{Name: "getRun", Method: http.MethodGet, Guard: httpapi.User, Input: zod.Object(zod.F("id", zod.String().Max(64))), Fn: getRun})
	httpapi.Register(httpapi.Handler{Name: "getUsage", Method: http.MethodGet, Guard: httpapi.User, Fn: getUsage})
}

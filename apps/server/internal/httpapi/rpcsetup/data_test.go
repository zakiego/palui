package rpcsetup_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// setConnectorDataFn sets and clears the owner's no-private-data mark on an
// MCP server, logs it, and refuses anything that isn't an MCP server.
func TestSetConnectorDataMarksAnMCPServer(t *testing.T) {
	e := apitest.New(t, nil)
	token := e.SignIn()
	cfg := jsonx.NewMap[any]()
	cfg.Set("url", "https://prayer.example.com/mcp")
	testutil.SeedConnector(t, e.DB, "mcp-prayer", "tools", testutil.SeedOptions{Type: "mcp", Config: cfg}, 1)
	testutil.SeedConnector(t, e.DB, "gcal", "tools", testutil.SeedOptions{}, 1)
	before, _, _ := connectors.Get(e.DB, "mcp-prayer")

	if status, body := e.Post(token, "setConnectorDataFn", `{"id":"mcp-prayer","noPrivateData":true}`); status != 200 || body != `{"ok":true}` {
		t.Fatal(status, body)
	}
	row, _, _ := connectors.Get(e.DB, "mcp-prayer")
	if !connectors.NoPrivateData(row) || row.ConfigHash != before.ConfigHash {
		t.Fatal(row.Config, row.ConfigHash, before.ConfigHash)
	}
	if u, _ := row.Config.Get("url"); u != "https://prayer.example.com/mcp" {
		t.Fatal(row.Config)
	}
	var level, msg string
	testutil.Must(t, e.DB.QueryRow("SELECT level, message FROM events WHERE kind = 'mcp.data' AND connector_id = 'mcp-prayer' ORDER BY id DESC LIMIT 1").Scan(&level, &msg))
	if level != "warn" || msg != "Marked as holding no private data: shared chats can use its tools" {
		t.Fatal(level, msg)
	}

	if status, body := e.Post(token, "setConnectorDataFn", `{"id":"mcp-prayer","noPrivateData":false}`); status != 200 || body != `{"ok":true}` {
		t.Fatal(status, body)
	}
	row, _, _ = connectors.Get(e.DB, "mcp-prayer")
	if _, has := row.Config.Get(connectors.NoPrivateDataKey); has || row.ConfigHash != before.ConfigHash {
		t.Fatal(row.Config)
	}
	testutil.Must(t, e.DB.QueryRow("SELECT level, message FROM events WHERE kind = 'mcp.data' AND connector_id = 'mcp-prayer' ORDER BY id DESC LIMIT 1").Scan(&level, &msg))
	if level != "info" || msg != "No longer marked as holding no private data" {
		t.Fatal(level, msg)
	}

	if status, body := e.Post(token, "setConnectorDataFn", `{"id":"gcal","noPrivateData":true}`); status != 400 || body != `{"error":"Only an MCP server can be marked as holding no private data.","code":"BAD_REQUEST"}` {
		t.Fatal(status, body)
	}
	if g, _, _ := connectors.Get(e.DB, "gcal"); g.Config != nil {
		if _, has := g.Config.Get(connectors.NoPrivateDataKey); has {
			t.Fatal(g.Config)
		}
	}
	if status, body := e.Post(token, "setConnectorDataFn", `{"id":"mcp-gone","noPrivateData":true}`); status != 400 || body != `{"error":"That connector doesn't exist anymore.","code":"BAD_REQUEST"}` {
		t.Fatal(status, body)
	}
	// Only a boolean: a string is a validation error, not a mark.
	if status, _ := e.Post(token, "setConnectorDataFn", `{"id":"mcp-prayer","noPrivateData":"true"}`); status != 400 {
		t.Fatal(status)
	}
	if row, _, _ = connectors.Get(e.DB, "mcp-prayer"); connectors.NoPrivateData(row) {
		t.Fatal(row.Config)
	}
	if status, _ := e.Post("", "setConnectorDataFn", `{"id":"mcp-prayer","noPrivateData":true}`); status != 401 {
		t.Fatal(status)
	}
}

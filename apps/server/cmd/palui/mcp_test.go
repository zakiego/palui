package main

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/mcpaccess"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestCLIMCPTokenListAndRevoke(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	if h.run("mcp", "token", "laptop") != 1 || !strings.Contains(h.err.String(), "owner invite") {
		t.Fatalf("no owner: %s", h.err.String())
	}
	inv, err := auth.CreateOwnerInvite(h.d, "https://palui.example.com", 1)
	testutil.Must(t, err)
	_, err = auth.AcceptOwnerInvite(h.d, inv.Token, 1)
	testutil.Must(t, err)

	if h.run("mcp", "token", "mcporter", "on", "mini") != 0 {
		t.Fatal(h.err.String())
	}
	token := last(h.out)
	if !strings.HasPrefix(token, "plt_") || !strings.Contains(h.err.String(), "https://palui.example.com/mcp") {
		t.Fatalf("%q %s", token, h.err.String())
	}
	c, err := mcpaccess.Verify(h.d, token, 2)
	testutil.Must(t, err)
	if c == nil || c.Grant.Name != "mcporter on mini" {
		t.Fatalf("token doesn't verify: %+v", c)
	}

	if h.run("mcp", "list") != 0 || !strings.Contains(last(h.out), "mcporter on mini (..."+token[len(token)-4:]+"), never used") {
		t.Fatal(h.out.String())
	}
	if h.run("mcp", "revoke", c.Grant.ID) != 0 {
		t.Fatal(h.err.String())
	}
	if c, _ := mcpaccess.Verify(h.d, token, 3); c != nil {
		t.Fatal("revoked token still verifies")
	}
	if h.run("mcp", "revoke", c.Grant.ID) != 1 {
		t.Fatal("revoking twice succeeded")
	}
}

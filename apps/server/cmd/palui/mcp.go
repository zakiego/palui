package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/mcpaccess"
)

// runMCP is `palui mcp token|list|revoke`: personal tokens for the /mcp
// server without the dashboard (a headless box, or an agent setting itself up).
func runMCP(sub string, rest []string, asJSON bool, o *cliIO) (int, error) {
	d, err := o.db()
	if err != nil {
		return 1, err
	}
	owner, ok, err := auth.FindOwner(d)
	if err != nil {
		return 1, err
	}
	if !ok {
		o.eprintln("No owner yet. Run palui owner invite and sign in first.")
		return 1, nil
	}
	url := mcpaccess.ResourceURL(o.appURL())
	switch sub {
	case "token":
		name := strings.TrimSpace(strings.Join(rest, " "))
		if name == "" {
			o.eprintln("usage: palui mcp token <name>   (name it after the machine or agent that uses it)")
			return 1, nil
		}
		token, g, err := mcpaccess.CreateToken(d, owner.ID, name, o.now())
		var me *mcpaccess.Error
		if errors.As(err, &me) {
			o.eprintln(me.Message)
			return 1, nil
		}
		if err != nil {
			return 1, err
		}
		if asJSON {
			o.println(jsonLine(ordered("id", g.ID, "name", g.Name, "token", token, "url", url)))
		} else {
			o.println(token)
			o.eprintln(fmt.Sprintf("Shown once. Send it as Authorization: Bearer <token> to %s", url))
		}
		return 0, nil
	case "list":
		grants, err := mcpaccess.List(d, owner.ID, o.now())
		if err != nil {
			return 1, err
		}
		if asJSON {
			o.println(jsonLine(grants))
			return 0, nil
		}
		if len(grants) == 0 {
			o.println("no MCP tokens or connections yet: palui mcp token <name>")
			return 0, nil
		}
		for _, g := range grants {
			line := padEnd(g.ID, 30) + " " + padEnd(g.Kind, 6) + " " + g.Name
			if g.TokenHint != nil {
				line += " (..." + *g.TokenHint + ")"
			}
			if g.LastUsedAt != nil {
				line += ", last used " + time.UnixMilli(*g.LastUsedAt).UTC().Format("2006-01-02 15:04 UTC")
			} else {
				line += ", never used"
			}
			o.println(line)
		}
		return 0, nil
	case "revoke":
		if len(rest) != 1 {
			o.eprintln("usage: palui mcp revoke <id>   (ids are in palui mcp list)")
			return 1, nil
		}
		if err := mcpaccess.Revoke(d, owner.ID, rest[0]); errors.Is(err, mcpaccess.ErrNotFound) {
			o.eprintln(fmt.Sprintf("no token or connection %q", rest[0]))
			return 1, nil
		} else if err != nil {
			return 1, err
		}
		o.eprintln("Revoked. It stops working now.")
		return 0, nil
	}
	o.eprintln("usage: palui mcp token <name> | mcp list | mcp revoke <id>")
	return 1, nil
}

package main

// `palui dev ...`: dev-only commands for the dashboard's fixture data.
//
//	palui dev seed [demo|worst|empty|one|huge] [--force]
//	    apps/app/scripts/seed-demo.ts: fill $DATA_DIR/palui.db with a fixture
//	    (internal/fixtures), replacing whatever product data is there. Refuses
//	    in prod mode, and refuses a database with data that didn't come from a
//	    fixture unless --force is given.
//	palui dev serve [serve flags]
//	    `palui serve` with stand-in connector runtimes (the replacement for
//	    apps/app/scripts/demo-runtime.ts): connectors keep the status the
//	    fixture stored and are never really started, so no token or network is
//	    used. Refuses in prod mode.

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/fixtures"
	"github.com/zakiego/palui/apps/server/internal/serve"
)

const devUsage = `usage: palui dev <command>
  seed [demo|worst|empty|one|huge] [--force]
                                fill $DATA_DIR/palui.db with a dashboard fixture (default demo)
  serve [serve flags]           palui serve with stand-in connectors (never started, no network)`

// devSettle is how long a resumed stand-in connector stays connecting.
const devSettle = 1200 * time.Millisecond

func runDev(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, devUsage)
		return 2
	}
	switch args[0] {
	case "seed":
		return devSeed(args[1:], config.Load(getenv), stdout, stderr)
	case "serve":
		o, err := serveFlags(args[1:], getenv, stderr)
		if err != nil {
			return 2
		}
		return devServe(o, stderr)
	}
	fmt.Fprintf(stderr, "unknown dev command %q\n%s\n", args[0], devUsage)
	return 2
}

func devSeed(args []string, cfg config.Config, stdout, stderr io.Writer) int {
	if cfg.Mode == "prod" {
		fmt.Fprintln(stderr, "seed-demo is for dev databases only")
		return 1
	}
	force := slices.Contains(args, "--force")
	wanted := "demo"
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			wanted = a
			break
		}
	}
	if !fixtures.Valid(wanted) {
		fmt.Fprintf(stderr, "Unknown fixture %q. Use one of: %s\n", wanted, strings.Join(fixtures.Names, ", "))
		return 1
	}
	key, err := config.MasterKey(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	file := db.FilePath(cfg.DataDir)
	d, err := db.Open(file, db.OpenOptions{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer d.Close()
	if err := fixtures.Apply(d, wanted, fixtures.Options{Force: force, MasterKey: key}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "seeded %s with the %q fixture\n", file, wanted)
	return 0
}

func devServe(o serve.Options, stderr io.Writer) int {
	if o.Config.Mode == "prod" {
		fmt.Fprintln(stderr, "palui dev serve is for dev databases only")
		return 1
	}
	if err := os.MkdirAll(o.Config.DataDir, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	d, err := db.Open(db.FilePath(o.Config.DataDir), db.OpenOptions{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer d.Close()
	manifests := registry.Manifests()
	reg, err := fixtures.StandIns(d, manifests, devSettle)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	o.DB, o.Registry, o.Manifests = d, reg, manifests
	return serve.Run(o, nil)
}

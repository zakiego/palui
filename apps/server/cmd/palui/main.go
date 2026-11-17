// Command palui is the Palui Agent server: `palui serve` runs everything in
// one process, and the admin commands (`owner invite`, `connectors ...`,
// `backup`, `secrets-check`, `hook create`, `mcp ...`) work on the same database.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	_ "time/tzdata"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders/eval"
	"github.com/zakiego/palui/apps/server/internal/serve"
	"github.com/zakiego/palui/apps/server/internal/version"
)

// serveFlags parses `palui serve` flags. Settings come from the environment
// (DATA_DIR, MASTER_KEY, APP_URL, HOST, PORT, NODE_ENV, PALUI_TZ,
// PALUI_WEB_DIR); --host, --port and --web-dir override HOST, PORT and
// PALUI_WEB_DIR, and --data-dir and --app-url override DATA_DIR and APP_URL.
func serveFlags(args []string, getenv func(string) string, stderr io.Writer) (serve.Options, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host, port := serve.ListenAddr(getenv)
	h := fs.String("host", host, "listen host (HOST)")
	p := fs.String("port", port, "listen port (PORT)")
	webDir := fs.String("web-dir", getenv("PALUI_WEB_DIR"), "serve the dashboard from this directory instead of the embedded build (PALUI_WEB_DIR)")
	dataDir := fs.String("data-dir", "", "data directory (DATA_DIR)")
	appURL := fs.String("app-url", "", "public URL of the dashboard (APP_URL)")
	noRuntime := fs.Bool("no-runtime", runtimeOffEnv(getenv("PALUI_NO_RUNTIME")), "serve without the runtime: no connectors, engine, scheduler or outbox (PALUI_NO_RUNTIME=1)")
	if err := fs.Parse(args); err != nil {
		return serve.Options{}, err
	}
	cfg := config.Load(getenv)
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *appURL != "" {
		cfg.AppURL = config.Load(func(k string) string {
			if k == "APP_URL" {
				return *appURL
			}
			return ""
		}).AppURL
	}
	config.Set(&cfg)
	return serve.Options{Config: cfg, Host: *h, Port: *p, WebDir: *webDir, RuntimeOff: *noRuntime}, nil
}

// envIO is the admin commands' io on the real environment.
func envIO(stdout, stderr io.Writer) (*cliIO, func()) {
	cfg := config.Get()
	var opened *db.DB
	_, port := serve.ListenAddr(os.Getenv)
	o := &cliIO{
		db: func() (*db.DB, error) {
			if opened == nil {
				d, err := db.Open(db.FilePath(cfg.DataDir), db.OpenOptions{})
				if err != nil {
					return nil, err
				}
				opened = d
			}
			return opened, nil
		},
		poke: func() bool {
			token, err := config.AdminToken(cfg)
			if err != nil {
				return false
			}
			return pokeServer(port, token)
		},
		appURL:    func() string { return cfg.AppURL },
		masterKey: func() (string, error) { return config.MasterKey(cfg) },
		dbFile:    func() string { return db.FilePath(cfg.DataDir) },
		out:       stdout,
		err:       stderr,
		readStdin: func() (string, error) {
			b, err := io.ReadAll(os.Stdin)
			return string(b), err
		},
	}
	return o, func() {
		if opened != nil {
			opened.Close()
		}
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		line := "palui " + version.Version
		if info := version.LoadBuildInfoFromEnv(); info != nil {
			line += " (" + version.ShortSha(info.Commit) + ")"
		}
		fmt.Fprintln(stdout, line)
		return 0
	case "eval-reminders":
		// The 60-phrase reminder parse eval, live against CLIProxyAPI (M4).
		// Opt-in: it makes real model calls, so no test runs it.
		return eval.Main(context.Background(), os.Getenv, stdout, stderr)
	case "serve":
		o, err := serveFlags(args[1:], os.Getenv, stderr)
		if err != nil {
			return 2
		}
		return serve.Run(o, nil)
	case "healthcheck":
		return runHealthcheck(args[1:], os.Getenv, stderr)
	case "snapshot":
		return runSnapshot(args[1:], stdout, stderr)
	case "owner", "connectors", "backup", "secrets-check", "hook", "mcp", "help", "--help", "-h":
		o, done := envIO(stdout, stderr)
		defer done()
		return runCLI(args, o)
	case "dev":
		return runDev(args[1:], os.Getenv, stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

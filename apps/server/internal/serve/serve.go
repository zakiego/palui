// Package serve is `palui serve`: one process that runs what used to be the
// app (TanStack Start: dashboard, REST, auth, migrations) and the runtime
// (channels, turns, scheduler, outbox, supervisor). Startup follows
// apps/runtime/src/main.ts and apps/app/src/server/db.ts combined; shutdown
// follows main.ts's stop() and index.ts's signal handling.
package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deploynotice"
	"github.com/zakiego/palui/apps/server/internal/deps"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcall"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/tools"
	"github.com/zakiego/palui/apps/server/internal/version"
	"github.com/zakiego/palui/apps/server/internal/web"
)

const (
	// DefaultHost and DefaultPort are what the image sets (HOST=0.0.0.0, PORT=3000).
	DefaultHost = "0.0.0.0"
	DefaultPort = "3000"
	// HardExitAfter ends the process if the ordered shutdown hangs, inside
	// compose's 60 s stop_grace_period: the engine drain takes up to 55 s.
	HardExitAfter = 58 * time.Second
	// httpShutdown is how long in-flight requests get once the streams closed.
	httpShutdown = 3 * time.Second
)

// Options configure a server. Zero values are the production defaults.
type Options struct {
	Config config.Config
	Log    *logx.Logger
	// Listen address. Ignored when Listener is set.
	Host string
	Port string
	// Tests listen on a random port.
	Listener net.Listener
	// Serve the dashboard from a directory instead of the embedded build.
	WebDir string
	// An open database (tests). Otherwise $DATA_DIR/palui.db, opened and migrated.
	DB *db.DB
	// Connector runtimes by type and manifests for builtins (tests use fakes).
	Registry  map[string]*connectors.Runtime
	Manifests []connectors.Manifest
	// The build to announce (default: PALUI_BUILD_INFO). NoBuild: none.
	Build   *version.BuildInfo
	NoBuild bool
	Clock   func() int64
	// Engine drain limit (default chat.DrainTimeoutMs).
	DrainTimeoutMs int64
	// httpapi stream timings (tests).
	SessionRecheck, Keepalive, EventPoll time.Duration
	// Run: the signals to wait for (tests; default SIGTERM and SIGINT) and
	// the hard exit timer (default HardExitAfter).
	Signals       <-chan os.Signal
	HardExitAfter time.Duration
	// RuntimeOff serves /health, auth, the RPC endpoints and the dashboard
	// but never builds the runtime: no connectors (so no channel session),
	// no engine, scheduler or outbox worker. The restore drill uses it on a
	// copy of production data (`palui serve --no-runtime`, PALUI_NO_RUNTIME).
	RuntimeOff bool
}

// App is a running server.
type App struct {
	DB      *db.DB
	Server  *httpapi.Server
	Runtime *httpapi.Runtime
	// The address it listens on (host:port).
	Addr string
	// Steps the shutdown ran, in order (tests check it).
	Stopped []string

	o        Options
	log      *logx.Logger
	http     *http.Server
	engine   *chat.Engine
	sup      *connectors.Supervisor
	worker   *outbox.Worker
	built    *deps.Built
	ownDB    bool
	stopOnce sync.Once
	served   chan struct{}
}

// Start brings the server up: database, runtime parts, HTTP listener (before
// connectors start, so /health answers early), builtins, then the worker,
// supervisor, engine and scheduler, then the deploy notice.
func Start(o Options) (*App, error) {
	log := o.Log
	if log == nil {
		log = logx.New(logx.Fields{"service": "palui"}, logx.StdoutSink(), logx.Info)
	}
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	cfg := o.Config
	startedAt := o.Clock()
	a := &App{o: o, log: log, served: make(chan struct{})}

	// The database. Only this process migrates now (the app's job in TS).
	d := o.DB
	if d == nil {
		var err error
		d, err = db.Open(db.FilePath(cfg.DataDir), db.OpenOptions{})
		if err != nil {
			return nil, fmt.Errorf("opening the database: %w", err)
		}
		a.ownDB = true
	}
	a.DB = d
	// Cleanup after the SQL migrations: drop rows of retired connector types. Never fatal.
	if _, err := connectors.RemoveRetired(d, o.Clock()); err != nil {
		log.Error("removing retired connectors failed", logx.Fields{"error": err})
	}
	schema, err := db.GetSchemaState(d)
	if err != nil {
		a.closeDB()
		return nil, err
	}
	key, err := config.MasterKey(cfg)
	if err != nil {
		a.closeDB()
		return nil, err
	}

	// The runtime, unless a newer build migrated the database: then only
	// /health (503) and the static dashboard answer, as when the TS runtime
	// refused to start next to an app that still served /health.
	runtimeOn := schema.OK && !o.RuntimeOff
	if !schema.OK {
		log.Error("database schema doesn't match this build; the runtime stays off", logx.Fields{"reason": schema.Reason, "unknown": schema.Unknown, "missing": schema.Missing})
	} else if o.RuntimeOff {
		log.Info("runtime off by request (--no-runtime): no connectors, engine, scheduler or outbox worker")
	} else {
		a.buildRuntime(d, key, startedAt)
	}

	webHandler := web.Handler(o.WebDir)
	srv, err := httpapi.New(httpapi.Options{
		DB: d, Config: cfg, MasterKey: key, Log: log.Child(logx.Fields{"component": "http"}), Clock: o.Clock,
		Runtime: a.Runtime, Web: webHandler, AdminToken: adminToken(key),
		SessionRecheck: o.SessionRecheck, Keepalive: o.Keepalive, EventPoll: o.EventPoll,
	})
	if err != nil {
		a.closeDB()
		return nil, err
	}
	a.Server = srv

	// The listener first, so /health answers while connectors come up.
	ln := o.Listener
	if ln == nil {
		host, port := o.Host, o.Port
		if host == "" {
			host = DefaultHost
		}
		if port == "" {
			port = DefaultPort
		}
		if ln, err = net.Listen("tcp", net.JoinHostPort(host, port)); err != nil {
			a.closeDB()
			return nil, err
		}
	}
	a.Addr = ln.Addr().String()
	a.http = &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		defer close(a.served)
		defer httpapi.Recover(log, "http server")
		if err := a.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server stopped", logx.Fields{"error": err})
		}
	}()
	log.Info("listening", logx.Fields{"addr": a.Addr, "schema": schema.Version, "version": version.Version})

	if !runtimeOn {
		return a, nil
	}
	manifests := o.Manifests
	if manifests == nil {
		manifests = registry.Manifests()
	}
	if err := a.sup.EnsureBuiltins(manifests); err != nil {
		log.Error("creating builtin connectors failed", logx.Fields{"error": err})
	}
	a.worker.Start()
	a.sup.Start()
	if err := a.engine.Start(); err != nil {
		log.Error("engine start failed", logx.Fields{"error": err})
	}
	if err := a.built.Scheduler.Start(); err != nil {
		log.Error("scheduler start failed", logx.Fields{"error": err})
	}
	// After the worker and channels are up, so the notice goes out right away.
	a.announce()
	return a, nil
}

func adminToken(masterKey string) string {
	t, err := secret.ControlToken(masterKey)
	if err != nil {
		return ""
	}
	return t
}

func (a *App) buildRuntime(d *db.DB, key string, startedAt int64) {
	o, log, cfg := a.o, a.log, a.o.Config
	reg := o.Registry
	if reg == nil {
		reg = registry.Runtimes()
	}
	services := &connectors.Services{}
	var engine *chat.Engine
	sup := connectors.NewSupervisor(connectors.SupervisorOptions{
		DB: d, Registry: reg, Services: services, Log: log.Child(logx.Fields{"component": "supervisor"}),
		MasterKey: key, DataDir: cfg.DataDir, Clock: o.Clock,
		InUse:     func(id string) bool { return engine != nil && engine.InUse(id) },
		OnRemoved: whatsapp.RemoveConnectorFiles(cfg.DataDir),
	})
	built := deps.Build(deps.Options{
		DB: d, Log: log,
		RunTurn: func(t chat.TurnOptions) (chat.TurnResult, error) { return engine.RunTurn(t) },
		Clock:   o.Clock,
		Running: sup.Running,
	})
	engine = chat.NewEngine(chat.EngineOptions{DB: d, Host: sup, Log: log.Child(logx.Fields{"component": "engine"}), Clock: o.Clock, Bus: chat.DefaultBus, Deps: built.Deps, AppURL: cfg.AppURL, DataDir: cfg.DataDir, MasterKey: key})
	*services = *engine.Services
	services.Reconcile = sup.Poke
	caller := tools.ConnectorToolCaller(sup.Running, tools.CallerOptions{Clock: o.Clock, TZ: func() string {
		tz, err := settings.Timezone(d)
		if err != nil {
			return "UTC"
		}
		return tz
	}})
	services.CallTool = func(ctx context.Context, connectorID, tool string, args *jsonx.Object) (any, error) {
		return caller(ctx, connectorID, tool, args, nil)
	}
	services.FlowTools = built.FlowTools
	worker := outbox.NewWorker(outbox.WorkerOptions{
		DB:      d,
		Channel: func(ch string) outbox.Channel { return sup.ChannelFor(ch) },
		Connector: func(id string) outbox.Channel {
			if e, ok := sup.Get(id); ok && e.Handle != nil && e.Handle.Channel != nil {
				return e.Handle.Channel
			}
			return nil
		},
		Log: log.Child(logx.Fields{"component": "outbox"}), Clock: o.Clock,
		OutboundFailed: deps.OutboundFailed(log),
	})
	a.sup, a.engine, a.built, a.worker = sup, engine, built, worker
	a.Runtime = &httpapi.Runtime{DB: d, Engine: engine, Sup: sup, Built: built, Bus: chat.DefaultBus, Log: log, StartedAt: startedAt, Clock: o.Clock}
}

func (a *App) announce() {
	defer httpapi.Recover(a.log, "deploy notice")
	build := a.o.Build
	if build == nil && !a.o.NoBuild {
		build = version.LoadBuildInfoFromEnv()
	}
	res, err := deploynotice.Announce(a.DB, version.Version, build, a.o.Clock())
	if err != nil {
		a.log.Error("deploy notice failed", logx.Fields{"error": err})
		return
	}
	if res.Sent {
		var commit any
		if build != nil {
			commit = build.Commit
		}
		a.log.Info("deploy notice queued", logx.Fields{"chats": len(res.ChatIDs), "skipped": res.Skipped, "commit": commit})
	}
}

func (a *App) closeDB() {
	if a.ownDB && a.DB != nil {
		a.DB.Close()
	}
}

func (a *App) step(name string, fn func()) {
	func() {
		defer httpapi.Recover(a.log, "shutdown: "+name)
		fn()
	}()
	a.Stopped = append(a.Stopped, name)
}

// Stop is main.ts stop(): stop taking turns and let the current step finish
// (up to 50 s), stop the scheduler, release outbox claims, stop connectors,
// close the HTTP server and SSE streams, close the database. Only the first
// call does anything.
func (a *App) Stop() {
	a.stopOnce.Do(func() {
		a.log.Info("draining")
		if a.engine != nil {
			a.step("engine", func() { a.engine.Stop(a.o.DrainTimeoutMs) })
			a.step("scheduler", func() { a.built.Scheduler.Stop() })
			a.step("worker", func() { a.worker.Stop() })
			a.step("supervisor", func() { a.sup.Stop() })
		}
		a.step("http", func() {
			ctx, cancel := context.WithTimeout(context.Background(), httpShutdown)
			defer cancel()
			done := make(chan struct{})
			go func() {
				a.Server.CloseStreams()
				close(done)
			}()
			if err := a.http.Shutdown(ctx); err != nil {
				_ = a.http.Close()
			}
			<-done
			<-a.served
		})
		a.step("database", a.closeDB)
		a.log.Info("stopped")
	})
}

// Run is `palui serve`: start, then wait for SIGTERM or SIGINT and shut
// down in order. The first signal wins; later ones are ignored. If the
// shutdown hangs, exit is called after HardExitAfter. It returns the exit code.
func Run(o Options, exit func(int)) int {
	if exit == nil {
		exit = os.Exit
	}
	log := o.Log
	if log == nil {
		log = logx.New(logx.Fields{"service": "palui"}, logx.StdoutSink(), logx.Info)
		o.Log = log
	}
	sigs := o.Signals
	if sigs == nil {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(ch)
		sigs = ch
	}
	hardExit := o.HardExitAfter
	if hardExit == 0 {
		hardExit = HardExitAfter
	}
	a, err := Start(o)
	if err != nil {
		log.Error("server failed to start", logx.Fields{"error": err})
		return 1
	}
	sig := <-sigs
	log.Info("signal received, shutting down", logx.Fields{"signal": signalName(sig)})
	go func() {
		for range sigs {
			// First signal wins.
		}
	}()
	timer := time.AfterFunc(hardExit, func() {
		log.Error("shutdown took too long, exiting", nil)
		exit(1)
	})
	defer timer.Stop()
	a.Stop()
	return 0
}

// signalName is the Node name of a signal ("SIGTERM").
func signalName(s os.Signal) string {
	switch s {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	}
	return strings.ToUpper(s.String())
}

// ListenAddr reads HOST and PORT the way the image sets them.
func ListenAddr(getenv func(string) string) (string, string) {
	host, port := getenv("HOST"), getenv("PORT")
	if host == "" {
		host = DefaultHost
	}
	if port == "" {
		port = DefaultPort
	}
	return host, port
}

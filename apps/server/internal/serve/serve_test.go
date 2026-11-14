package serve_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/serve"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func listener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.Must(t, err)
	return ln
}

func options(t *testing.T, d *db.DB) serve.Options {
	return serve.Options{
		Config:   config.Config{DataDir: t.TempDir(), AppURL: "http://localhost:4890", Mode: "dev", DefaultTz: "Asia/Makassar"},
		Log:      logx.Silent,
		DB:       d,
		Listener: listener(t),
		NoBuild:  true,
	}
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(url)
	testutil.Must(t, err)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res.StatusCode, m
}

func TestStartAnswersHealthAndStopsInOrder(t *testing.T) {
	a, err := serve.Start(options(t, testutil.DB(t)))
	testutil.Must(t, err)
	code, m := getJSON(t, "http://"+a.Addr+"/health")
	rt, _ := m["runtime"].(map[string]any)
	if code != 200 || m["ok"] != true || rt == nil || rt["ok"] != true {
		t.Fatalf("%d %v", code, m)
	}
	// The builtins exist and the web channel runs.
	var status string
	testutil.Must(t, a.DB.QueryRow("SELECT status FROM connectors WHERE id = 'web'").Scan(&status))
	if status != "on" {
		t.Fatal(status)
	}
	// The dashboard: / goes to /app, which serves the embedded build.
	res, err := http.Get("http://" + a.Addr + "/app/connectors")
	testutil.Must(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "Palui Agent") {
		t.Fatal(res.StatusCode)
	}
	a.Stop()
	a.Stop() // only the first call does anything
	want := []string{"engine", "scheduler", "worker", "supervisor", "http", "database"}
	if !reflect.DeepEqual(a.Stopped, want) {
		t.Fatalf("%v", a.Stopped)
	}
	if _, err := http.Get("http://" + a.Addr + "/health"); err == nil {
		t.Fatal("still listening")
	}
}

func TestANewerSchemaKeepsTheRuntimeOffButHealthAnswers503(t *testing.T) {
	d := testutil.DB(t)
	_, err := d.Exec("INSERT INTO _migrations (name, applied_at) VALUES ('9999_from_the_future', 1)")
	testutil.Must(t, err)
	a, err := serve.Start(options(t, d))
	testutil.Must(t, err)
	defer a.Stop()
	if a.Runtime != nil {
		t.Fatal("runtime started")
	}
	code, m := getJSON(t, "http://"+a.Addr+"/health")
	if code != 503 || m["error"] != "db_newer_than_app" {
		t.Fatalf("%d %v", code, m)
	}
	a.Stop()
	if !reflect.DeepEqual(a.Stopped, []string{"http", "database"}) {
		t.Fatal(a.Stopped)
	}
}

// RuntimeOff (the restore drill): /health is 200 and ok, the dashboard and
// the RPC framework answer, but nothing of the runtime is built, so no
// connector (and no channel session) ever starts.
func TestRuntimeOffServesHealthWithoutStartingConnectors(t *testing.T) {
	d := testutil.DB(t)
	o := options(t, d)
	o.RuntimeOff = true
	a, err := serve.Start(o)
	testutil.Must(t, err)
	defer a.Stop()
	if a.Runtime != nil {
		t.Fatal("runtime started")
	}
	code, m := getJSON(t, "http://"+a.Addr+"/health")
	if code != 200 || m["ok"] != true || m["runtime"] != "unreachable" {
		t.Fatalf("%d %v", code, m)
	}
	// EnsureBuiltins never ran: no connector rows at all on a fresh database.
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM connectors").Scan(&n))
	if n != 0 {
		t.Fatalf("%d connectors", n)
	}
	res, err := http.Get("http://" + a.Addr + "/app/")
	testutil.Must(t, err)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	a.Stop()
	if !reflect.DeepEqual(a.Stopped, []string{"http", "database"}) {
		t.Fatal(a.Stopped)
	}
}

func TestStartRefusesAProdServerWithoutAMasterKey(t *testing.T) {
	o := options(t, testutil.DB(t))
	o.Config.Mode = "prod"
	defer o.Listener.Close()
	if _, err := serve.Start(o); err != config.ErrNoMasterKey {
		t.Fatal(err)
	}
}

// The first signal wins; the hard exit timer fires when the shutdown is slow.
func TestRunShutsDownOnTheFirstSignal(t *testing.T) {
	o := options(t, testutil.DB(t))
	sigs := make(chan os.Signal, 3)
	o.Signals = sigs
	var mu sync.Mutex
	var exits []int
	o.HardExitAfter = time.Nanosecond
	addr := o.Listener.Addr().String()
	done := make(chan int)
	go func() {
		done <- serve.Run(o, func(code int) {
			mu.Lock()
			exits = append(exits, code)
			mu.Unlock()
		})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if res, err := http.Get("http://" + addr + "/health"); err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	sigs <- syscall.SIGTERM
	sigs <- syscall.SIGINT
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no shutdown")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("hard exit: %v", exits)
	}
}

func TestListenAddrDefaults(t *testing.T) {
	host, port := serve.ListenAddr(func(string) string { return "" })
	if host != "0.0.0.0" || port != "3000" {
		t.Fatal(host, port)
	}
	host, port = serve.ListenAddr(func(k string) string { return map[string]string{"HOST": "127.0.0.1", "PORT": "4895"}[k] })
	if host != "127.0.0.1" || port != "4895" {
		t.Fatal(host, port)
	}
}

// Every background task recovers its own panic and logs it.
func TestBackgroundPanicsAreRecoveredAndLogged(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	log := logx.New(nil, func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }, logx.Debug)
	httpapi.Go(log, "boom task", func() { panic("kaboom") })
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(lines)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no log line")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(lines[0], `"task":"boom task"`) || !strings.Contains(lines[0], "kaboom") || !strings.Contains(lines[0], "background task panicked") {
		t.Fatal(lines[0])
	}
}

// A connector whose Start panics doesn't take the server down.
func TestAPanickingConnectorLeavesTheServerUp(t *testing.T) {
	o := options(t, testutil.DB(t))
	boom := &connectors.Runtime{
		Manifest: connectors.Manifest{ID: "boom", Kind: "tools", Name: "Boom", Builtin: true},
		Start:    func(connectors.Context) (*connectors.RunningConnector, error) { panic("connector exploded") },
		Test:     func(connectors.Context) (connectors.TestResult, error) { return connectors.TestResult{OK: true}, nil },
	}
	o.Registry = map[string]*connectors.Runtime{"boom": boom}
	o.Manifests = []connectors.Manifest{boom.Manifest}
	a, err := serve.Start(o)
	testutil.Must(t, err)
	defer a.Stop()
	code, m := getJSON(t, "http://"+a.Addr+"/health")
	if code != 200 || m["ok"] != true {
		t.Fatal(code)
	}
	var status string
	testutil.Must(t, a.DB.QueryRow("SELECT status FROM connectors WHERE id = 'boom'").Scan(&status))
	if status != "error" {
		t.Fatal(status)
	}
}

package serve_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/serve"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// The real server on a random port against a copy of testdata/seeded.db
// (the TS-seeded `worst` fixture), with a fake Telegram: /health, the owner
// invite to a signed-in session, an SSE frame, and a hook delivery that ends
// in the outbox.
func TestEndToEndOnTheSeededDatabase(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/seeded.json")
	testutil.Must(t, err)
	var seeded struct {
		Golden struct {
			ChatID     string `json:"chatId"`
			HookSlug   string `json:"hookSlug"`
			HookSecret string `json:"hookSecret"`
			HookBody   string `json:"hookBody"`
		} `json:"golden"`
	}
	testutil.Must(t, json.Unmarshal(raw, &seeded))
	dir := t.TempDir()
	src, err := os.ReadFile("../../testdata/seeded.db")
	testutil.Must(t, err)
	testutil.Must(t, os.WriteFile(filepath.Join(dir, "palui.db"), src, 0o644))

	const app = "http://localhost:4890"
	telegram := conntest.NewFakeChannel("telegram")
	a, err := serve.Start(serve.Options{
		Config:    config.Config{DataDir: dir, AppURL: app, Mode: "dev", DefaultTz: "Asia/Makassar"},
		Log:       logx.Silent,
		Listener:  listener(t),
		Registry:  map[string]*connectors.Runtime{"telegram": conntest.ChannelRuntime("telegram", telegram), "web": web.Runtime},
		Manifests: []connectors.Manifest{},
		NoBuild:   true,
	})
	testutil.Must(t, err)
	defer a.Stop()
	base := "http://" + a.Addr

	// /health on the TS-written database.
	code, m := getJSON(t, base+"/health")
	rt, _ := m["runtime"].(map[string]any)
	if code != 200 || m["ok"] != true || m["schema"] != "0030_watches" || rt["ok"] != true || len(m["connectors"].([]any)) < 30 {
		t.Fatalf("%d %v", code, m)
	}

	// The owner invite, accepted through the RPC endpoint, signs in.
	inv, err := auth.CreateOwnerInvite(a.DB, app, time.Now().UnixMilli())
	testutil.Must(t, err)
	req, _ := http.NewRequest("POST", base+"/api/rpc/acceptInvite", strings.NewReader(`{"token":"`+inv.Token+`"}`))
	req.Header.Set("origin", app)
	req.Header.Set("content-type", "application/json")
	res, err := http.DefaultClient.Do(req)
	testutil.Must(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.CookieName {
			session = c
		}
	}
	if res.StatusCode != 200 || session == nil || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	req, _ = http.NewRequest("GET", base+"/api/auth/session", nil)
	req.AddCookie(session)
	res, err = http.DefaultClient.Do(req)
	testutil.Must(t, err)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `"username":"owner"`) {
		t.Fatal(string(body))
	}

	// An SSE frame from the events stream.
	req, _ = http.NewRequest("GET", base+"/api/events/stream", nil)
	req.AddCookie(session)
	stream, err := http.DefaultClient.Do(req)
	testutil.Must(t, err)
	line, err := bufio.NewReader(stream.Body).ReadString('\n')
	stream.Body.Close()
	if err != nil || line != "event: summary\n" {
		t.Fatalf("%q %v", line, err)
	}

	// A signed hook delivery reaches the outbox (no model runs here, so the
	// summarize hook forwards the event text to its chat).
	var before int
	testutil.Must(t, a.DB.QueryRow("SELECT count(*) FROM outbox WHERE chat_id = ? AND payload LIKE '%Golden%'", seeded.Golden.ChatID).Scan(&before))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ = http.NewRequest("POST", base+"/api/hooks/"+seeded.Golden.HookSlug, strings.NewReader(seeded.Golden.HookBody))
	req.Header.Set(hooks.TimestampHeader, ts)
	req.Header.Set(hooks.SignatureHeader, hooks.Sign(seeded.Golden.HookSecret, ts, []byte(seeded.Golden.HookBody)))
	res, err = http.DefaultClient.Do(req)
	testutil.Must(t, err)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 202 || !strings.Contains(string(body), `"id":"msg_`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		err := a.DB.QueryRow("SELECT count(*) FROM outbox WHERE chat_id = ? AND payload LIKE '%Golden%'", seeded.Golden.ChatID).Scan(&n)
		testutil.Must(t, err)
		if n > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hook never reached the outbox")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// admin-cli.test.ts.

type harness struct {
	d       *db.DB
	out     *bytes.Buffer
	err     *bytes.Buffer
	pokes   int
	io      *cliIO
	reachOK bool
}

func newHarness(t *testing.T, d *db.DB, key, dbFile, stdin string) *harness {
	if d == nil {
		d = testutil.DB(t)
	}
	if key == "" {
		key = testutil.TestKey
	}
	h := &harness{d: d, out: &bytes.Buffer{}, err: &bytes.Buffer{}, reachOK: true}
	h.io = &cliIO{
		db:        func() (*db.DB, error) { return d, nil },
		poke:      func() bool { h.pokes++; return h.reachOK },
		appURL:    func() string { return "https://palui.example.com" },
		masterKey: func() (string, error) { return key, nil },
		dbFile:    func() string { return dbFile },
		out:       h.out,
		err:       h.err,
		readStdin: func() (string, error) { return stdin, nil },
	}
	return h
}

func (h *harness) run(args ...string) int { return runCLI(args, h.io) }

func lines(b *bytes.Buffer) []string {
	return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
}

func last(b *bytes.Buffer) string {
	l := lines(b)
	return l[len(l)-1]
}

var telegram = connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}

func upsert(t *testing.T, d *db.DB, enabled bool, kv ...string) {
	t.Helper()
	var secrets *jsonx.Map[string]
	if len(kv) > 0 {
		secrets = jsonx.NewMap[string]()
		for i := 0; i+1 < len(kv); i += 2 {
			secrets.Set(kv[i], kv[i+1])
		}
	}
	_, err := connectors.Upsert(d, connectors.UpsertInput{ID: "telegram", Manifest: telegram, Enabled: &enabled, Secrets: secrets}, testutil.TestKey, 1)
	testutil.Must(t, err)
}

func TestCLIUsageAndUnknownCommands(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	if h.run() != 0 || !strings.Contains(h.out.String(), "owner invite") {
		t.Fatal(h.out.String())
	}
	if h.run("frobnicate") != 1 || h.run("connectors", "--nope") != 1 {
		t.Fatal("unknown accepted")
	}
}

func TestCLIOwnerInvitePrintsAWorkingSingleUseLink(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	if h.run("owner", "invite") != 0 {
		t.Fatal(h.err.String())
	}
	url := lines(h.out)[0]
	if !regexp.MustCompile(`^https://palui\.example\.com/app/invite/[\w-]+$`).MatchString(url) {
		t.Fatal(url)
	}
	parts := strings.Split(url, "/")
	st, _ := auth.InspectInvite(h.d, parts[len(parts)-1], 0)
	if !st.OK || !strings.Contains(h.err.String(), "15 minutes") {
		t.Fatal(st, h.err.String())
	}
	if h.run("owner", "invite", "--json") != 0 {
		t.Fatal()
	}
	var m map[string]any
	testutil.Must(t, json.Unmarshal([]byte(last(h.out)), &m))
	if m["url"] == nil || m["recovery"] != false {
		t.Fatal(m)
	}
}

func TestCLIConnectorsListOffAndOn(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	if h.run("connectors", "list") != 0 || !strings.Contains(h.out.String(), "no connectors yet") {
		t.Fatal(h.out.String())
	}
	upsert(t, h.d, true)
	_, err := h.d.Exec("UPDATE connectors SET status = 'on'")
	testutil.Must(t, err)
	if h.run("connectors", "off", "telegram") != 0 || last(h.out) != "telegram paused" {
		t.Fatal(h.out.String(), h.err.String())
	}
	row, _, _ := connectors.Get(h.d, "telegram")
	if row.Status != "paused" || h.pokes != 1 {
		t.Fatal(row.Status, h.pokes)
	}
	if h.run("connectors", "on", "telegram", "--json") != 0 || last(h.out) != `{"id":"telegram","state":"resumed","runtimeReachable":true}` {
		t.Fatal(last(h.out))
	}
	if h.run("connectors", "list", "--json") != 0 || last(h.out) != `[{"id":"telegram","type":"telegram","kind":"channel","enabled":true,"status":"connecting","detail":null}]` {
		t.Fatal(last(h.out))
	}
	if h.run("connectors", "list") != 0 || last(h.out) != "telegram             channel  connecting" {
		t.Fatalf("%q", last(h.out))
	}
	if h.run("connectors", "off", "nope") != 1 || !strings.Contains(last(h.err), `No connector "nope"`) {
		t.Fatal(h.err.String())
	}
	if h.run("connectors", "off") != 1 {
		t.Fatal("no id accepted")
	}
}

func TestCLIConnectorsOffWorksWithTheServerDownAndSaysSo(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	h.reachOK = false
	upsert(t, h.d, true)
	h.d.Exec("UPDATE connectors SET status = 'on'")
	if h.run("connectors", "off", "telegram") != 0 {
		t.Fatal(h.err.String())
	}
	row, _, _ := connectors.Get(h.d, "telegram")
	if row.Status != "paused" || last(h.err) != notReachable {
		t.Fatal(row.Status, h.err.String())
	}
}

func TestCLISecretsCheckPrintsCountsAndFailsOnTheWrongKey(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	upsert(t, h.d, false, "botToken", "a", "other", "b")
	if h.run("secrets-check") != 0 || last(h.out) != "decrypted 2 secrets in 1 connectors and 0 hook secrets" || strings.Contains(h.out.String(), "botToken") {
		t.Fatal(h.out.String())
	}
	wrong := newHarness(t, h.d, "another-master-key-0123456789-abcdefghijkl", ":memory:", "")
	if wrong.run("secrets-check", "--json") != 1 || last(wrong.out) != `{"ok":false,"connectors":0,"secrets":0,"hooks":0,"failed":["connector telegram"]}` {
		t.Fatal(wrong.out.String())
	}
}

func TestCLISecretsCheckRequireSomeFailsWhenThereIsNothingToDecrypt(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	if h.run("secrets-check") != 0 {
		t.Fatal()
	}
	if h.run("secrets-check", "--require-some", "--json") != 1 || last(h.out) != `{"ok":false,"connectors":0,"secrets":0,"hooks":0,"failed":[]}` {
		t.Fatal(last(h.out))
	}
	if h.run("secrets-check", "--require-some") != 1 || !strings.Contains(last(h.err), "nothing to decrypt") {
		t.Fatal(h.err.String())
	}
	upsert(t, h.d, false, "botToken", "a")
	if h.run("secrets-check", "--require-some") != 0 {
		t.Fatal(h.err.String())
	}
}

func TestCLIHookCreatePrintsTheURLAndTheSecretOnce(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "")
	chat, err := chats.EnsureWebChat(h.d, 1)
	testutil.Must(t, err)
	if h.run("hook", "create", "GitHub", "deploys", "--chat", chat.ID, "--mode", "summarize") != 0 {
		t.Fatal(h.err.String())
	}
	out := lines(h.out)
	if !regexp.MustCompile(`^https://palui\.example\.com/api/hooks/[A-Za-z0-9_-]{6,}$`).MatchString(out[0]) {
		t.Fatal(out[0])
	}
	var name, mode, chatID, stored string
	testutil.Must(t, h.d.QueryRow("SELECT name, mode, chat_id, secret FROM hooks").Scan(&name, &mode, &chatID, &stored))
	if name != "GitHub deploys" || mode != "summarize" || chatID != chat.ID || strings.Contains(stored, out[1]) {
		t.Fatal(name, mode)
	}
	if plain, _ := secret.Decrypt(stored, testutil.TestKey); plain != out[1] {
		t.Fatal("secret")
	}
	if h.run("hook", "create", "x", "--chat", "chat_missing") != 1 || h.run("hook", "create", "x", "--chat", chat.ID, "--mode", "loud") != 1 || h.run("hook", "create", "--chat", chat.ID) != 1 {
		t.Fatal("bad input accepted")
	}
	if h.run("hook", "create", "CI", "--chat", chat.ID, "--json") != 0 {
		t.Fatal()
	}
	var m map[string]any
	testutil.Must(t, json.Unmarshal([]byte(last(h.out)), &m))
	if m["mode"] != "forward" || !strings.HasPrefix(m["secret"].(string), "phk_") {
		t.Fatal(m)
	}
}

func TestCLIBackupWritesAGzippedSnapshot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "palui.db")
	d, err := db.Open(file, db.OpenOptions{})
	testutil.Must(t, err)
	defer d.Close()
	upsert(t, d, false)
	h := newHarness(t, d, "", file, "")
	dest := filepath.Join(dir, "backups", "palui.db.gz")
	if h.run("backup", dest) != 0 {
		t.Fatal(h.err.String())
	}
	if !regexp.MustCompile(`^backup .*palui\.db\.gz \d+ bytes`).MatchString(lines(h.out)[0]) {
		t.Fatal(h.out.String())
	}
	head, _ := os.ReadFile(dest)
	if len(head) < 2 || head[0] != 0x1f || head[1] != 0x8b {
		t.Fatal("not gzip")
	}
	if h.run("backup") != 1 {
		t.Fatal("no dest accepted")
	}
}

func TestCLIConnectorsConnectReadsTheSecretFromStdin(t *testing.T) {
	token := "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw0"
	h := newHarness(t, nil, "", ":memory:", token+"\n")
	if h.run("connectors", "connect", "telegram", "--secret-stdin", "botToken") != 0 {
		t.Fatal(h.err.String())
	}
	row, _, _ := connectors.Get(h.d, "telegram")
	if !row.Enabled || row.Status != "connecting" || row.Type != "telegram" || strings.Contains(*row.Secrets, token) || strings.Contains(h.out.String(), token) {
		t.Fatalf("%+v", row)
	}
	if last(h.out) != "telegram saved and switched on" || h.pokes != 1 {
		t.Fatal(h.out.String())
	}
}

func TestCLIConnectorsConnectRefusesABadSecretAndNamesTheField(t *testing.T) {
	h := newHarness(t, nil, "", ":memory:", "not-a-token")
	if h.run("connectors", "connect", "telegram", "--secret-stdin", "botToken") != 1 || !strings.Contains(h.err.String(), "botToken") {
		t.Fatal(h.err.String())
	}
	if h.run("connectors", "connect", "telegram", "--set", "nokey") != 1 || !strings.Contains(h.err.String(), "--set needs key=value") {
		t.Fatal(h.err.String())
	}
}

// pokeServer reaches the admin route on loopback with the derived token.
func TestPokeServer(t *testing.T) {
	token, _ := secret.ControlToken(testutil.TestKey)
	got := ""
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.Must(t, err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path + " " + r.Header.Get(httpapi.AdminHeader)
		w.WriteHeader(200)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if !pokeServer(port, token) || got != "/api/admin/reconcile "+token {
		t.Fatal(got)
	}
	if pokeServer(port, "") {
		t.Fatal("no token")
	}
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	_, closed, _ := net.SplitHostPort(ln2.Addr().String())
	ln2.Close()
	if pokeServer(closed, token) {
		t.Fatal("nothing listens")
	}
}

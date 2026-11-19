package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// portOf is the port of an httptest server on 127.0.0.1.
func portOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestHealthcheckExitsZeroOnlyOn200(t *testing.T) {
	status := http.StatusOK
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	port := portOf(t, srv)
	env := func(k string) string {
		if k == "PORT" {
			return port
		}
		return ""
	}
	var errOut bytes.Buffer
	// PORT from the environment, as in the image.
	if code := runHealthcheck(nil, env, io.Discard, &errOut); code != 0 {
		t.Fatalf("200: %d %q", code, errOut.String())
	}
	if len(paths) != 1 || paths[0] != "/health" {
		t.Fatal(paths)
	}
	for _, s := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusFound, http.StatusNoContent} {
		status = s
		errOut.Reset()
		if code := runHealthcheck([]string{"--port", port}, func(string) string { return "" }, io.Discard, &errOut); code != 1 || !strings.Contains(errOut.String(), "answered") {
			t.Fatalf("%d: %d %q", s, code, errOut.String())
		}
	}
	// Through run(), the way the compose healthcheck calls it.
	status = http.StatusOK
	if code := run([]string{"healthcheck", "--port", port}, io.Discard, io.Discard); code != 0 {
		t.Fatal(code)
	}
}

func TestHealthcheckPrintWritesTheBody(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":true,"runtime":"unreachable"}`+"\n")
	}))
	defer srv.Close()
	port := portOf(t, srv)
	var out bytes.Buffer
	if code := run([]string{"healthcheck", "--port", port, "--print"}, &out, io.Discard); code != 0 {
		t.Fatal(code)
	}
	if out.String() != `{"ok":true,"runtime":"unreachable"}`+"\n" {
		t.Fatalf("%q", out.String())
	}
	// A failing status still prints the body (it says why) and still exits 1.
	status = http.StatusServiceUnavailable
	out.Reset()
	if code := runHealthcheck([]string{"--port", port, "--print"}, func(string) string { return "" }, &out, io.Discard); code != 1 || !strings.Contains(out.String(), "unreachable") {
		t.Fatalf("%d %q", code, out.String())
	}
	// Without --print, stdout stays empty: the compose healthcheck logs nothing.
	out.Reset()
	status = http.StatusOK
	if code := runHealthcheck([]string{"--port", port}, func(string) string { return "" }, &out, io.Discard); code != 0 || out.Len() != 0 {
		t.Fatalf("%d %q", code, out.String())
	}
}

func TestHealthcheckFailsWhenNothingAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close() // nothing listens there now
	var errOut bytes.Buffer
	if code := runHealthcheck([]string{"--port", port}, func(string) string { return "" }, io.Discard, &errOut); code != 1 || errOut.Len() == 0 {
		t.Fatalf("%d %q", code, errOut.String())
	}
}

func TestHealthcheckTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	if code := runHealthcheck([]string{"--port", portOf(t, srv), "--timeout", "200ms"}, func(string) string { return "" }, io.Discard, io.Discard); code != 1 {
		t.Fatal(code)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %s", d)
	}
	if code := runHealthcheck([]string{"--nope"}, func(string) string { return "" }, io.Discard, io.Discard); code != 2 {
		t.Fatal(code)
	}
}

func TestServeFlagsRuntimeOff(t *testing.T) {
	cases := []struct {
		env  string
		args []string
		want bool
	}{
		{"", nil, false},
		{"1", nil, true},
		{"true", nil, true},
		{"ON", nil, true},
		{"0", nil, false},
		{"off", nil, false},
		{"", []string{"--no-runtime"}, true},
		{"1", []string{"--no-runtime=false"}, false},
	}
	for _, c := range cases {
		env := map[string]string{"PALUI_NO_RUNTIME": c.env}
		o, err := serveFlags(c.args, func(k string) string { return env[k] }, io.Discard)
		if err != nil || o.RuntimeOff != c.want {
			t.Fatalf("env %q args %v: %v %v", c.env, c.args, o.RuntimeOff, err)
		}
	}
}

func TestSnapshotCopiesAWALDatabaseConsistently(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "wa", "whatsapp.db")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	// A WAL database with an open writer, like whatsmeow's store.
	live, err := sql.Open("sqlite", "file:"+src+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.SetMaxOpenConns(1)
	for _, q := range []string{
		"PRAGMA wal_autocheckpoint = 0",
		"CREATE TABLE device (jid TEXT PRIMARY KEY, data BLOB)",
		"INSERT INTO device VALUES ('628@s.whatsapp.net', x'00ff')",
	} {
		if _, err := live.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if st, err := os.Stat(src + "-wal"); err != nil || st.Size() == 0 {
		t.Fatal("expected the rows to sit in the WAL", err)
	}
	dest := filepath.Join(dir, "backups", "wa-whatsapp.db.gz")
	var out, errOut bytes.Buffer
	if code := run([]string{"snapshot", src, dest, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"dest":"`+dest+`"`) {
		t.Fatal(out.String())
	}
	// Unpack and read the row that was only in the WAL.
	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "restored.db")
	b, err := io.ReadAll(zr)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := sql.Open("sqlite", "file:"+plain)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var jid string
	if err := r.QueryRow("SELECT jid FROM device").Scan(&jid); err != nil || jid != "628@s.whatsapp.net" {
		t.Fatal(jid, err)
	}
	// No temp files left next to the destination.
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatal(entries)
	}

	if code := run([]string{"snapshot", src}, io.Discard, io.Discard); code != 2 {
		t.Fatal(code)
	}
	if code := run([]string{"snapshot", src, dest, "--nope"}, io.Discard, io.Discard); code != 2 {
		t.Fatal(code)
	}
	if code := run([]string{"snapshot", filepath.Join(dir, "missing.db"), dest + "2"}, io.Discard, io.Discard); code != 1 {
		t.Fatal(code)
	}
}

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/fixtures"
)

func devRun(env map[string]string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := runDev(args, func(k string) string { return env[k] }, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestDevSeed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")
	env := map[string]string{"DATA_DIR": dir}

	code, out, errOut := devRun(env, "seed")
	file := db.FilePath(dir)
	if code != 0 || out != "seeded "+file+" with the \"demo\" fixture\n" {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if code, out, _ = devRun(env, "seed", "worst"); code != 0 || !strings.Contains(out, `"worst"`) {
		t.Fatalf("%d %q", code, out)
	}
	d, err := db.Open(file, db.OpenOptions{NoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := fixtures.Current(d)
	d.Close()
	if err != nil || cur != "worst" {
		t.Fatalf("current %q %v", cur, err)
	}

	if code, _, errOut = devRun(env, "seed", "nope"); code != 1 || errOut != "Unknown fixture \"nope\". Use one of: demo, worst, empty, one, huge\n" {
		t.Fatalf("%d %q", code, errOut)
	}
	prod := map[string]string{"DATA_DIR": dir, "NODE_ENV": "production"}
	if code, _, errOut = devRun(prod, "seed", "demo"); code != 1 || errOut != "seed-demo is for dev databases only\n" {
		t.Fatalf("%d %q", code, errOut)
	}
	if code, _, errOut = devRun(prod, "serve"); code != 1 || !strings.Contains(errOut, "dev databases only") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestDevSeedRefusesRealData(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"DATA_DIR": dir}
	d, err := db.Open(db.FilePath(dir), db.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO connectors (id, type, kind, label, created_at, updated_at) VALUES ('telegram', 'telegram', 'channel', 'Telegram', 1, 1)"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	code, _, errOut := devRun(env, "seed", "demo")
	if code != 1 || errOut != fixtures.RealDataError.Error()+"\n" {
		t.Fatalf("%d %q", code, errOut)
	}
	if code, _, errOut = devRun(env, "seed", "--force", "one"); code != 0 {
		t.Fatalf("--force: %d %q", code, errOut)
	}
}

func TestDevUsage(t *testing.T) {
	if code, _, errOut := devRun(nil); code != 2 || !strings.Contains(errOut, "usage: palui dev") {
		t.Fatalf("%d %q", code, errOut)
	}
	if code, _, errOut := devRun(nil, "frob"); code != 2 || !strings.Contains(errOut, `unknown dev command "frob"`) {
		t.Fatalf("%d %q", code, errOut)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"dev"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: palui dev") {
		t.Fatalf("run dev: %d %q", code, errOut.String())
	}
}

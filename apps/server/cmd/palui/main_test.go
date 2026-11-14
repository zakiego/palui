package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "palui ") {
		t.Fatalf("%d %q %q", code, out.String(), errOut.String())
	}
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatal(code)
	}
	if code := run([]string{"frobnicate"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatal(code)
	}
}

func TestServeFlags(t *testing.T) {
	env := map[string]string{"HOST": "127.0.0.1", "PORT": "4899", "DATA_DIR": "/tmp/x", "APP_URL": "https://a.example/", "PALUI_WEB_DIR": "/srv/web"}
	var errOut bytes.Buffer
	o, err := serveFlags(nil, func(k string) string { return env[k] }, &errOut)
	if err != nil || o.Host != "127.0.0.1" || o.Port != "4899" || o.WebDir != "/srv/web" || o.Config.DataDir != "/tmp/x" || o.Config.AppURL != "https://a.example" {
		t.Fatalf("%+v %v", o, err)
	}
	o, err = serveFlags([]string{"--port", "4900", "--web-dir", "dist", "--data-dir", "/tmp/y", "--app-url", "http://localhost:4900/"}, func(k string) string { return env[k] }, &errOut)
	if err != nil || o.Port != "4900" || o.WebDir != "dist" || o.Config.DataDir != "/tmp/y" || o.Config.AppURL != "http://localhost:4900" {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := serveFlags([]string{"--nope"}, func(string) string { return "" }, &errOut); err == nil {
		t.Fatal("unknown flag")
	}
}

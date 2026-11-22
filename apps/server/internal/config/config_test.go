package config_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// config.ts has no test file; these pin the defaults and the master key rules.

func TestDefaults(t *testing.T) {
	c := config.FromMap(nil)
	want := config.Config{DataDir: "./data", AppURL: "http://localhost:4890", DefaultTz: "UTC", Mode: "dev"}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("%+v", c)
	}
}

func TestReadsTheEnvironment(t *testing.T) {
	c := config.FromMap(map[string]string{"DATA_DIR": "/data", "APP_URL": "https://palui.test///", "NODE_ENV": "production", "PALUI_PROD_TOKEN_HASHES": " a, ,b ", "PALUI_TZ": "UTC"})
	if c.DataDir != "/data" || c.AppURL != "https://palui.test" || c.Mode != "prod" || !reflect.DeepEqual(c.ProdTokenHashes, []string{"a", "b"}) || c.DefaultTz != "UTC" {
		t.Fatalf("%+v", c)
	}
}

func TestMasterKey(t *testing.T) {
	k, err := config.MasterKey(config.FromMap(nil))
	if err != nil || k != config.DevMasterKey {
		t.Fatal(k, err)
	}
	if _, err := config.MasterKey(config.FromMap(map[string]string{"NODE_ENV": "production"})); !errors.Is(err, config.ErrNoMasterKey) {
		t.Fatal(err)
	}
	k, _ = config.MasterKey(config.FromMap(map[string]string{"MASTER_KEY": "x"}))
	if k != "x" {
		t.Fatal(k)
	}
	tok, err := config.AdminToken(config.FromMap(nil))
	want, _ := secret.ControlToken(config.DevMasterKey)
	if err != nil || tok != want {
		t.Fatal(tok, err)
	}
}

func TestSetOverrides(t *testing.T) {
	config.Set(&config.Config{DataDir: "/tmp/x"})
	defer config.Set(nil)
	if config.Get().DataDir != "/tmp/x" {
		t.Fatal("override")
	}
}

// Package config ports packages/core/src/config.ts: process configuration from
// the environment. Only MASTER_KEY and APP_URL matter for secrets; every other
// credential lives encrypted in the database.
//
// The Go build is one process: RUNTIME_URL, CONTROL_PORT and the control token
// (the app to runtime API) are gone, and so is BETTER_AUTH_SECRET (sessions
// are opaque tokens checked against the database). HOST and PORT are read by
// `palui serve` itself (serve.ListenAddr).
package config

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

type Config struct {
	DataDir   string
	MasterKey string
	// Public origin of the dashboard (cookies, passkey rpID, invite links).
	AppURL    string
	DefaultTz string
	// "prod" refuses nothing; "dev" refuses channel tokens that match PALUI_PROD_TOKEN_HASHES.
	Mode            string
	ProdTokenHashes []string
}

// DevMasterKey is the fallback so dev works before a MASTER_KEY exists. Never used in prod.
const DevMasterKey = "palui-dev-master-key-not-for-production-use-0000"

func trimTrailingSlashes(s string) string { return strings.TrimRight(s, "/") }

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Load reads the config from env (a getenv function, os.Getenv in production).
func Load(getenv func(string) string) Config {
	var hashes []string
	for _, s := range strings.Split(getenv("PALUI_PROD_TOKEN_HASHES"), ",") {
		if s = strx.Trim(s); s != "" {
			hashes = append(hashes, s)
		}
	}
	mode := "dev"
	if getenv("NODE_ENV") == "production" {
		mode = "prod"
	}
	return Config{
		DataDir:         or(getenv("DATA_DIR"), "./data"),
		MasterKey:       getenv("MASTER_KEY"),
		AppURL:          trimTrailingSlashes(or(getenv("APP_URL"), "http://localhost:4890")),
		DefaultTz:       or(getenv("PALUI_TZ"), "Asia/Makassar"),
		Mode:            mode,
		ProdTokenHashes: hashes,
	}
}

// FromMap loads from a fixed map, for tests.
func FromMap(env map[string]string) Config {
	return Load(func(k string) string { return env[k] })
}

var (
	mu     sync.Mutex
	cached *Config
)

// Get is the process config, loaded once from the environment.
func Get() Config {
	mu.Lock()
	defer mu.Unlock()
	if cached == nil {
		c := Load(os.Getenv)
		cached = &c
	}
	return *cached
}

// Set overrides the process config (tests). Nil goes back to the environment.
func Set(c *Config) {
	mu.Lock()
	defer mu.Unlock()
	cached = c
}

// ErrNoMasterKey is returned in prod when MASTER_KEY is missing.
var ErrNoMasterKey = errors.New("MASTER_KEY is not set. Add it to .env (openssl rand -base64 32).")

// MasterKey is cfg.MasterKey, or the dev key outside prod.
func MasterKey(cfg Config) (string, error) {
	if cfg.MasterKey != "" {
		return cfg.MasterKey, nil
	}
	if cfg.Mode == "prod" {
		return "", ErrNoMasterKey
	}
	return DevMasterKey, nil
}

// AdminToken guards the loopback admin route the CLI pokes after a
// desired-state write (POST /api/admin/reconcile). It is the old app to
// runtime control token, derived from MasterKey, so the CLI needs no new setting.
func AdminToken(cfg Config) (string, error) {
	k, err := MasterKey(cfg)
	if err != nil {
		return "", err
	}
	return secret.ControlToken(k)
}

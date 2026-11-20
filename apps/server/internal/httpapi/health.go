package httpapi

import (
	"net/http"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/version"
)

// RuntimeLive is the old runtime's GET /live body, now about the runtime
// running in this process.
type RuntimeLive struct {
	OK      bool   `json:"ok"`
	Schema  string `json:"schema"`
	Version string `json:"version"`
}

type healthConnector struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type healthBody struct {
	OK         bool              `json:"ok"`
	Version    string            `json:"version"`
	Schema     string            `json:"schema"`
	Runtime    any               `json:"runtime"`
	Connectors []healthConnector `json:"connectors"`
}

type healthNewer struct {
	OK                bool     `json:"ok"`
	Version           string   `json:"version"`
	Error             string   `json:"error"`
	Message           string   `json:"message"`
	UnknownMigrations []string `json:"unknownMigrations"`
}

// live is the in-process /live: the database answers, the schema is the one
// this build knows, and the runtime started. Nil runtime: "unreachable".
func (s *Server) live() any {
	if s.o.Runtime == nil {
		return "unreachable"
	}
	out := RuntimeLive{Schema: "unknown", Version: version.Version}
	var one int
	if err := s.o.DB.QueryRow("SELECT 1").Scan(&one); err != nil {
		return out
	}
	st, err := db.GetSchemaState(s.o.DB)
	if err != nil || !st.OK {
		return out
	}
	out.OK, out.Schema = true, st.Version
	return out
}

// health is GET /health (health.ts). The compose healthcheck and deploy.sh
// read its status code, so only "this build must not serve" turns it non-200:
// a database migrated by a newer build. Runtime and connector state go in the
// body and never change the code.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("cache-control", "no-store")
	st, err := db.GetSchemaState(s.o.DB)
	if err != nil {
		s.o.Log.Error("health: schema state failed", logx.Fields{"error": err})
		writeJSON(w, 500, map[string]string{"error": "Internal error"})
		return
	}
	if !st.OK && st.Reason == "newer" {
		writeJSON(w, http.StatusServiceUnavailable, healthNewer{
			OK:                false,
			Version:           version.Version,
			Error:             "db_newer_than_app",
			Message:           "The database has migrations this build doesn't know (" + strings.Join(st.Unknown, ", ") + "). A newer version migrated it. Deploy that version again, or restore a backup taken before it.",
			UnknownMigrations: st.Unknown,
		})
		return
	}
	list := []healthConnector{}
	// Anyone on the internet can read /health, and the connector list names
	// every service the owner connected, so strangers get an empty list.
	// A schema that's behind shows up in `schema` below; don't fail on it here.
	if s.showConnectors(r) {
		if rows, err := connectors.List(s.o.DB); err == nil {
			for _, c := range rows {
				list = append(list, healthConnector{ID: c.ID, Status: c.Status})
			}
		}
	}
	schema := st.Version
	if !st.OK {
		schema = "behind (" + strings.Join(st.Missing, ", ") + ")"
	}
	writeJSON(w, 200, healthBody{OK: true, Version: version.Version, Schema: schema, Runtime: s.live(), Connectors: list})
}

// adminReconcile is the CLI's poke after a desired-state write (the old
// POST /control/reconcile): loopback callers with the admin token only. The
// token is secret.ControlToken(masterKey), so the CLI derives it from the same
// MASTER_KEY without any new configuration.
func (s *Server) adminReconcile(w http.ResponseWriter, r *http.Request) {
	if !loopback(r.RemoteAddr) || s.o.AdminToken == "" || !secret.SafeEqual(r.Header.Get(AdminHeader), s.o.AdminToken) {
		writeJSON(w, 401, map[string]string{"error": "Missing or wrong admin token"})
		return
	}
	if s.o.Runtime == nil {
		writeJSON(w, 503, map[string]string{"error": RuntimeDown})
		return
	}
	s.o.Runtime.Reconcile()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// AdminHeader carries the admin token (the old control header).
const AdminHeader = "x-palui-control"

// RuntimeDown is runtime.ts RUNTIME_DOWN, for calls that need the runtime
// while it isn't running.
const RuntimeDown = "The runtime isn't reachable. Check docker compose logs runtime."

// showConnectors: the caller is signed in, or is a direct loopback request
// (the container healthcheck, a curl on the host) that no proxy forwarded.
func (s *Server) showConnectors(r *http.Request) bool {
	if loopback(r.RemoteAddr) && r.Header.Get("cf-connecting-ip") == "" && r.Header.Get("x-forwarded-for") == "" {
		return true
	}
	f, err := auth.GetSession(s.o.DB, auth.TokenFrom(r), false, s.now())
	return err == nil && f != nil
}

func loopback(remote string) bool {
	host := remote
	if i := strings.LastIndex(remote, ":"); i >= 0 {
		host = remote[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}

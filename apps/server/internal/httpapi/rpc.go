package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/connectors/manage"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// The dashboard's server functions (apps/app/src/lib/fns) as
// `GET|POST /api/rpc/<name>` with JSON in and out. Each handler declares its
// name, method, guard and input schema; the framework runs the same-origin
// check on every POST, the guard, and zod's validation before the handler.
//
// Input: a GET sends it as ?input=<JSON>, a POST as the JSON body; none is
// undefined. Output: the handler's value as JSON (nil is null).
//
// Errors are one shape, whatever failed:
//
//	{"error": "<message the dashboard shows>", "code": "<CODE>", "fields"?: {...}, "issues"?: [...]}
//
// code is NOT_FOUND (no such function, 404), METHOD_NOT_ALLOWED (405),
// FORBIDDEN (cross-origin write, 403, error "cross-origin request refused"),
// UNAUTHORIZED (no session, 401, error "UNAUTHORIZED", which the dashboard
// matches on to go to the login page), VALIDATION (the input failed its
// schema, 400; error is ZodError's message, issues the zod issues), or the
// handler's: an *Error keeps its status, code and fields, a *manage.Error is
// 400 BAD_REQUEST with its fields, an *auth.Error keeps its status and code,
// anything else is 500 ERROR with the error's text (a thrown Error's message
// reached the dashboard the same way under TanStack Start).

// Guard is who may call a handler. The zero value is invalid, so a handler
// that forgot to pick one fails Register.
type Guard int

const (
	guardUnset Guard = iota
	// Public: anyone (the login and invite pages). A public POST still gets
	// the same-origin check.
	Public
	// User: a signed-in session (requireUser).
	User
	// Writer: same origin plus a signed-in session (requireWriter). POST only.
	Writer
)

func (g Guard) String() string {
	switch g {
	case Public:
		return "public"
	case User:
		return "user"
	case Writer:
		return "writer"
	}
	return "unset"
}

// Handler is one server function.
type Handler struct {
	// The server function's exported name (getSession, listPasskeys, ...).
	Name string
	// http.MethodGet or http.MethodPost, as createServerFn({method}).
	Method string
	Guard  Guard
	// zod schema for the input (.validator(...)); nil takes no input.
	Input zod.Schema
	Fn    func(c *Call) (any, error)
}

// Call is one invocation.
type Call struct {
	Ctx context.Context
	R   *http.Request
	// W is for cookies only; the framework writes the body.
	W http.ResponseWriter
	S *Server
	// The caller; nil on a Public handler without a session.
	Viewer *auth.Viewer
	// zod's output for the input (jsonx values: *jsonx.Object, []any,
	// string, float64, bool, nil), or zod.Undefined.
	Input any
}

// DB, MasterKey, Now and friends, for handlers.
func (c *Call) DB() *db.DB        { return c.S.o.DB }
func (c *Call) MasterKey() string { return c.S.o.MasterKey }
func (c *Call) AppURL() string    { return c.S.o.Config.AppURL }
func (c *Call) DataDir() string   { return c.S.o.Config.DataDir }
func (c *Call) Now() int64        { return c.S.now() }
func (c *Call) Log() *logx.Logger { return c.S.o.Log }
func (c *Call) Runtime() (*Runtime, error) {
	if c.S.o.Runtime == nil {
		return nil, &Error{Status: 503, Code: "RUNTIME_DOWN", Message: RuntimeDown}
	}
	return c.S.o.Runtime, nil
}

// Obj is the input as an object (nil when it isn't one).
func (c *Call) Obj() *jsonx.Object {
	o, _ := c.Input.(*jsonx.Object)
	return o
}

// Decode copies the validated input into a Go value through JSON.
func (c *Call) Decode(v any) error {
	if zod.IsUndefined(c.Input) {
		return nil
	}
	b, err := jsonx.Marshal(c.Input)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Fail is an error the dashboard shows as is (400).
func Fail(format string, args ...any) error {
	return &Error{Status: 400, Code: "BAD_REQUEST", Message: fmt.Sprintf(format, args...)}
}

var (
	regMu    sync.RWMutex
	registry = map[string]*Handler{}
)

// CheckHandler reports what's wrong with a handler definition, or nil.
func CheckHandler(h Handler) error {
	switch {
	case h.Name == "":
		return errors.New("a handler needs a name")
	case h.Method != http.MethodGet && h.Method != http.MethodPost:
		return fmt.Errorf("%s: method must be GET or POST", h.Name)
	case h.Guard == guardUnset || h.Guard > Writer:
		return fmt.Errorf("%s: no guard (Public, User or Writer)", h.Name)
	case h.Guard == Writer && h.Method != http.MethodPost:
		return fmt.Errorf("%s: Writer is for POST handlers", h.Name)
	case h.Method == http.MethodPost && h.Guard == User:
		return fmt.Errorf("%s: a POST handler is Writer or Public (every write checks the origin)", h.Name)
	case h.Fn == nil:
		return fmt.Errorf("%s: no Fn", h.Name)
	}
	return nil
}

// Register adds a server function. It panics on a bad definition or a
// duplicate name, so a mistake fails at startup and in every test.
func Register(h Handler) {
	if err := CheckHandler(h); err != nil {
		panic("httpapi.Register: " + err.Error())
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[h.Name]; dup {
		panic("httpapi.Register: duplicate " + h.Name)
	}
	registry[h.Name] = &h
}

// Handlers lists every registered handler by name.
func Handlers() []Handler {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Handler, 0, len(registry))
	for _, h := range registry {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func lookup(name string) *Handler {
	regMu.RLock()
	defer regMu.RUnlock()
	return registry[name]
}

// RPCError is the error body.
type RPCError struct {
	Error  string             `json:"error"`
	Code   string             `json:"code"`
	Fields *jsonx.Map[string] `json:"fields,omitempty"`
	Issues []*jsonx.Object    `json:"issues,omitempty"`
}

func rpcFail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, RPCError{Error: msg, Code: code})
}

func readInput(r *http.Request) (any, error) {
	var raw []byte
	if r.Method == http.MethodGet {
		if !r.URL.Query().Has("input") {
			return zod.Undefined, nil
		}
		raw = []byte(r.URL.Query().Get("input"))
	} else {
		b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
		if err != nil {
			return nil, &Error{Status: 413, Code: "VALIDATION", Message: "Body is too large"}
		}
		raw = b
	}
	if len(raw) == 0 {
		return zod.Undefined, nil
	}
	v, err := jsonx.Parse(raw)
	if err != nil {
		return nil, &Error{Status: 400, Code: "VALIDATION", Message: "Input isn't valid JSON"}
	}
	return v, nil
}

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h := lookup(name)
	if h == nil {
		rpcFail(w, 404, "NOT_FOUND", "No server function "+name)
		return
	}
	if r.Method != h.Method {
		w.Header().Set("allow", h.Method)
		rpcFail(w, 405, "METHOD_NOT_ALLOWED", name+" takes "+h.Method)
		return
	}
	if r.Method == http.MethodPost && !auth.SameOrigin(r, s.o.Config.AppURL) {
		rpcFail(w, 403, "FORBIDDEN", "cross-origin request refused")
		return
	}
	viewer, err := s.Viewer(w, r)
	if err != nil {
		s.o.Log.Error("session lookup failed", logx.Fields{"error": err})
	}
	if h.Guard != Public && viewer == nil {
		rpcFail(w, 401, "UNAUTHORIZED", "UNAUTHORIZED")
		return
	}
	input, err := readInput(r)
	if err != nil {
		s.rpcError(w, name, err)
		return
	}
	if h.Input != nil {
		out, err := zod.Parse(h.Input, input)
		if err != nil {
			var ze *zod.Error
			if errors.As(err, &ze) {
				issues := make([]*jsonx.Object, len(ze.Issues))
				for i, is := range ze.Issues {
					issues[i] = is.JSON()
				}
				writeJSON(w, 400, RPCError{Error: ze.Error(), Code: "VALIDATION", Issues: issues})
				return
			}
			s.rpcError(w, name, err)
			return
		}
		input = out
	}
	res, err := h.Fn(&Call{Ctx: r.Context(), R: r, W: w, S: s, Viewer: viewer, Input: input})
	if err != nil {
		s.rpcError(w, name, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) rpcError(w http.ResponseWriter, name string, err error) {
	var he *Error
	var me *manage.Error
	var ae *auth.Error
	switch {
	case errors.As(err, &he):
		code := he.Code
		if code == "" {
			code = "ERROR"
		}
		status := he.Status
		if status == 0 {
			status = 500
		}
		writeJSON(w, status, RPCError{Error: he.Message, Code: code, Fields: he.Fields})
	case errors.As(err, &me):
		var fields *jsonx.Map[string]
		if me.Fields != nil && me.Fields.Len() > 0 {
			fields = me.Fields
		}
		writeJSON(w, 400, RPCError{Error: me.Message, Code: "BAD_REQUEST", Fields: fields})
	case errors.As(err, &ae):
		writeJSON(w, ae.Status, RPCError{Error: ae.Message, Code: ae.Code})
	default:
		s.o.Log.Error("server function failed", logx.Fields{"fn": name, "error": err})
		writeJSON(w, 500, RPCError{Error: err.Error(), Code: "ERROR"})
	}
}

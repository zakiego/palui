package recall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// DefaultURL is RECALL_DEFAULT_URL.
const DefaultURL = "http://host.docker.internal:4800"

// ConfigZod is the recall manifest's config.
var ConfigZod = zod.Object(
	zod.F("url", zod.Default(zod.String().URL(regexp.MustCompile(`^https?$`)), DefaultURL)),
	zod.F("container", zod.Default(zod.String().Trim().Min(1).Max(100), "default")),
	zod.F("autoRecall", zod.Default(zod.Bool(), true)),
	zod.F("autoSave", zod.Default(zod.Bool(), true)),
	zod.F("defaultContainer", zod.String().Trim().Min(1).Max(100).Optional()),
)

var SecretZod = []zod.Field{zod.F("apiKey", zod.String().Regex(regexp.MustCompile(`^rcl_[A-Za-z0-9_-]+$`), `^rcl_[A-Za-z0-9_-]+$`, "recall keys start with rcl_"))}

var Manifest = connectors.Manifest{
	ID:      "recall",
	Kind:    "memory",
	Name:    "recall",
	Icon:    "brain",
	Summary: "Remembers what you tell your AI agent.",
	Setup: []connectors.SetupStep{
		{Field: "url", Label: "recall URL", Placeholder: DefaultURL, Help: "In Docker on the same host, keep the default."},
		{Field: "apiKey", Label: "API key", Secret: true, Placeholder: "rcl_...", Help: "recall Settings > API keys."},
		{Action: "pickContainer", Label: "Pick a container"},
	},
	Trust:         "own",
	PauseNote:     "While paused, Palui doesn't recall or save memories.",
	RemoveWarning: "This removes the recall key from Palui. Your memories stay in recall.",
	Docs: &connectors.Docs{Notes: []string{
		"Tools: add_memory (write), search_memories, ask_memory, get_profile, list_memories (read), forget_memory and forget_document (destructive).",
		"In your own DM or the web chat, Palui also gets search_agent_memory and list_agent_memory (read) to read other agents' memory. They never read yours. After reading, every write asks you first, even a tool you set to allow.",
		"Auto-save uses source_id <session>:<range>, so saving the same slice twice is a no-op.",
	}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

// ToolClasses are recall's own MCP tools and their classes. Anything recall
// adds later is destructive until it's listed here (fail closed).
var ToolClasses = map[string]string{
	"add_memory":      "write",
	"search_memories": "read",
	"ask_memory":      "read",
	"get_profile":     "read",
	"list_memories":   "read",
}

// ToolClass is recallToolClass.
func ToolClass(name string) string {
	if c, ok := ToolClasses[name]; ok {
		return c
	}
	return "destructive"
}

// Stats are RecallStats.
type Stats struct {
	mu                                                 sync.Mutex
	Calls, Hits, Empty, SkippedShort, Timeouts, Errors int
}

// ShouldRecall: auto-recall skips commands and messages under 4 words.
func ShouldRecall(query string) bool {
	q := strx.Trim(query)
	if q == "" || strings.HasPrefix(q, "/") {
		return false
	}
	return len(splitSpace(q)) >= 4
}

func splitSpace(s string) []string {
	return strings.FieldsFunc(s, strx.IsSpace)
}

// Config is the stored config as the runtime reads it.
type Config struct {
	URL              string
	Container        string
	AutoRecall       bool
	AutoSave         bool
	DefaultContainer string
}

// ConfigFrom reads a stored config (no defaults, like ctx.config).
func ConfigFrom(o *jsonx.Object) Config {
	get := func(k string) any {
		if o == nil {
			return nil
		}
		v, _ := o.Get(k)
		return v
	}
	s := func(k string) string { x, _ := get(k).(string); return x }
	return Config{URL: s("url"), Container: s("container"), AutoRecall: get("autoRecall") == true, AutoSave: get("autoSave") == true, DefaultContainer: s("defaultContainer")}
}

// MemoryHandle is createMemoryHandle.
func MemoryHandle(api *API, cfg Config, stats *Stats) *connectors.MemoryHandle {
	return &connectors.MemoryHandle{
		AutoRecall: cfg.AutoRecall,
		AutoSave:   cfg.AutoSave,
		Recall: func(ctx context.Context, query string, opts connectors.RecallOptions) (string, bool, error) {
			stats.mu.Lock()
			if !ShouldRecall(query) {
				stats.SkippedShort++
				stats.mu.Unlock()
				return "", false, nil
			}
			stats.Calls++
			stats.mu.Unlock()
			container := cfg.Container
			if opts.Container != nil && *opts.Container != "" {
				container = *opts.Container
			}
			tctx, cancel := context.WithTimeout(ctx, time.Duration(opts.TimeoutMs)*time.Millisecond)
			defer cancel()
			p, err := api.Profile(tctx, container, query)
			stats.mu.Lock()
			defer stats.mu.Unlock()
			if err != nil {
				if IsTimeout(err) || tctx.Err() != nil {
					stats.Timeouts++
				} else {
					stats.Errors++
				}
				return "", false, nil
			}
			text, ok := ProfileText(p, opts.About)
			if ok {
				stats.Hits++
			} else {
				stats.Empty++
			}
			return text, ok, nil
		},
		Save: func(ctx context.Context, in connectors.SaveInput) error {
			if strx.Trim(in.Content) == "" {
				return nil
			}
			container := cfg.Container
			if in.Container != nil && *in.Container != "" {
				container = *in.Container
			}
			return api.AddMemory(ctx, container, in.Content, in.Title, in.SourceID)
		},
	}
}

// ForgetMax is FORGET_MAX.
const ForgetMax = 20

// Scope is the container a turn's recall tools may use.
type Scope struct {
	Container string
	Read      []string
	Locked    bool
}

const unknownContainer = "Can't tell which recall container this agent uses right now. Try again in a moment."

func forgetOne(ctx context.Context, api *API, id string, scope *Scope) (any, error) {
	if scope != nil && scope.Locked && scope.Container == "" {
		return kit.O("forgotten", false, "id", id, "error", unknownContainer), nil
	}
	notFound := kit.O("forgotten", false, "id", id, "error", "No memory with that id")
	if scope != nil && scope.Locked {
		found, err := api.GetMemory(ctx, id)
		if err != nil {
			var re *Error
			if errors.As(err, &re) && re.Status == 404 {
				return notFound, nil
			}
			return nil, err
		}
		if found == nil || found.Container != scope.Container {
			return notFound, nil
		}
	}
	m, err := api.Forget(ctx, id)
	if err != nil {
		var re *Error
		if errors.As(err, &re) && re.Status == 404 {
			return notFound, nil
		}
		return nil, err
	}
	var fact any = kit.Undef
	if m != nil {
		fact = m.Fact
	}
	return kit.O("forgotten", true, "id", id, "fact", fact), nil
}

var forgetInput = zod.Object(
	zod.F("ids", zod.Array(zod.String().Min(1)).Min(1).Max(ForgetMax).Optional().Describe("Memory ids")),
	zod.F("id", zod.String().Min(1).Optional().Describe("One memory id (older form; prefer ids)")),
)

// ForgetTool is forgetTool(api, scope): one call takes every id, so
// forgetting several facts asks the owner once.
func ForgetTool(api *API, scope *Scope) connectors.Tool {
	return kit.Tool(fmt.Sprintf("Forget memories in recall by id (from search_memories or list_memories). The facts stop being recalled. Pass every id in one call, at most %d: each call asks the owner first.", ForgetMax), forgetInput,
		func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
			ids, hasIDs := in.Strs("ids")
			id := in.S("id")
			var all []string
			for _, x := range append(append([]string{}, ids...), id) {
				if x != "" && !slices.Contains(all, x) {
					all = append(all, x)
				}
			}
			if len(all) > ForgetMax {
				all = all[:ForgetMax]
			}
			if len(all) == 0 {
				return kit.O("forgotten", 0, "results", []any{}, "error", "Pass ids."), nil
			}
			results := make([]any, 0, len(all))
			n := 0
			for _, one := range all {
				r, err := forgetOne(ctx, api, one, scope)
				if err != nil {
					return nil, err
				}
				if v, _ := r.(*jsonx.Object).Get("forgotten"); v == true {
					n++
				}
				results = append(results, r)
			}
			if !hasIDs && id != "" {
				return results[0], nil
			}
			return kit.O("forgotten", n, "results", results), nil
		})
}

var forgetDocumentInput = zod.Object(zod.F("id", zod.String().Min(1).Describe("Document id, doc_...")))

// ForgetDocumentTool is forgetDocumentTool(api, scope).
func ForgetDocumentTool(api *API, scope *Scope) connectors.Tool {
	return kit.Tool("Delete a document from recall by its id (doc_..., as add_memory returned it): its stored text stops showing up in searches. Facts already taken from it stay; forget those with forget_memory. Ask the owner first.", forgetDocumentInput,
		func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
			id := in.S("id")
			if scope != nil && scope.Locked && scope.Container == "" {
				return kit.O("deleted", false, "id", id, "error", unknownContainer), nil
			}
			notFound := kit.O("deleted", false, "id", id, "error", "No document with that id")
			found, err := api.GetDocument(ctx, id)
			if err == nil {
				if scope != nil && scope.Locked && (found == nil || found.Container != scope.Container) {
					return notFound, nil
				}
				err = api.DeleteDocument(ctx, id)
			}
			if err != nil {
				var re *Error
				if errors.As(err, &re) && re.Status == 404 {
					return notFound, nil
				}
				return nil, err
			}
			var title any
			if found != nil && found.Title != nil {
				title = *found.Title
			}
			return kit.O("deleted", true, "id", id, "title", title), nil
		})
}

// ContainerError is a refused container.
type ContainerError struct{ Msg string }

func (e *ContainerError) Error() string { return e.Msg }

// ResolveContainer is resolveContainer: an empty container is the turn's own;
// a locked scope can't reach past its own container, and its read list for
// read tools.
func ResolveContainer(asked any, cls string, scope Scope) (string, error) {
	name := ""
	if s, ok := asked.(string); ok {
		name = strx.Trim(s)
	}
	if name == "" || name == scope.Container {
		return scope.Container, nil
	}
	if !scope.Locked {
		return name, nil
	}
	if cls == "read" && slices.Contains(scope.Read, name) {
		return name, nil
	}
	var allowed []string
	if cls == "read" {
		allowed = append([]string{scope.Container}, scope.Read...)
	} else {
		allowed = []string{scope.Container}
	}
	var kept []string
	for _, a := range allowed {
		if a != "" {
			kept = append(kept, a)
		}
	}
	can := "It can only use its own container."
	if len(kept) > 0 {
		verb := "write"
		if cls == "read" {
			verb = "read"
		}
		can = fmt.Sprintf("It can %s %s.", verb, strings.Join(kept, ", "))
	}
	return "", &ContainerError{fmt.Sprintf("This agent can't use recall container %s. %s", strx.Slice16(name, 64), can)}
}

func withContainer(t connectors.Tool, cls string, scope *Scope) connectors.Tool {
	exec := t.Execute
	if exec == nil {
		return t
	}
	t.Execute = func(ctx context.Context, input json.RawMessage, call connectors.ToolCallInfo) (any, error) {
		args := jsonx.NewMap[any]()
		if v, err := jsonx.Parse(input); err == nil {
			if o, ok := v.(*jsonx.Object); ok {
				args = o.Clone()
			}
		}
		asked, _ := args.Get("container")
		c, err := ResolveContainer(asked, cls, *scope)
		if err != nil {
			return nil, err
		}
		args.Set("container", c)
		b, err := jsonx.Marshal(args)
		if err != nil {
			return nil, err
		}
		return exec(ctx, b, call)
	}
	return t
}

// TurnScope is turnScope: the turn's scope with the connector's container
// filled in. No scope: the connector's own container, locked.
func TurnScope(turn *connectors.TurnInfo, fallback string) Scope {
	var m *connectors.MemoryScope
	if turn != nil {
		m = turn.Memory
	}
	c := ""
	if m != nil && m.Container != nil {
		c = *m.Container
	}
	if c == "" {
		c = fallback
	}
	s := Scope{Container: c, Read: []string{}, Locked: true}
	if m != nil {
		if m.Read != nil {
			s.Read = m.Read
		}
		s.Locked = m.Locked
	}
	return s
}

func explain(err error) string {
	var re *Error
	if errors.As(err, &re) {
		return re.Message
	}
	return "Can't reach recall: " + err.Error()
}

// Options are what tests inject.
type Options struct {
	Client *http.Client
	// Health and container calls; default 5 s.
	TimeoutMs int64
}

// New is the runtime with injectable HTTP plumbing.
func New(opts Options) *connectors.Runtime {
	timeout := func(ctx context.Context) (context.Context, context.CancelFunc) {
		ms := opts.TimeoutMs
		if ms == 0 {
			ms = 5000
		}
		return context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	}
	base := func(c connectors.Context) context.Context {
		if c.Ctx != nil {
			return c.Ctx
		}
		return context.Background()
	}
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			key := c.Secret("apiKey")
			if key == "" {
				return connectors.TestResult{OK: false, Detail: "Add the recall API key (rcl_...)"}, nil
			}
			cfg := ConfigFrom(c.Config)
			api := NewAPI(cfg.URL, key, opts.Client)
			ctx, cancel := timeout(base(c))
			ok, err := api.Health(ctx)
			cancel()
			if err != nil {
				return connectors.TestResult{OK: false, Detail: explain(err)}, nil
			}
			if !ok {
				return connectors.TestResult{OK: false, Detail: "recall /health says it isn't healthy"}, nil
			}
			ctx, cancel = timeout(base(c))
			list, err := api.Containers(ctx)
			cancel()
			if err != nil {
				return connectors.TestResult{OK: false, Detail: explain(err)}, nil
			}
			n := len(list)
			items := make([]any, n)
			for i, x := range list {
				items[i] = x
			}
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("Connected, %d container%s", n, kit.Plural(n)), Data: kit.O("containers", items)}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			key := c.Secret("apiKey")
			if key == "" {
				return nil, errors.New("Add the recall API key (rcl_...)")
			}
			cfg := ConfigFrom(c.Config)
			api := NewAPI(cfg.URL, key, opts.Client)
			ctx, cancel := timeout(base(c))
			_, err := api.Health(ctx)
			cancel()
			if err != nil {
				return nil, errors.New(explain(err))
			}
			log := c.Log
			if log == nil {
				log = logx.Silent
			}
			conn := mcpconn.NewConnection(mcpconn.Endpoint{URL: api.MCPURL(), Headers: map[string]string{"Authorization": "Bearer " + key}, Client: opts.Client}, 0)
			stats := &Stats{}
			var mu sync.Mutex
			mcpError := ""
			known := cfg.DefaultContainer
			// recall's default container: asked once, kept while it answers.
			// One lookup at a time, so concurrent callers share it.
			var lookup sync.Mutex
			var serverDefault string
			defaultContainer := func(ctx context.Context) (string, error) {
				lookup.Lock()
				defer lookup.Unlock()
				if serverDefault != "" {
					return serverDefault, nil
				}
				ctx, cancel := timeout(ctx)
				defer cancel()
				name, err := api.DefaultContainer(ctx)
				if err != nil {
					return "", err
				}
				serverDefault = name
				mu.Lock()
				patch := known != name
				known = name
				mu.Unlock()
				if patch && c.PatchConfig != nil {
					c.PatchConfig(kit.O("defaultContainer", name))
				}
				return name, nil
			}
			if cfg.Container == "" {
				go func() {
					if _, err := defaultContainer(context.Background()); err != nil {
						log.Warn("recall default container unknown for now", map[string]any{"error": err})
					}
				}()
			}
			setError := func(detail string) {
				mu.Lock()
				changed := detail != mcpError
				mcpError = detail
				mu.Unlock()
				if c.SetStatus == nil || !changed {
					return
				}
				if detail == "" {
					c.SetStatus("on", nil)
				} else {
					c.SetStatus("error", &detail)
				}
			}
			return &connectors.RunningConnector{
				Memory: MemoryHandle(api, cfg, stats),
				Stop:   func(context.Context) error { return conn.Close() },
				Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
					scope := TurnScope(&turn, cfg.Container)
					if scope.Container == "" {
						if name, err := defaultContainer(ctx); err == nil {
							scope.Container = name
						} else {
							log.Warn("recall default container unknown, forget tools refuse until it answers", map[string]any{"error": err})
						}
					}
					set := connectors.NewToolSet()
					set.Set("forget_memory", connectors.PaluiTool{Data: connectors.DataAgent, Tool: ForgetTool(api, &scope), Class: "destructive", Title: "Forget memory"})
					set.Set("forget_document", connectors.PaluiTool{Data: connectors.DataAgent, Tool: ForgetDocumentTool(api, &scope), Class: "destructive", Title: "Delete document"})
					defs, err := conn.ListTools(ctx, false, 0)
					var aiTools map[string]connectors.Tool
					if err == nil {
						aiTools, err = conn.Tools(ctx, defs)
					}
					if err != nil {
						setError("recall MCP: " + err.Error())
						return set, nil
					}
					for _, def := range defs {
						if _, known := ToolClasses[def.Name]; scope.Locked && !known {
							continue
						}
						t, ok := aiTools[def.Name]
						if !ok {
							continue
						}
						cls := ToolClass(def.Name)
						title := def.Title
						if title == "" {
							title = def.Name
						}
						set.Set(def.Name, connectors.PaluiTool{Data: connectors.DataAgent, Tool: withContainer(t, cls, &scope), Class: cls, Title: title})
					}
					if c.DB != nil {
						now := turn.Now
						if now == 0 {
							now = time.Now().UnixMilli()
						}
						down, err := agents.CanReadDown(c.DB, turn, now)
						if err != nil {
							return nil, err
						}
						if down {
							more := ReadDownTools(c.DB, api, aiTools, timeout)
							for _, k := range more.Keys() {
								pt, _ := more.Get(k)
								set.Set(k, pt)
							}
						}
					}
					mu.Lock()
					had := mcpError != ""
					mu.Unlock()
					if had {
						setError("")
					}
					return set, nil
				},
				Live: func() *jsonx.Object {
					stats.mu.Lock()
					s := kit.O("calls", stats.Calls, "hits", stats.Hits, "empty", stats.Empty, "skippedShort", stats.SkippedShort, "timeouts", stats.Timeouts, "errors", stats.Errors)
					rate := 0.0
					if stats.Calls > 0 {
						rate = float64(stats.Timeouts+stats.Errors) / float64(stats.Calls)
					}
					s.Set("skipRate", rate)
					stats.mu.Unlock()
					mu.Lock()
					var e any
					if mcpError != "" {
						e = mcpError
					}
					mu.Unlock()
					var container any = kit.Undef
					if cfg.Container != "" {
						container = cfg.Container
					}
					return kit.O("container", container, "autoRecall", cfg.AutoRecall, "autoSave", cfg.AutoSave, "recall", s, "error", e)
				},
				Action: func(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error) {
					if name != "listContainers" {
						return nil, fmt.Errorf("Unknown action %s", name)
					}
					tctx, cancel := timeout(ctx)
					defer cancel()
					list, err := api.Containers(tctx)
					if err != nil {
						return nil, err
					}
					items := make([]any, len(list))
					for i, x := range list {
						items[i] = x
					}
					return kit.O("containers", items), nil
				},
			}, nil
		},
	}
}

// Runtime is the recall connector runtime.
var Runtime = New(Options{})

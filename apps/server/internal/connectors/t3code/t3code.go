package t3code

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// DefaultURL is DEFAULT_T3_URL.
const DefaultURL = "http://host.docker.internal:3773"

const (
	PollMs       = 20_000
	WatchTTLMs   = 24 * 3600_000
	MaxWatches   = 20
	MaxHistory   = 50
	replyChars   = 1500
	noticeTTLMs  = 24 * 3600_000
	defaultLimit = 10
)

var modelPattern = regexp.MustCompile(`^[^/\s]+/\S+$`)

// ConfigZod is t3Config.
var ConfigZod = zod.Object(
	zod.F("url", zod.Default(zod.String().URL(regexp.MustCompile(`^https?$`)), DefaultURL)),
	zod.F("baseRef", zod.Default(zod.String().Trim().Min(1).Max(200), "main")),
	zod.F("model", zod.String().Trim().Regex(modelPattern, `^[^/\s]+\/\S+$`, "Use provider/model, e.g. claudeAgent/claude-sonnet-5-5").Optional()),
)

var SecretZod = []zod.Field{zod.F("pairingCode", zod.String().Trim().Min(8))}

var Manifest = connectors.Manifest{
	ID:      "t3code",
	Kind:    "tools",
	Name:    "T3 Code",
	Icon:    "square-terminal",
	Summary: "Start coding threads in T3 Code from chat.",
	Trust:   "own",
	Setup: []connectors.SetupStep{
		{Field: "pairingCode", Label: "Pairing code", Secret: true, Help: "Run this on the T3 Code Mac, then paste the code.", Command: "t3 auth pairing create --ttl 10m"},
		{Field: "url", Label: "T3 Code URL", Placeholder: DefaultURL, Help: "In Docker on the same Mac, keep the default."},
		{Field: "baseRef", Label: "Default base branch", Optional: true, Placeholder: "main"},
		{Field: "model", Label: "Model", Optional: true, Placeholder: "claudeAgent/claude-opus-5-5", Help: "Used when a project has no default model."},
	},
	PauseNote:     "While paused, running threads keep going and finish messages wait.",
	RemoveWarning: "This removes Palui's T3 Code token. Running threads keep going.",
	Docs: &connectors.Docs{
		Commands: []string{"t3 auth pairing create --ttl 10m"},
		Notes: []string{
			"Threads run with full access, the same as a thread you start in T3 Code yourself.",
			"The token lasts 30 days and T3 Code can't renew it. When it runs out the connector shows an error; paste a fresh pairing code.",
			"Starting a thread and sending to one are write tools: they run right away in your own chat, and ask first after untrusted content or in a scheduled job.",
		},
	},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

// Config is t3Config's output.
type Config struct {
	URL     string
	BaseRef string
	Model   string
}

// ParseConfig is parseConfig: the stored config with defaults, or the
// defaults when it doesn't parse.
func ParseConfig(raw *jsonx.Object) Config {
	if raw == nil {
		raw = jsonx.NewMap[any]()
	}
	out, err := zod.Parse(ConfigZod, raw)
	if err != nil {
		out, _ = zod.Parse(ConfigZod, jsonx.NewMap[any]())
	}
	o := out.(*jsonx.Object)
	s := func(k string) string { v, _ := o.Get(k); x, _ := v.(string); return x }
	return Config{URL: s("url"), BaseRef: s("baseRef"), Model: s("model")}
}

// Watch is a thread the watcher follows.
type Watch struct {
	ThreadID   string  `json:"threadId"`
	RunID      *string `json:"runId"`
	ChatID     string  `json:"chatId"`
	Title      string  `json:"title"`
	Link       string  `json:"link,omitempty"`
	Since      int64   `json:"since"`
	AskedInput bool    `json:"askedInput,omitempty"`
}

// HistoryEntry is a thread started or followed from a chat.
type HistoryEntry struct {
	ThreadID string `json:"threadId"`
	ChatID   string `json:"chatId"`
	Title    string `json:"title"`
	Link     string `json:"link,omitempty"`
	Action   string `json:"action"`
	At       int64  `json:"at"`
}

type learnedConfig struct {
	PairedCode     string         `json:"pairedCode"`
	TokenExpiresAt *int64         `json:"tokenExpiresAt"`
	Watches        []Watch        `json:"watches"`
	History        []HistoryEntry `json:"history"`
}

// learnedOf reads the runtime-learned keys; a malformed value reads as none.
func learnedOf(raw *jsonx.Object) learnedConfig {
	var l learnedConfig
	if raw == nil {
		return l
	}
	b, err := jsonx.Marshal(raw)
	if err != nil || json.Unmarshal(b, &l) != nil {
		return learnedConfig{}
	}
	return l
}

func ctxOf(c connectors.Context) context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// EnsureToken is ensureToken: the token for this connector, trading the
// pairing code once when needed.
func EnsureToken(c connectors.Context, cfg Config, hc *http.Client, now int64) (string, error) {
	code := strx.Trim(c.Secret("pairingCode"))
	token := c.Secret("token")
	seen := learnedOf(c.Config).PairedCode
	if token != "" && (code == "" || seen == CodeFingerprint(code)) {
		return token, nil
	}
	if code == "" {
		return "", t3err("Paste a pairing code from t3 auth pairing create --ttl 10m.", "no_code")
	}
	paired, err := PairWithCode(ctxOf(c), cfg.URL, code, hc, now)
	if err != nil {
		return "", err
	}
	if c.PatchSecrets != nil {
		p := jsonx.NewMap[string]()
		p.Set("token", paired.Token)
		c.PatchSecrets(p)
	}
	var exp any
	if paired.ExpiresAt != nil {
		exp = *paired.ExpiresAt
	}
	if c.PatchConfig != nil {
		c.PatchConfig(kit.O("pairedCode", CodeFingerprint(code), "tokenExpiresAt", exp))
	}
	if c.Secrets != nil {
		c.Secrets.Set("token", paired.Token)
	}
	if c.Config != nil {
		c.Config.Set("pairedCode", CodeFingerprint(code))
	}
	return paired.Token, nil
}

var unreachableRe = regexp.MustCompile(`(?i)ECONNREFUSED|fetch failed|ENOTFOUND|EHOSTUNREACH|connection refused|no such host|host is unreachable`)

// ErrorText is errorText.
func ErrorText(err error) string {
	if IsAuthError(err) {
		return "T3 Code rejected Palui's token (it lasts 30 days). On the Mac, run t3 auth pairing create --ttl 10m and paste the new code in this connector."
	}
	msg := err.Error()
	if unreachableRe.MatchString(msg) {
		return fmt.Sprintf("Can't reach T3 Code at that URL. Is the app running? (%s)", msg)
	}
	return msg
}

// ThreadIDOf accepts a thread id or a T3 Code thread link
// (…/threads/<env>/<threadId>). Ids of threads started over MCP look like
// "mcp:launch:<hex>" and are percent-encoded in links.
func ThreadIDOf(input string) string {
	s := strx.Trim(input)
	path, ok := urlPath(s)
	if !ok || !strings.Contains(path, "/threads/") {
		return s
	}
	path = strings.TrimRight(path, "/")
	last := path[strings.LastIndex(path, "/")+1:]
	if last == "" {
		return s
	}
	if d, err := url.PathUnescape(last); err == nil {
		return d
	}
	return last
}

// EnvironmentIDOf is the T3 Code environment in a thread link
// (…/threads/<environmentId>/<threadId>); "" when none.
func EnvironmentIDOf(link string) string {
	if link == "" {
		return ""
	}
	path, ok := urlPath(link)
	if !ok {
		return ""
	}
	var parts []string
	for _, p := range strings.Split(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for i, p := range parts {
		if p == "threads" && len(parts) >= i+3 {
			return parts[i+1]
		}
	}
	return ""
}

var urlRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://[^/?#\\]*([^?#]*)`)

// urlPath is URL.parse(s)?.pathname for the absolute URLs a thread link is:
// the path as written (percent escapes kept, valid or not, like WHATWG).
func urlPath(s string) (string, bool) {
	m := urlRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	if m[1] == "" {
		return "/", true
	}
	return m[1], true
}

const linkNote = "Links look like …/threads/<environmentId>/<threadId>. environmentId is the T3 Code install on the owner's Mac and is the same for every thread; threadId is the thread."

func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}

func clip(text string, max int) string {
	t := strx.Trim(text)
	if strx.Len16(t) <= max {
		return t
	}
	return strings.TrimRightFunc(strx.Slice16(t, max-1), strx.IsSpace) + "…"
}

func threadView(t ThreadSummary) *jsonx.Object {
	o := kit.O("id", t.ThreadID, "title", t.Title, "status", t.Status)
	if t.Link != "" {
		o.Set("link", t.Link)
	}
	if env := EnvironmentIDOf(t.Link); env != "" {
		o.Set("environmentId", env)
	}
	if t.Branch != nil && *t.Branch != "" {
		o.Set("branch", *t.Branch)
	}
	if t.PendingRequestCount != 0 {
		o.Set("waitingForAnswer", true)
	}
	if len(t.PullRequests) > 0 {
		var urls []any
		for _, p := range t.PullRequests {
			if p.URL != "" {
				urls = append(urls, p.URL)
			}
		}
		if urls == nil {
			urls = []any{}
		}
		o.Set("pullRequests", urls)
	}
	if t.UpdatedAt != "" {
		o.Set("updatedAt", t.UpdatedAt)
	}
	return o
}

// --- watcher ---------------------------------------------------------------------------

func lastReply(items []Item, runID *string) string {
	var replies, own []Item
	for _, i := range items {
		if i.Type == "assistant_message" && i.Text != nil && *i.Text != "" {
			replies = append(replies, i)
			if runID != nil && i.RunID != nil && *i.RunID == *runID {
				own = append(own, i)
			}
		}
	}
	pick := replies
	if len(own) > 0 {
		pick = own
	}
	if len(pick) == 0 {
		return ""
	}
	return *pick[len(pick)-1].Text
}

// FinishedText is finishedText.
func FinishedText(w Watch, status, reply string) string {
	head := "T3 Code beres: " + w.Title
	if status != "completed" {
		head = fmt.Sprintf("T3 Code berhenti (%s): %s", status, w.Title)
	}
	parts := []string{head}
	if reply != "" {
		parts = append(parts, clip(reply, replyChars))
	}
	if w.Link != "" {
		parts = append(parts, w.Link)
	}
	return strings.Join(parts, "\n\n")
}

// WaitingText is waitingText.
func WaitingText(w Watch) string {
	parts := []string{"T3 Code nunggu jawaban kamu: " + w.Title}
	if w.Link != "" {
		parts = append(parts, w.Link)
	}
	return strings.Join(parts, "\n\n")
}

func notify(q db.Q, chatID, text, source string, now int64) (bool, error) {
	chat, ok, err := chats.Get(q, chatID)
	if err != nil || !ok {
		return false, err
	}
	exp := now + noticeTTLMs
	if _, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chatID, Channel: chat.Channel, Payload: db.OutboxPayload{Text: text}, Source: source, ExpiresAt: &exp}, now); err != nil {
		return false, err
	}
	outbox.Notify()
	return true, nil
}

// Watcher follows threads Palui started and remembers each chat's threads.
// Watches and history live in the connector's config (no restart).
type Watcher struct {
	c        connectors.Context
	client   func() *Client
	onError  func(error)
	mu       sync.Mutex
	watches  []Watch
	history  []HistoryEntry
	polling  bool
	toObject func(v any) any
}

// NewWatcher is createWatcher(ctx, client, onError).
func NewWatcher(c connectors.Context, client func() *Client, onError func(error)) *Watcher {
	l := learnedOf(c.Config)
	return &Watcher{c: c, client: client, onError: onError, watches: l.Watches, history: l.History}
}

func toJSON(v any) any {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return nil
	}
	p, err := jsonx.Parse(b)
	if err != nil {
		return nil
	}
	return p
}

func (w *Watcher) save() {
	if w.c.PatchConfig == nil {
		return
	}
	list := make([]any, len(w.watches))
	for i, x := range w.watches {
		list[i] = toJSON(x)
	}
	w.c.PatchConfig(kit.O("watches", list))
}

// Add starts following a thread (one watch per thread, at most 20).
func (w *Watcher) Add(x Watch, now int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	x.Since = now
	var kept []Watch
	for _, y := range w.watches {
		if y.ThreadID != x.ThreadID {
			kept = append(kept, y)
		}
	}
	kept = append(kept, x)
	if len(kept) > MaxWatches {
		kept = kept[len(kept)-MaxWatches:]
	}
	w.watches = kept
	w.save()
}

// List is the current watches.
func (w *Watcher) List() []Watch {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Watch(nil), w.watches...)
}

// Remember records a thread for a chat (newest last, at most 50).
func (w *Watcher) Remember(e HistoryEntry, now int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	e.At = now
	var kept []HistoryEntry
	for _, y := range w.history {
		if !(y.ThreadID == e.ThreadID && y.ChatID == e.ChatID) {
			kept = append(kept, y)
		}
	}
	kept = append(kept, e)
	if len(kept) > MaxHistory {
		kept = kept[len(kept)-MaxHistory:]
	}
	w.history = kept
	if w.c.PatchConfig != nil {
		list := make([]any, len(kept))
		for i, x := range kept {
			list[i] = toJSON(x)
		}
		w.c.PatchConfig(kit.O("history", list))
	}
}

// History is the chat's entries, oldest first.
func (w *Watcher) History(chatID string) []HistoryEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []HistoryEntry
	for _, e := range w.history {
		if e.ChatID == chatID {
			out = append(out, e)
		}
	}
	return out
}

// Poll checks every watch once.
func (w *Watcher) Poll(ctx context.Context, now int64) {
	w.mu.Lock()
	if w.polling || len(w.watches) == 0 {
		w.mu.Unlock()
		return
	}
	w.polling = true
	current := append([]Watch(nil), w.watches...)
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.polling = false
		w.mu.Unlock()
	}()
	var keep []Watch
	changed := false
	for _, x := range current {
		if now-x.Since > WatchTTLMs {
			if w.c.Event != nil {
				w.c.Event("t3code.watch_expired", fmt.Sprintf("Stopped watching T3 Code thread %s after 24 hours", x.Title), "info", kit.O("threadId", x.ThreadID))
			}
			changed = true
			continue
		}
		next, drop, err := w.check(ctx, x, now)
		if err != nil {
			var te *Error
			if errors.As(err, &te) && te.Code == "thread_not_found" {
				changed = true
				continue
			}
			keep = append(keep, x)
			if w.onError != nil {
				w.onError(err)
			}
			continue
		}
		if drop {
			changed = true
			continue
		}
		if next != x {
			changed = true
		}
		keep = append(keep, next)
	}
	if changed {
		w.mu.Lock()
		w.watches = keep
		w.save()
		w.mu.Unlock()
	}
}

func (w *Watcher) check(ctx context.Context, x Watch, now int64) (Watch, bool, error) {
	c := w.client()
	waited, err := c.Peek(ctx, x.ThreadID, x.RunID)
	if err != nil {
		return x, false, err
	}
	if waited.TimedOut {
		if !x.AskedInput {
			read, err := c.Read(ctx, x.ThreadID, 1, 1)
			if err != nil {
				return x, false, err
			}
			if read.Thread.PendingRequestCount > 0 {
				if _, err := notify(w.c.DB, x.ChatID, WaitingText(x), "t3code:"+x.ThreadID, now); err != nil {
					return x, false, err
				}
				x.AskedInput = true
				return x, false, nil
			}
		}
		return x, false, nil
	}
	reply := ""
	if waited.Status == "completed" {
		read, err := c.Read(ctx, x.ThreadID, 6, replyChars+200)
		if err != nil {
			return x, false, err
		}
		run := waited.RunID
		if run == nil {
			run = x.RunID
		}
		reply = lastReply(read.Items, run)
	}
	if _, err := notify(w.c.DB, x.ChatID, FinishedText(x, waited.Status, reply), "t3code:"+x.ThreadID, now); err != nil {
		return x, false, err
	}
	return x, true, nil
}

// --- tools ---------------------------------------------------------------------------

func fail(msg string) *jsonx.Object { return kit.Fail(msg) }

// ToolsInput is t3Tools' input.
type ToolsInput struct {
	Client    func() *Client
	Config    Config
	Turn      connectors.TurnInfo
	Watcher   *Watcher
	Untrusted bool
	OnError   func(error)
}

var threadInput = zod.String().Min(1).Describe("Thread id, or the thread's T3 Code link")

// Tools is t3Tools.
func Tools(in ToolsInput) *connectors.ToolSet {
	turn, untrusted := in.Turn, in.Untrusted
	guard := func(fn func() (any, error)) (any, error) {
		out, err := fn()
		if err == nil {
			return out, nil
		}
		var te *Error
		if errors.As(err, &te) {
			return fail(te.Message), nil
		}
		if in.OnError != nil {
			in.OnError(err)
		}
		return fail(ErrorText(err)), nil
	}
	set := connectors.NewToolSet()
	set.Set("list_projects", connectors.PaluiTool{
		Class: "read", Untrusted: untrusted, Title: "List T3 Code projects",
		Tool: kit.Tool("List the owner's T3 Code projects (repos on their Mac). Use the name with start_thread.", zod.Object(),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				return guard(func() (any, error) {
					projects, err := in.Client().Projects(ctx)
					if err != nil {
						return nil, err
					}
					list := make([]any, len(projects))
					for i, p := range projects {
						list[i] = kit.O("id", p.ID, "name", p.Title, "path", p.WorkspaceRoot)
					}
					return kit.O("ok", true, "projects", list), nil
				})
			}),
	})
	set.Set("list_threads", connectors.PaluiTool{
		Class: "read", Untrusted: untrusted, Title: "List T3 Code threads",
		Tool: kit.Tool("List recent T3 Code threads, newest first, in one project or across all of them. For the threads started from this chat, use list_chat_threads. "+linkNote,
			zod.Object(
				zod.F("project", zod.String().Optional().Describe("Project name, id or path. Omit for all projects.")),
				zod.F("activeOnly", zod.Bool().Optional().Describe("Only threads that are running, queued or waiting for an answer")),
			),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				return guard(func() (any, error) {
					c := in.Client()
					projectID := ""
					if p := x.S("project"); p != "" {
						projects, err := c.Projects(ctx)
						if err != nil {
							return nil, err
						}
						m, why := MatchProject(projects, p)
						if m == nil {
							return fail(why), nil
						}
						projectID = m.ID
					}
					var statuses []string
					if x.True("activeOnly") {
						statuses = []string{"preparing", "queued", "starting", "running", "waiting"}
					}
					threads, err := c.Threads(ctx, projectID, defaultLimit, statuses)
					if err != nil {
						return nil, err
					}
					list := make([]any, len(threads))
					for i, t := range threads {
						list[i] = threadView(t)
					}
					return kit.O("ok", true, "threads", list), nil
				})
			}),
	})
	set.Set("start_thread", connectors.PaluiTool{
		Class: "write", Untrusted: untrusted, Title: "Start a T3 Code thread",
		Tool: kit.Tool("Start a new T3 Code thread: a coding agent on the owner's Mac works on `task` in the project. By default it runs in a fresh git worktree off the default base branch, so the owner's checkout is untouched. Write `task` as a complete brief, in the owner's words plus any detail from this chat. Palui messages this chat when the thread finishes, so don't promise to check back. Give the owner the returned link.",
			zod.Object(
				zod.F("project", zod.String().Min(1).Describe("Project name, id or path. Call list_projects when unsure.")),
				zod.F("task", zod.String().Min(1).Max(100_000).Describe("The full instructions for the coding agent")),
				zod.F("title", zod.String().Max(80).Optional().Describe("Short thread title. Defaults to the task's first line.")),
				zod.F("baseRef", zod.String().Max(200).Optional().Describe("Branch, tag or commit for the new worktree. Default: "+in.Config.BaseRef)),
				zod.F("inPlace", zod.Bool().Optional().Describe("Work in the project's own checkout instead of a new worktree. Only when the owner asks.")),
			),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				return guard(func() (any, error) {
					c := in.Client()
					projects, err := c.Projects(ctx)
					if err != nil {
						return nil, err
					}
					p, why := MatchProject(projects, x.S("project"))
					if p == nil {
						return fail(why), nil
					}
					task := x.S("task")
					name := strx.Trim(x.S("title"))
					if name == "" {
						name = strings.Split(strx.Trim(task), "\n")[0]
					}
					if name == "" {
						name = "Palui task"
					}
					name = strx.Slice16(name, 80)
					base := ""
					if !x.True("inPlace") {
						base = strx.Trim(x.S("baseRef"))
						if base == "" {
							base = in.Config.BaseRef
						}
					}
					var model *Model
					if p.DefaultModelSelection == nil {
						if model = ParseModel(in.Config.Model); model == nil {
							if model, err = c.DefaultModel(ctx); err != nil {
								return nil, err
							}
						}
					}
					launched, err := c.Launch(ctx, LaunchInput{ProjectID: p.ID, Model: model, Title: name, Message: task, BaseRef: base, ClientRequestID: fmt.Sprintf("palui-%s-%s", turn.RunID, shortHash(p.ID, task))})
					if err != nil {
						return nil, err
					}
					if launched.RunID != nil && *launched.RunID != "" {
						in.Watcher.Add(Watch{ThreadID: launched.ThreadID, RunID: launched.RunID, ChatID: turn.ChatID, Title: name, Link: launched.Link}, time.Now().UnixMilli())
					}
					in.Watcher.Remember(HistoryEntry{ThreadID: launched.ThreadID, ChatID: turn.ChatID, Title: name, Action: "started", Link: launched.Link}, turn.Now)
					o := kit.O("ok", true, "threadId", launched.ThreadID)
					if env := EnvironmentIDOf(launched.Link); env != "" {
						o.Set("environmentId", env)
					}
					workspace := "project checkout"
					if base != "" {
						workspace = "new worktree from " + base
					}
					kit.Spread(o, kit.O("title", name, "project", p.Title, "workspace", workspace))
					if launched.Link != "" {
						o.Set("link", launched.Link)
					}
					note := "The thread was created idle."
					if launched.RunID != nil && *launched.RunID != "" {
						note = "Palui will message this chat when the thread finishes."
					}
					o.Set("note", note)
					return o, nil
				})
			}),
	})
	set.Set("send_to_thread", connectors.PaluiTool{
		Class: "write", Untrusted: untrusted, Title: "Message a T3 Code thread",
		Tool: kit.Tool("Send a follow-up to a T3 Code thread. A running thread gets it as a mid-turn update or its next turn; an idle one starts working. Palui messages this chat when that run finishes.",
			zod.Object(zod.F("thread", threadInput), zod.F("message", zod.String().Min(1).Max(100_000))),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				return guard(func() (any, error) {
					c := in.Client()
					threadID := ThreadIDOf(x.S("thread"))
					message := x.S("message")
					if err := c.Send(ctx, threadID, message, fmt.Sprintf("palui-%s-%s", turn.RunID, shortHash(threadID, message))); err != nil {
						return nil, err
					}
					read, err := c.Read(ctx, threadID, 1, 1)
					if err != nil {
						return nil, err
					}
					t := read.Thread
					runID := t.ActiveRunID
					if runID == nil {
						runID = t.LatestRunID
					}
					in.Watcher.Add(Watch{ThreadID: threadID, RunID: runID, ChatID: turn.ChatID, Title: t.Title, Link: t.Link}, time.Now().UnixMilli())
					in.Watcher.Remember(HistoryEntry{ThreadID: threadID, ChatID: turn.ChatID, Title: t.Title, Action: "followed", Link: t.Link}, turn.Now)
					return kit.O("ok", true, "thread", threadView(t), "note", "Palui will message this chat when it finishes."), nil
				})
			}),
	})
	set.Set("list_chat_threads", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read", Untrusted: untrusted, Title: "List this chat's T3 Code threads",
		Tool: kit.Tool("List the T3 Code threads started or followed up from this chat, newest first, with their thread ids and links. Only this chat's threads. "+linkNote, zod.Object(),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				h := in.Watcher.History(turn.ChatID)
				threads := make([]any, 0, len(h))
				for i := len(h) - 1; i >= 0; i-- {
					e := h[i]
					o := kit.O("threadId", e.ThreadID, "title", e.Title, "action", e.Action, "at", kit.ISO(e.At))
					if e.Link != "" {
						o.Set("link", e.Link)
						if env := EnvironmentIDOf(e.Link); env != "" {
							o.Set("environmentId", env)
						}
					}
					threads = append(threads, o)
				}
				return kit.O("ok", true, "chatId", turn.ChatID, "threads", threads, "message", kit.If(len(threads) == 0, "Belum ada thread T3 Code dari chat ini.")), nil
			}),
	})
	set.Set("get_thread", connectors.PaluiTool{
		Class: "read", Untrusted: untrusted, Title: "Check a T3 Code thread",
		Tool: kit.Tool("Read a T3 Code thread's status, branch, pull requests and its latest messages.", zod.Object(zod.F("thread", threadInput)),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				return guard(func() (any, error) {
					read, err := in.Client().Read(ctx, ThreadIDOf(x.S("thread")), 6, replyChars)
					if err != nil {
						return nil, err
					}
					msgs := []any{}
					for _, i := range read.Items {
						if (i.Type == "user_message" || i.Type == "assistant_message") && i.Text != nil && *i.Text != "" {
							from := "agent"
							if i.Type == "user_message" {
								from = "owner"
							}
							m := kit.O("from", from, "text", *i.Text)
							if i.TextTruncated {
								m.Set("cut", true)
							}
							msgs = append(msgs, m)
						}
					}
					return kit.O("ok", true, "thread", threadView(read.Thread), "messages", msgs), nil
				})
			}),
	})
	set.Set("stop_thread", connectors.PaluiTool{
		Class: "destructive", Untrusted: untrusted, Title: "Stop a T3 Code thread",
		Tool: kit.Tool("Interrupt a T3 Code thread's running turn. Its work so far stays in the worktree.", zod.Object(zod.F("thread", threadInput)),
			func(ctx context.Context, x kit.Input, call connectors.ToolCallInfo) (any, error) {
				return guard(func() (any, error) {
					threadID := ThreadIDOf(x.S("thread"))
					if err := in.Client().Interrupt(ctx, threadID, fmt.Sprintf("palui-%s-stop-%s", turn.RunID, threadID)); err != nil {
						return nil, err
					}
					return kit.O("ok", true, "threadId", threadID), nil
				})
			}),
	})
	return set
}

// --- runtime ---------------------------------------------------------------------------

// Started is startT3's answer: the watcher and the handle.
type Started struct {
	Watcher *Watcher
	Handle  *connectors.RunningConnector
}

// StartOptions are what tests inject.
type StartOptions struct {
	Client *http.Client
	// The watcher's interval; 0 is 20 s.
	PollMs int64
}

// Start is startT3(ctx, token, opts).
func Start(c connectors.Context, token string, opts StartOptions) *Started {
	cfg := ParseConfig(c.Config)
	var mu sync.Mutex
	current := NewClient(cfg.URL, token, opts.Client)
	lastError := ""
	onError := func(err error) {
		detail := ErrorText(err)
		mu.Lock()
		same := detail == lastError
		lastError = detail
		mu.Unlock()
		if !same && c.SetStatus != nil {
			c.SetStatus("error", &detail)
		}
	}
	onOK := func() {
		mu.Lock()
		had := lastError != ""
		lastError = ""
		mu.Unlock()
		if had && c.SetStatus != nil {
			c.SetStatus("on", nil)
		}
	}
	client := func() *Client {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	watcher := NewWatcher(c, client, onError)
	poll := time.Duration(opts.PollMs) * time.Millisecond
	if poll <= 0 {
		poll = PollMs * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				func() {
					defer func() { _ = recover() }()
					watcher.Poll(context.Background(), time.Now().UnixMilli())
				}()
			}
		}
	}()
	untrusted := c.Trust == "external"
	var stopOnce sync.Once
	handle := &connectors.RunningConnector{
		Stop: func(context.Context) error {
			stopOnce.Do(func() {
				ticker.Stop()
				close(done)
			})
			return client().Close()
		},
		Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
			mu.Lock()
			failing := lastError != ""
			mu.Unlock()
			if failing {
				if _, err := client().Projects(ctx); err == nil {
					onOK()
				} else {
					onError(err)
					old := client()
					_ = old.Close()
					mu.Lock()
					current = NewClient(cfg.URL, token, opts.Client)
					mu.Unlock()
				}
			}
			return Tools(ToolsInput{Client: client, Config: cfg, Turn: turn, Watcher: watcher, Untrusted: untrusted, OnError: onError}), nil
		},
		Live: func() *jsonx.Object {
			l := learnedOf(c.Config)
			var exp any
			if l.TokenExpiresAt != nil {
				exp = *l.TokenExpiresAt
			}
			mu.Lock()
			var e any
			if lastError != "" {
				e = lastError
			}
			mu.Unlock()
			return kit.O("url", cfg.URL, "watching", len(watcher.List()), "tokenExpiresAt", exp, "error", e)
		},
	}
	return &Started{Watcher: watcher, Handle: handle}
}

// New is the runtime with injectable HTTP plumbing.
func New(opts StartOptions) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			cfg := ParseConfig(c.Config)
			token, err := EnsureToken(c, cfg, opts.Client, time.Now().UnixMilli())
			if err != nil {
				return connectors.TestResult{OK: false, Detail: ErrorText(err)}, nil
			}
			client := NewClient(cfg.URL, token, opts.Client)
			defer client.Close()
			projects, err := client.Projects(ctxOf(c))
			if err != nil {
				return connectors.TestResult{OK: false, Detail: ErrorText(err)}, nil
			}
			n := len(projects)
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("Connected to T3 Code: %d project%s", n, kit.Plural(n))}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			cfg := ParseConfig(c.Config)
			token, err := EnsureToken(c, cfg, opts.Client, time.Now().UnixMilli())
			if err != nil {
				return nil, errors.New(ErrorText(err))
			}
			probe := NewClient(cfg.URL, token, opts.Client)
			_, err = probe.Projects(ctxOf(c))
			probe.Close()
			if err != nil {
				return nil, errors.New(ErrorText(err))
			}
			return Start(c, token, opts).Handle, nil
		},
	}
}

// Runtime is the t3code connector runtime.
var Runtime = New(StartOptions{})

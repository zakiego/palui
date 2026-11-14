// Package t3code is the T3 Code connector (connectors/src/t3code): pairing
// with a one-time code (OAuth-like, PKCE), tools that start, follow and stop
// coding threads, and a watcher that tells the chat when a thread is done.
package t3code

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Error is T3Error: a message written for an agent, safe to show the model.
type Error struct {
	Message string
	Code    string
}

func (e *Error) Error() string  { return e.Message }
func (e *Error) JSName() string { return "T3Error" }

func t3err(msg, code string) *Error {
	if code == "" {
		code = "t3_error"
	}
	return &Error{msg, code}
}

var authErrRe = regexp.MustCompile(`(?i)\b401\b|unauthori[sz]ed|invalid_mcp_credential`)

// IsAuthError: a 401 from /mcp (the token ran out or was revoked).
func IsAuthError(err error) bool { return err != nil && authErrRe.MatchString(err.Error()) }

// OriginOf is new URL(url).origin.
func OriginOf(raw string) (string, error) {
	u, err := safenet.ParseURL(raw)
	if err != nil {
		return "", err
	}
	return u.Origin(), nil
}

// CodeFingerprint is kept in config so the same code isn't traded twice.
func CodeFingerprint(code string) string {
	sum := sha256.Sum256([]byte(strx.Trim(code)))
	return hex.EncodeToString(sum[:])[:16]
}

// T3 Code's redirect URI must be https or loopback http. Palui never follows
// it; it only reads the code back out of the redirect the decision returns.
const redirectURI = "http://127.0.0.1/palui-t3code"

// PairResult is the token and when it expires (0 when T3 Code didn't say).
type PairResult struct {
	Token     string
	ExpiresAt *int64
}

var b64 = base64.RawURLEncoding

func randomB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b64.EncodeToString(b)
}

// PairWithCode trades a pairing code (t3 auth pairing create) for a
// full-access MCP token: register a client, approve with the code, exchange
// the authorization code. T3 Code ties the token to the origin it was asked
// at, so url must be the address the runtime keeps using.
func PairWithCode(ctx context.Context, rawURL, pairingCode string, hc *http.Client, now int64) (PairResult, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	origin, err := OriginOf(rawURL)
	if err != nil {
		return PairResult{}, err
	}
	resource := origin + "/mcp"
	readJSON := func(res *http.Response, step string) (*jsonx.Object, error) {
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		text := string(raw)
		body, err := jsonx.ParseObject(raw)
		if err != nil || strings.TrimSpace(text) == "" {
			body = jsonx.NewMap[any]()
		}
		if res.StatusCode < 200 || res.StatusCode > 299 {
			why := ""
			for _, k := range []string{"message", "error_description", "error"} {
				if v, ok := body.Get(k); ok && v != nil {
					why = jsString(v)
					break
				}
			}
			if why == "" {
				why = strx.Slice16(text, 200)
			}
			if why == "" {
				why = fmt.Sprintf("HTTP %d", res.StatusCode)
			}
			return nil, t3err(fmt.Sprintf("T3 Code refused the %s: %s", step, why), "pairing_rejected")
		}
		return body, nil
	}
	post := func(path string, body any) (*http.Response, error) {
		b, err := jsonx.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", origin+path, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("content-type", "application/json")
		return hc.Do(req)
	}

	res, err := post("/oauth/mcp/register", obj("client_name", "Palui", "redirect_uris", []any{redirectURI}))
	if err != nil {
		return PairResult{}, err
	}
	reg, err := readJSON(res, "registration")
	if err != nil {
		return PairResult{}, err
	}
	clientID := ""
	if v, ok := reg.Get("client_id"); ok && v != nil {
		clientID = jsString(v)
	}
	if clientID == "" {
		return PairResult{}, t3err("T3 Code registered Palui without a client id. Update T3 Code.", "incompatible")
	}
	verifier := randomB64(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := b64.EncodeToString(sum[:])
	state := randomB64(16)
	res, err = post("/oauth/mcp/decision", obj(
		"authorization", obj("response_type", "code", "client_id", clientID, "redirect_uri", redirectURI, "code_challenge", challenge, "code_challenge_method", "S256", "state", state, "resource", resource),
		"decision", obj("_tag", "pairing-code", "access", "full-access", "code", strx.Trim(pairingCode)),
	))
	if err != nil {
		return PairResult{}, err
	}
	decided, err := readJSON(res, "pairing code")
	if err != nil {
		return PairResult{}, err
	}
	to := ""
	if v, ok := decided.Get("redirectTo"); ok && v != nil {
		to = jsString(v)
	}
	redirect, perr := url.Parse(to)
	ok := perr == nil && to != "" && redirect.Scheme != ""
	code := ""
	if ok {
		code = redirect.Query().Get("code")
	}
	if !ok || code == "" || redirect.Query().Get("state") != state {
		why := "Check the code and make a new one if it's older than its TTL."
		if ok {
			if d := redirect.Query().Get("error_description"); redirect.Query().Has("error_description") {
				why = d
			}
		}
		return PairResult{}, t3err("T3 Code didn't accept the pairing code. "+why, "pairing_rejected")
	}
	form := url.Values{}
	for _, kv := range [][2]string{{"grant_type", "authorization_code"}, {"code", code}, {"redirect_uri", redirectURI}, {"client_id", clientID}, {"code_verifier", verifier}, {"resource", resource}} {
		form.Add(kv[0], kv[1])
	}
	req, err := http.NewRequestWithContext(ctx, "POST", origin+"/oauth/mcp/token", strings.NewReader(encodeOrdered(form, "grant_type", "code", "redirect_uri", "client_id", "code_verifier", "resource")))
	if err != nil {
		return PairResult{}, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	res, err = hc.Do(req)
	if err != nil {
		return PairResult{}, err
	}
	tok, err := readJSON(res, "token exchange")
	if err != nil {
		return PairResult{}, err
	}
	token := ""
	if v, ok := tok.Get("access_token"); ok && v != nil {
		token = jsString(v)
	}
	if token == "" {
		return PairResult{}, t3err("T3 Code answered the token exchange without a token. Update T3 Code.", "incompatible")
	}
	out := PairResult{Token: token}
	if v, ok := tok.Get("expires_in"); ok {
		if f, ok := toNumber(v); ok && !math.IsInf(f, 0) && f > 0 {
			at := now + int64(f*1000)
			out.ExpiresAt = &at
		}
	}
	return out, nil
}

func encodeOrdered(v url.Values, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v.Get(k)))
	}
	return strings.Join(parts, "&")
}

func toNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, !math.IsNaN(t)
	case string:
		f := strx.Number(t)
		return f, !math.IsNaN(f)
	}
	return 0, false
}

func jsString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsonx.Stringify(v)
}

// --- tool results ----------------------------------------------------------------------

type Project struct {
	ID                    string `json:"id"`
	Title                 string `json:"title"`
	WorkspaceRoot         string `json:"workspaceRoot"`
	DefaultModelSelection any    `json:"defaultModelSelection,omitempty"`
	ArchivedAt            any    `json:"archivedAt,omitempty"`
	DeletedAt             any    `json:"deletedAt,omitempty"`
}

type Model struct {
	InstanceID string `json:"instanceId"`
	Model      string `json:"model"`
}

var modelRe = regexp.MustCompile(`^([^/\s]+)/(\S+)$`)

// ParseModel is parseModel: "claudeAgent/claude-sonnet-5-5"; nil when malformed.
func ParseModel(value string) *Model {
	m := modelRe.FindStringSubmatch(strx.Trim(value))
	if m == nil {
		return nil
	}
	return &Model{InstanceID: m[1], Model: m[2]}
}

type PullRequest struct {
	URL string `json:"url"`
}

type ThreadSummary struct {
	ThreadID            string        `json:"threadId"`
	ProjectID           *string       `json:"projectId"`
	Title               string        `json:"title"`
	Link                string        `json:"link"`
	Status              string        `json:"status"`
	LatestRunID         *string       `json:"latestRunId"`
	ActiveRunID         *string       `json:"activeRunId"`
	Branch              *string       `json:"branch"`
	PendingRequestCount float64       `json:"pendingRequestCount"`
	PullRequests        []PullRequest `json:"pullRequests"`
	UpdatedAt           string        `json:"updatedAt"`
}

type Item struct {
	ItemID        string  `json:"itemId"`
	RunID         *string `json:"runId"`
	Type          string  `json:"type"`
	Text          *string `json:"text"`
	TextTruncated bool    `json:"textTruncated"`
}

type ThreadRead struct {
	Thread ThreadSummary `json:"thread"`
	Items  []Item        `json:"items"`
}

type Launch struct {
	ThreadID  string  `json:"threadId"`
	ProjectID string  `json:"projectId"`
	RunID     *string `json:"runId"`
	Link      string  `json:"link"`
}

type Wait struct {
	ThreadID string  `json:"threadId"`
	RunID    *string `json:"runId"`
	Status   string  `json:"status"`
	TimedOut bool    `json:"timedOut"`
}

// Client is T3Client: T3 Code's orchestration tools on its /mcp endpoint.
type Client struct {
	Conn *mcpconn.Connection
}

// NewClient talks to <origin>/mcp with the token, on the legacy protocol.
func NewClient(rawURL, token string, hc *http.Client) *Client {
	origin, _ := OriginOf(rawURL)
	return &Client{Conn: mcpconn.NewConnection(mcpconn.Endpoint{URL: origin + "/mcp", Headers: map[string]string{"Authorization": "Bearer " + token}, ClientName: "palui", LegacyProtocol: true, Client: hc}, 0)}
}

func (c *Client) Close() error { return c.Conn.Close() }

// Call is call<T>: one tool call; a tool-level failure (isError) becomes an
// *Error with the server's message.
func (c *Client) Call(ctx context.Context, tool string, args *jsonx.Object, out any) error {
	res, err := c.Conn.CallTool(ctx, tool, args)
	if err != nil {
		return err
	}
	text := ""
	if raw, ok := res.Get("content"); ok {
		list, _ := raw.([]any)
		for _, x := range list {
			if o, ok := x.(*jsonx.Object); ok {
				if t, ok := o.Get("text"); ok {
					if s, ok := t.(string); ok {
						text = s
						break
					}
				}
			}
		}
	}
	if v, _ := res.Get("isError"); v == true {
		message, code := text, "t3_error"
		if message == "" {
			message = tool + " failed"
		}
		var parsed struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal([]byte(text), &parsed) == nil {
			if parsed.Message != "" {
				message = parsed.Message
			}
			if parsed.Code != "" {
				code = parsed.Code
			}
		}
		return t3err(message, code)
	}
	var data []byte
	if sc, ok := res.Get("structuredContent"); ok {
		if data, err = jsonx.Marshal(sc); err != nil {
			return err
		}
	} else {
		data = []byte(text)
	}
	if json.Unmarshal(data, out) != nil {
		return t3err(fmt.Sprintf("T3 Code sent an unexpected %s result. Update T3 Code or Palui.", tool), "incompatible")
	}
	return nil
}

// Projects lists live projects (10 pages of 100).
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	var cursor any = float64(0)
	for page := 0; page < 10 && cursor != nil; page++ {
		var res struct {
			Projects   []Project `json:"projects"`
			NextCursor any       `json:"nextCursor"`
		}
		if err := c.Call(ctx, "t3_project_list", obj("cursor", cursor, "limit", 100), &res); err != nil {
			return nil, err
		}
		out = append(out, res.Projects...)
		cursor = res.NextCursor
	}
	live := []Project{}
	for _, p := range out {
		if !truthy(p.ArchivedAt) && !truthy(p.DeletedAt) {
			live = append(live, p)
		}
	}
	return live, nil
}

func truthy(v any) bool { return jsonx.Truthy(v) }

// Threads is t3_thread_list.
func (c *Client) Threads(ctx context.Context, projectID string, limit int, statuses []string) ([]ThreadSummary, error) {
	args := jsonx.NewMap[any]()
	if projectID != "" {
		args.Set("projectId", projectID)
	} else {
		args.Set("allProjects", true)
	}
	if len(statuses) > 0 {
		list := make([]any, len(statuses))
		for i, s := range statuses {
			list[i] = s
		}
		args.Set("statuses", list)
	}
	args.Set("includeSubagents", false)
	args.Set("limit", limit)
	var res struct {
		Threads []ThreadSummary `json:"threads"`
	}
	err := c.Call(ctx, "t3_thread_list", args, &res)
	return res.Threads, err
}

// DefaultModel is the first usable provider's first model.
func (c *Client) DefaultModel(ctx context.Context) (*Model, error) {
	var caps struct {
		Providers []struct {
			ProviderInstanceID string `json:"providerInstanceId"`
			Models             []struct {
				ID string `json:"id"`
			} `json:"models"`
			Constraints []string `json:"constraints"`
		} `json:"providers"`
	}
	if err := c.Call(ctx, "orchestrator_capabilities", jsonx.NewMap[any](), &caps); err != nil {
		return nil, err
	}
	for _, p := range caps.Providers {
		if len(p.Models) > 0 && len(p.Constraints) == 0 {
			return &Model{InstanceID: p.ProviderInstanceID, Model: p.Models[0].ID}, nil
		}
	}
	return nil, t3err("T3 Code has no provider with a model turned on. Enable one in T3 Code's settings.", "no_model")
}

// LaunchInput is launch's input.
type LaunchInput struct {
	ProjectID, Title, Message, BaseRef, ClientRequestID string
	Model                                               *Model
}

// Launch is t3_thread_launch.
func (c *Client) Launch(ctx context.Context, in LaunchInput) (Launch, error) {
	args := obj("projectId", in.ProjectID)
	if in.Model != nil {
		args.Set("modelSelection", obj("instanceId", in.Model.InstanceID, "model", in.Model.Model))
	}
	args.Set("title", in.Title)
	args.Set("message", in.Message)
	args.Set("clientRequestId", in.ClientRequestID)
	if in.BaseRef != "" {
		args.Set("workspaceStrategy", obj("type", "worktree", "baseRef", in.BaseRef))
	}
	var out Launch
	err := c.Call(ctx, "t3_thread_launch", args, &out)
	return out, err
}

// Send is t3_thread_send.
func (c *Client) Send(ctx context.Context, threadID, message, clientRequestID string) error {
	var out any
	return c.Call(ctx, "t3_thread_send", obj("threadId", threadID, "message", message, "mode", "auto", "clientRequestId", clientRequestID), &out)
}

// Read is t3_thread_read.
func (c *Client) Read(ctx context.Context, threadID string, limit, maxChars int) (ThreadRead, error) {
	var out ThreadRead
	err := c.Call(ctx, "t3_thread_read", obj("threadId", threadID, "view", "messages", "limit", limit, "runLimit", 3, "maxCharsPerItem", maxChars), &out)
	return out, err
}

// Peek returns at once with timedOut when the run is still going.
func (c *Client) Peek(ctx context.Context, threadID string, runID *string) (Wait, error) {
	args := obj("threadId", threadID)
	if runID != nil && *runID != "" {
		args.Set("runId", *runID)
	}
	args.Set("timeoutMs", 1)
	var out Wait
	err := c.Call(ctx, "t3_thread_wait", args, &out)
	return out, err
}

// Interrupt is t3_thread_interrupt.
func (c *Client) Interrupt(ctx context.Context, threadID, clientRequestID string) error {
	var out any
	return c.Call(ctx, "t3_thread_interrupt", obj("threadId", threadID, "reason", "Stopped from Palui", "clientRequestId", clientRequestID), &out)
}

// MatchProject picks a project from an id, a name, a repo name or a path.
func MatchProject(projects []Project, query string) (*Project, string) {
	q := strings.ToLower(strx.Trim(query))
	if q == "" {
		return nil, "Name a project."
	}
	base := func(p Project) string {
		root := strings.TrimRight(p.WorkspaceRoot, "/")
		parts := strings.Split(root, "/")
		return strings.ToLower(parts[len(parts)-1])
	}
	var exact []Project
	for _, p := range projects {
		if strings.ToLower(p.ID) == q || strings.ToLower(p.Title) == q || base(p) == q || strings.ToLower(p.WorkspaceRoot) == q {
			exact = append(exact, p)
		}
	}
	if len(exact) == 1 {
		return &exact[0], ""
	}
	loose := exact
	if len(loose) == 0 {
		for _, p := range projects {
			if strings.Contains(strings.ToLower(p.Title), q) || strings.Contains(base(p), q) {
				loose = append(loose, p)
			}
		}
	}
	if len(loose) == 1 {
		return &loose[0], ""
	}
	pool := loose
	if len(pool) == 0 {
		pool = projects
	}
	var names []string
	for i, p := range pool {
		if i >= 15 {
			break
		}
		names = append(names, p.Title)
	}
	if len(loose) > 1 {
		return nil, fmt.Sprintf("\"%s\" matches %d projects: %s. Ask which one.", query, len(loose), strings.Join(names, ", "))
	}
	return nil, fmt.Sprintf("No T3 Code project matches \"%s\". Projects: %s.", query, strings.Join(names, ", "))
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

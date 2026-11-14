// Package recall is the recall memory connector (connectors/src/recall): a
// small client for recall's REST API, auto-recall and auto-save, recall's own
// MCP tools with the container locked to the turn's scope, and REST forget
// tools.
package recall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Error is RecallError: Status 0 when recall couldn't be reached.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string  { return e.Message }
func (e *Error) JSName() string { return "RecallError" }

type Container struct {
	Container    string `json:"container"`
	Memories     int64  `json:"memories"`
	Documents    int64  `json:"documents"`
	LastActivity *int64 `json:"lastActivity"`
}

type Memory struct {
	ID        string `json:"id"`
	Fact      string `json:"fact"`
	Kind      string `json:"kind,omitempty"`
	Container string `json:"container,omitempty"`
}

type SearchResult struct {
	Type  string   `json:"type"`
	ID    string   `json:"id"`
	Text  string   `json:"text"`
	Score *float64 `json:"score,omitempty"`
}

type Profile struct {
	Container       string         `json:"container"`
	Static          string         `json:"static"`
	Dynamic         string         `json:"dynamic"`
	UpdatedAt       *int64         `json:"updatedAt"`
	StaticMemories  []Memory       `json:"staticMemories"`
	DynamicMemories []Memory       `json:"dynamicMemories"`
	SearchResults   []SearchResult `json:"searchResults,omitempty"`
}

// MaxSourceID: recall caps source_id at 200 characters.
const MaxSourceID = 200

// NormalizeSourceID hashes ids over 200 characters so they stay unique.
func NormalizeSourceID(id string) string {
	t := strx.Trim(id)
	if strx.Len16(t) <= MaxSourceID {
		return t
	}
	sum := sha256.Sum256([]byte(t))
	return "palui:" + hex.EncodeToString(sum[:])
}

// API is RecallApi.
type API struct {
	Base   string
	key    string
	client *http.Client
}

// NewAPI strips trailing slashes from url. client nil is http.DefaultClient.
func NewAPI(rawURL, apiKey string, client *http.Client) *API {
	if client == nil {
		client = http.DefaultClient
	}
	return &API{Base: strings.TrimRight(rawURL, "/"), key: apiKey, client: client}
}

// MCPURL is <base>/mcp.
func (a *API) MCPURL() string { return a.Base + "/mcp" }

// IsTimeout reports an aborted or timed-out call (TS AbortError/TimeoutError).
func IsTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func (a *API) call(ctx context.Context, method, path string, body any, auth bool, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := jsonx.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+path, rdr)
	if err != nil {
		return &Error{0, fmt.Sprintf("Can't reach recall at %s: %s", a.Base, err.Error())}
	}
	req.Header.Set("accept", "application/json")
	if auth {
		req.Header.Set("authorization", "Bearer "+a.key)
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		if IsTimeout(err) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		return &Error{0, fmt.Sprintf("Can't reach recall at %s: %s", a.Base, err.Error())}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	text := string(raw)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		msg := ""
		var data struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &data) == nil && len(data.Error) > 0 {
			var s string
			var o struct {
				Message *string `json:"message"`
			}
			if json.Unmarshal(data.Error, &s) == nil {
				msg = s
			} else if json.Unmarshal(data.Error, &o) == nil && o.Message != nil {
				msg = *o.Message
			}
		}
		if msg == "" {
			msg = strx.Slice16(text, 200)
			if msg == "" {
				msg = http.StatusText(res.StatusCode)
			}
		}
		if res.StatusCode == 401 {
			return &Error{401, "recall rejected the API key (401)"}
		}
		return &Error{res.StatusCode, fmt.Sprintf("recall answered %d: %s", res.StatusCode, msg)}
	}
	if out != nil && strings.TrimSpace(text) != "" {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil
		}
	}
	return nil
}

// Health is GET /health (no auth); ok reports its ok field.
func (a *API) Health(ctx context.Context) (bool, error) {
	var res struct {
		OK bool `json:"ok"`
	}
	err := a.call(ctx, "GET", "/health", nil, false, &res)
	return res.OK, err
}

// Containers is GET /v1/containers.
func (a *API) Containers(ctx context.Context) ([]Container, error) {
	var res struct {
		Containers []Container `json:"containers"`
	}
	if err := a.call(ctx, "GET", "/v1/containers", nil, true, &res); err != nil {
		return nil, err
	}
	if res.Containers == nil {
		res.Containers = []Container{}
	}
	return res.Containers, nil
}

// Profile is GET /v1/profile; container "" means recall's own default.
func (a *API) Profile(ctx context.Context, container, q string) (Profile, error) {
	var params []string
	if container != "" {
		params = append(params, "container="+formEncode(container))
	}
	if q != "" {
		params = append(params, "q="+formEncode(strx.Slice16(q, 2000)))
	}
	query := ""
	if len(params) > 0 {
		query = "?" + strings.Join(params, "&")
	}
	var p Profile
	err := a.call(ctx, "GET", "/v1/profile"+query, nil, true, &p)
	return p, err
}

// formEncode is URLSearchParams' encoding (space as +).
func formEncode(s string) string { return url.QueryEscape(s) }

// DefaultContainer is the name recall gives its default container.
func (a *API) DefaultContainer(ctx context.Context) (string, error) {
	p, err := a.Profile(ctx, "", "")
	if err != nil {
		return "", err
	}
	name := strx.Trim(p.Container)
	if name == "" {
		return "", &Error{0, "recall didn't say which container is its default"}
	}
	return name, nil
}

// AddMemory is POST /v1/memories.
func (a *API) AddMemory(ctx context.Context, container, content, title, sourceID string) error {
	body := jsonx.NewMap[any]()
	if container != "" {
		body.Set("container", container)
	}
	body.Set("content", content)
	if title != "" {
		body.Set("title", strx.Slice16(title, 300))
	}
	if sourceID != "" {
		body.Set("source_id", NormalizeSourceID(sourceID))
	}
	return a.call(ctx, "POST", "/v1/memories", body, true, nil)
}

// GetMemory is GET /v1/memories/:id.
func (a *API) GetMemory(ctx context.Context, id string) (*Memory, error) {
	var res struct {
		Memory *Memory `json:"memory"`
	}
	err := a.call(ctx, "GET", "/v1/memories/"+url.PathEscape(id), nil, true, &res)
	return res.Memory, err
}

// Forget is DELETE /v1/memories/:id.
func (a *API) Forget(ctx context.Context, id string) (*Memory, error) {
	var res struct {
		Memory *Memory `json:"memory"`
	}
	err := a.call(ctx, "DELETE", "/v1/memories/"+url.PathEscape(id), nil, true, &res)
	return res.Memory, err
}

type Document struct {
	ID        string  `json:"id"`
	Container string  `json:"container"`
	Title     *string `json:"title"`
}

// GetDocument is GET /v1/documents/:id.
func (a *API) GetDocument(ctx context.Context, id string) (*Document, error) {
	var res struct {
		Document *Document `json:"document"`
	}
	err := a.call(ctx, "GET", "/v1/documents/"+url.PathEscape(id), nil, true, &res)
	return res.Document, err
}

// DeleteDocument is DELETE /v1/documents/:id.
func (a *API) DeleteDocument(ctx context.Context, id string) error {
	return a.call(ctx, "DELETE", "/v1/documents/"+url.PathEscape(id), nil, true, nil)
}

const maxProfileChars = 4000

// ProfileText is profileText: the profile for the prompt, ok false when recall
// knows nothing useful.
func ProfileText(p Profile, about string) (string, bool) {
	if about == "" {
		about = "the owner"
	}
	var parts []string
	stat, dyn := strx.Trim(p.Static), strx.Trim(p.Dynamic)
	facts := func(ms []Memory) string {
		var lines []string
		for i, m := range ms {
			if i >= 10 {
				break
			}
			lines = append(lines, "- "+m.Fact)
		}
		return strings.Join(lines, "\n")
	}
	if stat != "" {
		parts = append(parts, fmt.Sprintf("About %s: %s", about, stat))
	} else if len(p.StaticMemories) > 0 {
		parts = append(parts, fmt.Sprintf("Facts about %s:\n%s", about, facts(p.StaticMemories)))
	}
	if dyn != "" {
		parts = append(parts, "Lately: "+dyn)
	} else if len(p.DynamicMemories) > 0 {
		parts = append(parts, "Recent facts:\n"+facts(p.DynamicMemories))
	}
	var hits []string
	for _, r := range p.SearchResults {
		if t := strx.Trim(r.Text); t != "" && len(hits) < 8 {
			hits = append(hits, "- "+t)
		}
	}
	if len(hits) > 0 {
		parts = append(parts, "Memories related to this message:\n"+strings.Join(hits, "\n"))
	}
	if len(parts) == 0 {
		return "", false
	}
	text := strings.Join(parts, "\n\n")
	if strx.Len16(text) > maxProfileChars {
		return strx.Slice16(text, maxProfileChars-3) + "...", true
	}
	return text, true
}

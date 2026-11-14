// Package hooks ports packages/core/src/hooks: inbound webhooks. Each hook has
// a random URL slug and its own HMAC secret. `POST /api/hooks/:slug` is handled
// in this order: refuse a Content-Length over 256 KB, look the slug up
// (Lookup), read the body with a hard cap (ReadBody), then verify the signature
// over the raw bytes (VerifySignature). Ingest then writes a `pending` message
// into the hook's chat whose content is `{ text, hook: { id, name, mode } }`.
//
// Signing: x-palui-timestamp is the unix time in seconds, and x-palui-signature
// is `sha256=<hex HMAC-SHA256 of "<timestamp>.<raw body>">`. Timestamps more
// than 5 minutes off are refused, and a signature is accepted once.
package hooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	SignatureHeader = "x-palui-signature"
	TimestampHeader = "x-palui-timestamp"
	// How far a request's timestamp may be from now, either way.
	MaxSkewMs = 5 * 60_000
	// The hooks skill connector. Pausing it makes every hook answer 503.
	ConnectorID   = "hooks"
	MaxBodyBytes  = 256 * 1024
	MaxTextChars  = 8000
	ModeForward   = "forward"
	ModeSummarize = "summarize"
	ModeFlow      = "flow"
)

// View is what the dashboard gets: never the secret, not even encrypted.
type View struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	ChatID    string `json:"chatId"`
	Enabled   bool   `json:"enabled"`
	LastAt    *int64 `json:"lastAt"`
	CreatedAt int64  `json:"createdAt"`
}

func view(h db.Hook) View {
	return View{ID: h.ID, Slug: h.Slug, Name: h.Name, Mode: h.Mode, ChatID: h.ChatID, Enabled: h.Enabled, LastAt: h.LastAt, CreatedAt: h.CreatedAt}
}

func newSecret() string { return "phk_" + secret.RandomToken(32) }

type CreateInput struct {
	Name   string
	Mode   string
	ChatID string
}

// Created is a new hook plus its secret, shown once.
type Created struct {
	Hook   View   `json:"hook"`
	Secret string `json:"secret"`
}

func Create(q db.Q, in CreateInput, masterKey string, now int64) (Created, error) {
	name := strx.Trim(in.Name)
	if name == "" {
		return Created{}, errors.New("Hook name is required")
	}
	var n int
	if err := q.QueryRow("SELECT count(*) FROM chats WHERE id = ?", in.ChatID).Scan(&n); err != nil {
		return Created{}, err
	}
	if n == 0 {
		return Created{}, fmt.Errorf("No chat %s", in.ChatID)
	}
	sec := newSecret()
	enc, err := secret.Encrypt(sec, masterKey)
	if err != nil {
		return Created{}, err
	}
	// 16 url-safe characters, 96 bits: unguessable, and short enough to paste.
	row, err := db.ScanHook(q.WQueryRow("INSERT INTO hooks (id, slug, name, secret, mode, chat_id, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?) RETURNING "+db.HookCols,
		secret.NewID("hook"), secret.RandomToken(12), name, enc, in.Mode, in.ChatID, now))
	if err != nil {
		return Created{}, err
	}
	return Created{Hook: view(row), Secret: sec}, nil
}

func RotateSecret(q db.Q, id, masterKey string) (Created, error) {
	sec := newSecret()
	enc, err := secret.Encrypt(sec, masterKey)
	if err != nil {
		return Created{}, err
	}
	row, ok, err := db.One(q.WQueryRow("UPDATE hooks SET secret = ? WHERE id = ? RETURNING "+db.HookCols, enc, id), db.ScanHook)
	if err != nil {
		return Created{}, err
	}
	if !ok {
		return Created{}, fmt.Errorf("No hook %s", id)
	}
	return Created{Hook: view(row), Secret: sec}, nil
}

func List(q db.Q) ([]View, error) {
	rows, err := q.Query("SELECT " + db.HookCols + " FROM hooks ORDER BY created_at DESC")
	all, err := db.All(rows, err, db.ScanHook)
	if err != nil {
		return nil, err
	}
	out := make([]View, len(all))
	for i, h := range all {
		out[i] = view(h)
	}
	return out, nil
}

func GetBySlug(q db.Q, slug string) (db.Hook, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.HookCols+" FROM hooks WHERE slug = ?", slug), db.ScanHook)
}

func SetEnabled(q db.Q, id string, enabled bool) (View, bool, error) {
	row, ok, err := db.One(q.WQueryRow("UPDATE hooks SET enabled = ? WHERE id = ? RETURNING "+db.HookCols, db.Bool(enabled), id), db.ScanHook)
	return view(row), ok, err
}

func Delete(q db.Q, id string) (bool, error) {
	n, err := db.Changes(q.Exec("DELETE FROM hooks WHERE id = ?", id))
	return n > 0, err
}

// Sign is the x-palui-signature value: `sha256=<hex HMAC-SHA256>` over the
// timestamp (as sent), a ".", and the raw body bytes.
func Sign(secretText, timestamp string, rawBody []byte) string {
	mac := hmac.New(sha256.New, []byte(secretText))
	mac.Write([]byte(timestamp + "."))
	mac.Write(rawBody)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Failure is a refusal with the HTTP status and a sentence the app returns as is.
type Failure struct {
	Status int
	Error  string
}

type Headers struct {
	Signature *string
	Timestamp *string
}

// Lookup is step 1, before the body is read: the hook exists and is on. A
// paused hooks connector answers 503 for every hook.
func Lookup(q db.Q, slug string) (db.Hook, *Failure, error) {
	hook, ok, err := GetBySlug(q, slug)
	if err != nil {
		return hook, nil, err
	}
	if !ok {
		return hook, &Failure{404, "No such hook"}, nil
	}
	var enabled bool
	err = q.QueryRow("SELECT enabled FROM connectors WHERE id = ?", ConnectorID).Scan(&enabled)
	if err == nil && !enabled {
		return hook, &Failure{503, "Hooks are paused"}, nil
	}
	if !hook.Enabled {
		return hook, &Failure{503, "This hook is turned off"}, nil
	}
	return hook, nil, nil
}

var digitsOnly = regexp.MustCompile(`^\d+$`)

// ReadBody is step 2: read the body as bytes and stop as soon as it passes max
// (MaxBodyBytes when 0), so a huge upload is never buffered whole.
func ReadBody(r *http.Request, max int) ([]byte, *Failure) {
	if max == 0 {
		max = MaxBodyBytes
	}
	tooLarge := &Failure{413, "Body is too large"}
	if length := r.Header.Get("content-length"); length != "" || len(r.Header.Values("content-length")) > 0 {
		t := strx.Trim(length)
		n, err := strconv.ParseFloat(t, 64)
		if !digitsOnly.MatchString(t) || err != nil || n > float64(max) {
			return nil, tooLarge
		}
	}
	if r.Body == nil {
		return []byte{}, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, int64(max)+1))
	if err != nil {
		return nil, &Failure{400, err.Error()}
	}
	if len(b) > max {
		return nil, tooLarge
	}
	return b, nil
}

// BodyText is the body as text for Ingest (invalid UTF-8 becomes U+FFFD).
func BodyText(body []byte) string { return strx.TextDecode(body) }

// Signatures accepted in the last few minutes, per hook: a captured request
// can't be replayed while its timestamp is still inside the window.
var (
	seenMu sync.Mutex
	seen   = map[string]int64{}
)

func claimSignature(key string, now int64) bool {
	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seen) > 1000 {
		for k, exp := range seen {
			if exp <= now {
				delete(seen, k)
			}
		}
		if len(seen) > 5000 {
			clear(seen)
		}
	}
	if exp, ok := seen[key]; ok && exp > now {
		return false
	}
	seen[key] = now + 2*MaxSkewMs
	return true
}

var timestampRe = regexp.MustCompile(`^\d{1,12}$`)

// VerifySignature is step 3: size, timestamp and signature over the raw bytes.
// The signature check is timing safe. A request that passes is recorded, so the
// same signature is refused if it comes again.
func VerifySignature(hook db.Hook, rawBody []byte, h Headers, masterKey string, now int64) *Failure {
	if len(rawBody) > MaxBodyBytes {
		return &Failure{413, "Body is too large"}
	}
	if h.Timestamp == nil || *h.Timestamp == "" {
		return &Failure{401, "Missing " + TimestampHeader + " header (unix seconds)"}
	}
	ts := strx.Trim(*h.Timestamp)
	if !timestampRe.MatchString(ts) {
		return &Failure{401, TimestampHeader + " must be unix time in seconds"}
	}
	secs, _ := strconv.ParseInt(ts, 10, 64)
	skew := now - secs*1000
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxSkewMs {
		return &Failure{401, TimestampHeader + " is more than 5 minutes off. Sign each request when you send it."}
	}
	if h.Signature == nil || *h.Signature == "" {
		return &Failure{401, "Missing " + SignatureHeader + " header"}
	}
	sec, err := secret.Decrypt(hook.Secret, masterKey)
	if err != nil {
		return &Failure{500, "Hook secret can't be read (MASTER_KEY changed?)"}
	}
	expected := Sign(sec, ts, rawBody)
	got := strings.ToLower(strx.Trim(*h.Signature))
	if !secret.SafeEqual(got, expected) {
		return &Failure{401, "Bad signature"}
	}
	if !claimSignature(hook.ID+":"+expected, now) {
		return &Failure{409, "This request was already received"}
	}
	return nil
}

// VerifyRequest is steps 1 and 3 together, for callers that hold the whole body.
func VerifyRequest(q db.Q, slug string, rawBody []byte, h Headers, masterKey string, now int64) (db.Hook, *Failure, error) {
	hook, fail, err := Lookup(q, slug)
	if err != nil || fail != nil {
		return hook, fail, err
	}
	return hook, VerifySignature(hook, rawBody, h, masterKey, now), nil
}

// --- Payload to text -----------------------------------------------------------------

const maxDepth = 6

func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return strx.Trim(strx.CollapseSpace(t))
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return jsonx.NumberString(t)
	}
	return fmt.Sprint(v)
}

func isScalar(v any) bool {
	switch v.(type) {
	case nil, string, float64, bool:
		return true
	}
	return false
}

func render(value any, indent string, depth int, out *[]string) {
	if depth > maxDepth {
		*out = append(*out, indent+"...")
		return
	}
	switch t := value.(type) {
	case []any:
		for _, item := range t {
			if isScalar(item) {
				*out = append(*out, indent+"- "+scalar(item))
			} else {
				*out = append(*out, indent+"-")
				render(item, indent+"  ", depth+1, out)
			}
		}
		return
	case *jsonx.Object:
		for _, k := range t.Keys() {
			v, _ := t.Get(k)
			if isScalar(v) {
				*out = append(*out, indent+k+": "+scalar(v))
				continue
			}
			if arr, ok := v.([]any); ok && allScalar(arr) {
				parts := make([]string, len(arr))
				for i, x := range arr {
					parts[i] = scalar(x)
				}
				*out = append(*out, indent+k+": "+strings.Join(parts, ", "))
				continue
			}
			*out = append(*out, indent+k+":")
			render(v, indent+"  ", depth+1, out)
		}
		return
	}
	*out = append(*out, indent+scalar(value))
}

func allScalar(arr []any) bool {
	for _, x := range arr {
		if !isScalar(x) {
			return false
		}
	}
	return true
}

// SummarizePayload renders a hook payload: JSON becomes indented `key: value`
// lines, anything else is passed through. Capped at MaxTextChars.
func SummarizePayload(name, rawBody string) string {
	var body string
	if parsed, err := jsonx.Parse([]byte(rawBody)); err == nil {
		var lines []string
		render(parsed, "", 0, &lines)
		body = strings.Join(lines, "\n")
	} else {
		body = strx.Trim(rawBody)
	}
	if body == "" {
		body = "(empty body)"
	}
	text := "Webhook " + name + ":\n" + body
	if strx.Len16(text) <= MaxTextChars {
		return text
	}
	const marker = "\n[truncated]"
	return strx.Slice16(text, MaxTextChars-strx.Len16(marker)) + marker
}

type hookRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// Content is the stored message content of an ingested hook.
type Content struct {
	Text string  `json:"text"`
	Hook hookRef `json:"hook"`
}

// Ingest inserts the event as a pending inbound message in the hook's chat.
func Ingest(q db.Q, hook db.Hook, rawBody string, now int64) (db.Message, error) {
	text := SummarizePayload(hook.Name, rawBody)
	content, err := db.Raw(Content{Text: text, Hook: hookRef{ID: hook.ID, Name: hook.Name, Mode: hook.Mode}})
	if err != nil {
		return db.Message{}, err
	}
	var out db.Message
	err = q.Tx(func(tx db.Q) error {
		var sessionID *string
		err := tx.QueryRow("SELECT session_id FROM chats WHERE id = ?", hook.ChatID).Scan(&sessionID)
		if err != nil {
			return fmt.Errorf("Hook %s points at a missing chat %s", hook.ID, hook.ChatID)
		}
		row, err := db.ScanMessage(tx.WQueryRow("INSERT INTO messages (id, chat_id, session_id, role, content, text, sender_id, status, created_at) VALUES (?, ?, ?, 'user', ?, ?, ?, 'pending', ?) RETURNING "+db.MessageCols,
			secret.NewID("msg"), hook.ChatID, sessionID, content, text, "hook:"+hook.Slug, now))
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE hooks SET last_at = ? WHERE id = ?", now, hook.ID); err != nil {
			return err
		}
		out = row
		return nil
	})
	return out, err
}

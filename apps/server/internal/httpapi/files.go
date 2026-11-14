package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// --- file responses (file-response.ts) -------------------------------------------------

// encodeURIComponent is JS encodeURIComponent.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)>>4, 16)) + strings.ToUpper(strconv.FormatInt(int64(c)&15, 16)))
	}
	return b.String()
}

// asciiName is name.replace(/[^\x20-\x7e]/g, "_").replace(/"/g, ""): one "_"
// per UTF-16 unit, so an astral character becomes two.
func asciiName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"':
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		case r > 0xffff:
			b.WriteString("__")
		case r == utf8.RuneError:
			b.WriteString("_")
		default:
			b.WriteString("_")
		}
	}
	return b.String()
}

// fileResponse writes a saved attachment's bytes. A download is always safe.
// Inline is only for what PreviewKind allows, always with nosniff, so a file
// can never render as an HTML page on the dashboard's origin. Text goes out
// as text/plain whatever it was saved as. Images and text get a sandbox CSP;
// PDFs can't (Chrome's viewer shows a blank page under sandbox).
func fileResponse(w http.ResponseWriter, meta db.AttachmentMeta, b []byte, inline bool, cache string) {
	name := attachments.DownloadName(meta.Filename, meta.Title, meta.MimeType)
	kind := ""
	if inline {
		kind = attachments.PreviewKind(meta.MimeType)
	}
	disposition := "attachment"
	if kind != "" {
		disposition = "inline"
	}
	contentType := meta.MimeType
	if kind == "text" {
		contentType = "text/plain; charset=utf-8"
	}
	csp := "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox; frame-ancestors 'self'"
	if kind == "pdf" {
		csp = "default-src 'none'; frame-ancestors 'self'"
	}
	if cache == "" {
		cache = "private, no-store"
	}
	h := w.Header()
	h.Set("content-type", contentType)
	h.Set("content-length", strconv.Itoa(len(b)))
	h.Set("content-disposition", disposition+`; filename="`+asciiName(name)+`"; filename*=UTF-8''`+encodeURIComponent(name))
	h.Set("x-content-type-options", "nosniff")
	h.Set("content-security-policy", csp)
	h.Set("referrer-policy", "no-referrer")
	h.Set("cache-control", cache)
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

var attachmentID = regexp.MustCompile(`(?i)^att_[a-z0-9]+$`)

// attachment is GET /api/attachments/{id} (signed in). ?inline=1 opens
// previewable types in the browser; anything else downloads.
func (s *Server) attachment(w http.ResponseWriter, r *http.Request) {
	if s.requireUserFromRequest(w, r) == nil {
		return
	}
	id := r.PathValue("id")
	if !attachmentID.MatchString(id) {
		writeJSON(w, 404, map[string]string{"error": "NOT_FOUND"})
		return
	}
	file, ok, err := attachments.Read(s.o.DB, id, s.o.MasterKey)
	if err != nil {
		s.o.Log.Error("reading an attachment failed", logx.Fields{"id": id, "error": err})
	}
	if err != nil || !ok {
		writeJSON(w, 404, map[string]string{"error": "NOT_FOUND"})
		return
	}
	fileResponse(w, file.Meta, file.Bytes, r.URL.Query().Get("inline") == "1", "")
}

// notice is plain text, so nothing on the public share path can render as a page.
func notice(w http.ResponseWriter, status int, text string) {
	h := w.Header()
	h.Set("content-type", "text/plain; charset=utf-8")
	h.Set("x-content-type-options", "nosniff")
	h.Set("cache-control", "no-store")
	h.Set("referrer-policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text + "\n"))
}

// share is GET /api/share/{token}: a temporary link to one saved attachment.
// No session for "public" links (the token is the auth); "private" ones need
// a dashboard sign-in. Previewable files open in the browser, the rest
// download; ?download=1 always downloads.
func (s *Server) share(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	found, err := attachments.ResolveShare(s.o.DB, r.PathValue("token"), now)
	if err != nil {
		s.o.Log.Error("resolving a share failed", logx.Fields{"error": err})
		notice(w, 404, "This link doesn't exist.")
		return
	}
	if !found.OK {
		switch found.Reason {
		case "expired":
			notice(w, 410, "This link has expired. Ask for a new one.")
		case "revoked":
			notice(w, 410, "This link was turned off. Ask for a new one.")
		default:
			notice(w, 404, "This link doesn't exist.")
		}
		return
	}
	if found.Share.Visibility == "private" {
		if v, _ := s.Viewer(w, r); v == nil {
			notice(w, 401, "This link is private. Sign in to the dashboard first, then open the link again.")
			return
		}
	}
	file, ok, err := attachments.Read(s.o.DB, found.Attachment.ID, s.o.MasterKey)
	if err != nil || !ok {
		notice(w, 404, "This link doesn't exist.")
		return
	}
	if err := attachments.RecordOpen(s.o.DB, found.Share.ID, now); err != nil {
		s.o.Log.Error("counting a share open failed", logx.Fields{"error": err})
	}
	fileResponse(w, file.Meta, file.Bytes, r.URL.Query().Get("download") != "1", "")
}

// --- hooks ----------------------------------------------------------------------------

func (s *Server) poke() {
	if s.o.Runtime != nil {
		s.o.Runtime.Reconcile()
	}
}

type hookFailed struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type hookAccepted struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}

type hookRuns struct {
	OK   bool `json:"ok"`
	Runs int  `json:"runs"`
}

// hook is POST /api/hooks/{slug}: inbound webhooks. No session: the HMAC
// signature over x-palui-timestamp and the raw body bytes is the auth, and old
// timestamps and repeated signatures are refused. The cheap checks run before
// the body is read: Content-Length, then the slug. A verified event becomes a
// `pending` message in the hook's chat (or starts the flows listening to it),
// and the runtime handles it right away.
func (s *Server) hook(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { writeJSON(w, status, hookFailed{OK: false, Error: msg}) }
	// 1. Refuse an announced oversized body before touching the database.
	if r.ContentLength > hooks.MaxBodyBytes {
		fail(413, "Body is too large")
		return
	}
	if length := r.Header.Get("content-length"); length != "" {
		t := strx.Trim(length)
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil || n < 0 || n > hooks.MaxBodyBytes || strings.TrimLeft(t, "0123456789") != "" {
			fail(413, "Body is too large")
			return
		}
	}
	// 2. The slug, before reading anything.
	hook, failure, err := hooks.Lookup(s.o.DB, r.PathValue("slug"))
	if err != nil {
		s.o.Log.Error("hook lookup failed", logx.Fields{"error": err})
		fail(500, "Internal error")
		return
	}
	if failure != nil {
		fail(failure.Status, failure.Error)
		return
	}
	// 3. The body as bytes, cut off at the cap even without a Content-Length.
	body, failure := hooks.ReadBody(r, hooks.MaxBodyBytes)
	if failure != nil {
		fail(failure.Status, failure.Error)
		return
	}
	// 4. Timestamp and signature over the raw bytes.
	headers := hooks.Headers{}
	if v, ok := r.Header[http.CanonicalHeaderKey(hooks.SignatureHeader)]; ok && len(v) > 0 {
		headers.Signature = &v[0]
	}
	if v, ok := r.Header[http.CanonicalHeaderKey(hooks.TimestampHeader)]; ok && len(v) > 0 {
		headers.Timestamp = &v[0]
	}
	now := s.now()
	if failure := hooks.VerifySignature(hook, body, headers, s.o.MasterKey, now); failure != nil {
		fail(failure.Status, failure.Error)
		return
	}
	// Mode flow (RFC flows 4.1): the body starts the flows listening to this hook.
	if hook.Mode == hooks.ModeFlow {
		hm := map[string]string{}
		for _, name := range flows.HookHeaders {
			if v, ok := r.Header[http.CanonicalHeaderKey(name)]; ok && len(v) > 0 {
				hm[name] = strings.Join(v, ", ")
			}
		}
		res, err := flows.IngestFlowHook(s.o.DB, hook, hooks.BodyText(body), hm, now)
		if err != nil {
			s.o.Log.Error("flow hook ingest failed", logx.Fields{"error": err})
			fail(500, "Internal error")
			return
		}
		s.poke()
		writeJSON(w, 202, hookRuns{OK: true, Runs: res.Started})
		return
	}
	msg, err := hooks.Ingest(s.o.DB, hook, hooks.BodyText(body), now)
	if err != nil {
		s.o.Log.Error("hook ingest failed", logx.Fields{"error": err})
		fail(500, "Internal error")
		return
	}
	s.poke()
	writeJSON(w, 202, hookAccepted{OK: true, ID: msg.ID})
}

// --- Google OAuth ---------------------------------------------------------------------

// DetailMax is oauth-return.ts DETAIL_MAX.
const DetailMax = 200

// CleanDetail is cleanDetail: control and bidi characters become spaces,
// whitespace collapses, at most 200 code points.
func CleanDetail(detail string) string {
	var b strings.Builder
	for _, r := range detail {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	out := strx.Trim(strx.CollapseSpace(b.String()))
	runes := []rune(out)
	if len(runes) > DetailMax {
		runes = runes[:DetailMax]
	}
	return string(runes)
}

// OAuthReturnURL is oauthReturnUrl: always APP_URL's Connectors page.
func OAuthReturnURL(appURL string, ok bool, detail string) string {
	target, err := url.Parse(strings.TrimRight(appURL, "/") + "/app/connectors")
	if err != nil {
		return "/app/connectors"
	}
	state := "error"
	if ok {
		state = "ok"
	}
	target.RawQuery = "oauth=" + formEncode(state) + "&detail=" + formEncode(CleanDetail(detail))
	return target.String()
}

// formEncode is URLSearchParams' value encoding: space is "+", and only
// letters, digits and *-._ stay as they are.
func formEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("*-._", c) >= 0:
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)>>4, 16)) + strings.ToUpper(strconv.FormatInt(int64(c)&15, 16)))
		}
	}
	return b.String()
}

// googleCallback is GET /api/oauth/google/callback: the gcal connector's
// OAuth redirect. The signed state names the connector and expires after 10
// minutes, so it needs no session. On success the refresh token is stored
// encrypted and the supervisor restarts the connector right away.
func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	res, err := gcal.HandleGoogleCallback(ctx, s.o.DB, r.URL.Query(), s.o.MasterKey, s.o.Config.AppURL, nil, s.now())
	if err != nil {
		s.o.Log.Error("google callback failed", logx.Fields{"error": err})
		res = gcal.CallbackResult{Detail: err.Error()}
	}
	if res.OK {
		s.poke()
	}
	w.Header().Set("location", OAuthReturnURL(s.o.Config.AppURL, res.OK, res.Detail))
	w.WriteHeader(http.StatusFound)
}

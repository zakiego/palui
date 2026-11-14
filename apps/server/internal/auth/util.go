package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- same origin ---------------------------------------------------------------------

func originOf(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// SameOrigin is the check every write runs (checkSameOrigin and
// crossOriginWrite combined). A request marked cross-site by the browser is
// refused. The Origin header decides when present, else the Referer: it must
// be APP_URL's origin, or (behind a dev proxy) the host the request was sent
// to. A write with neither header is refused too: every browser sends Origin
// on a fetch POST, so only non-browser clients lack both, and they have no
// business writing through the dashboard's endpoints.
func SameOrigin(r *http.Request, appURL string) bool {
	if r.Header.Get("sec-fetch-site") == "cross-site" {
		return false
	}
	from := r.Header.Get("origin")
	if from == "" || from == "null" {
		from = r.Header.Get("referer")
	}
	if from == "" {
		return false
	}
	origin, ok := originOf(from)
	if !ok {
		return false
	}
	app, ok := originOf(appURL)
	if ok && origin == app {
		return true
	}
	host := r.Header.Get("x-forwarded-host")
	if host == "" {
		host = r.Host
	}
	return host != "" && hostOf(origin) == host
}

// --- rate limits ---------------------------------------------------------------------

// Rule is a rate limit window (seconds) and the requests allowed in it.
type Rule struct {
	Window int64
	Max    int
}

// DefaultRule and PathRules are the TS rateLimit config: 100 per 60 s, and
// the passkey ceremonies tighter. Paths are relative to /api/auth.
var (
	DefaultRule = Rule{Window: 60, Max: 100}
	PathRules   = map[string]Rule{
		"/passkey/authenticate":         {Window: 60, Max: 5},
		"/passkey/authenticate-options": {Window: 60, Max: 10},
		"/passkey/register":             {Window: 60, Max: 5},
		"/passkey/register-options":     {Window: 60, Max: 10},
	}
)

type bucket struct {
	count       int
	lastRequest int64
	expiresAt   int64
}

// Limiter is Better Auth's memory rate limiter: one bucket per client IP and
// path. A bucket's count resets once a full window passed since its last
// counted request.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	Rules   map[string]Rule
	Default Rule
	// Unix ms; default time.Now.
	Clock func() int64
}

func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, Rules: PathRules, Default: DefaultRule}
}

const maxBuckets = 100_000

// Allow counts one request; when refused it returns the seconds to wait.
func (l *Limiter) Allow(ip, path string) (bool, int64) {
	now := time.Now().UnixMilli()
	if l.Clock != nil {
		now = l.Clock()
	}
	rule, ok := l.Rules[path]
	if !ok {
		rule = l.Default
	}
	window := rule.Window * 1000
	key := ip + "|" + path
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if now >= b.expiresAt {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) > maxBuckets {
		for k := range l.buckets {
			delete(l.buckets, k)
			if len(l.buckets) <= maxBuckets {
				break
			}
		}
	}
	b := l.buckets[key]
	switch {
	case b == nil:
		l.buckets[key] = &bucket{count: 1, lastRequest: now, expiresAt: now + window}
	case now-b.lastRequest >= window:
		b.count, b.lastRequest, b.expiresAt = 1, now, now+window
	case b.count >= rule.Max:
		retry := (b.lastRequest + window - now + 999) / 1000
		return false, retry
	default:
		b.count++
		b.lastRequest, b.expiresAt = now, now+window
	}
	return true, 0
}

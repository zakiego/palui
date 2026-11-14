package auth_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const T0 int64 = 1_800_000_000_000

func count(t *testing.T, d *db.DB, table string) int {
	t.Helper()
	var n int
	if err := d.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedUser(t *testing.T, d *db.DB) string {
	t.Helper()
	testutil.Must(t, exec(d, "INSERT INTO user (id, name, email, email_verified, created_at, updated_at) VALUES ('u1', 'Owner', 'owner@palui.local', 1, ?, ?)", T0, T0))
	return "u1"
}

func exec(d *db.DB, q string, args ...any) error {
	_, err := d.Exec(q, args...)
	return err
}

// --- owner invite (owner-invite.test.ts) ----------------------------------------------

func TestInviteStoresOnlyTheHashAndPrintsAnAppInviteLink(t *testing.T) {
	d := testutil.DB(t)
	inv, err := auth.CreateOwnerInvite(d, "https://palui.example.com/", T0)
	testutil.Must(t, err)
	if inv.URL != "https://palui.example.com/app/invite/"+inv.Token || inv.ExpiresAt != T0+auth.InviteTTLMs {
		t.Fatalf("%+v", inv)
	}
	var hash string
	testutil.Must(t, d.QueryRow("SELECT token_hash FROM owner_invites").Scan(&hash))
	if hash == inv.Token || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) || hash != secret.Sha256(inv.Token) {
		t.Fatal(hash)
	}
	if len(inv.Token) != 32 {
		t.Fatalf("24 bytes base64url is 32 characters, got %d", len(inv.Token))
	}
}

func TestInviteCreatesTheOwnerOnFirstUseAndWorksOnlyOnce(t *testing.T) {
	d := testutil.DB(t)
	inv, _ := auth.CreateOwnerInvite(d, "http://localhost:4890", T0)
	st, _ := auth.InspectInvite(d, inv.Token, T0+1000)
	if !st.OK || st.Recovery {
		t.Fatalf("%+v", st)
	}
	res, err := auth.AcceptOwnerInvite(d, inv.Token, T0+1000)
	testutil.Must(t, err)
	if res.Username != "owner" || !res.Created {
		t.Fatalf("%+v", res)
	}
	var username, email string
	testutil.Must(t, d.QueryRow("SELECT username, email FROM user").Scan(&username, &email))
	if count(t, d, "user") != 1 || username != "owner" || email != "owner@palui.invalid" {
		t.Fatal(username)
	}
	st, _ = auth.InspectInvite(d, inv.Token, T0+2000)
	if b, _ := st.MarshalJSON(); string(b) != `{"ok":false,"problem":"This link was already used."}` {
		t.Fatal(string(b))
	}
	if _, err := auth.AcceptOwnerInvite(d, inv.Token, T0+2000); err == nil {
		t.Fatal("accepted twice")
	} else if _, ok := err.(*auth.InviteError); !ok {
		t.Fatalf("%T", err)
	}
	if count(t, d, "user") != 1 {
		t.Fatal("second user")
	}
}

func TestInviteRefusesAnExpiredLinkAndLeavesItUnused(t *testing.T) {
	d := testutil.DB(t)
	inv, _ := auth.CreateOwnerInvite(d, "http://localhost:4890", T0)
	late := T0 + auth.InviteTTLMs
	st, _ := auth.InspectInvite(d, inv.Token, late)
	if b, _ := st.MarshalJSON(); string(b) != `{"ok":false,"problem":"This link expired."}` {
		t.Fatal(string(b))
	}
	if _, err := auth.AcceptOwnerInvite(d, inv.Token, late); err == nil {
		t.Fatal("accepted")
	}
	var used *int64
	testutil.Must(t, d.QueryRow("SELECT used_at FROM owner_invites").Scan(&used))
	if count(t, d, "user") != 0 || used != nil {
		t.Fatal("used")
	}
}

func TestInviteRefusesATokenThatWasNeverIssued(t *testing.T) {
	d := testutil.DB(t)
	st, _ := auth.InspectInvite(d, "nope", T0)
	if b, _ := st.MarshalJSON(); string(b) != `{"ok":false,"problem":"This link isn't valid."}` {
		t.Fatal(string(b))
	}
}

func TestRecoveryKeepsTheOwnerAndShutsOutOldSessionsAndPasskeys(t *testing.T) {
	d := testutil.DB(t)
	inv, _ := auth.CreateOwnerInvite(d, "http://x", T0)
	first, err := auth.AcceptOwnerInvite(d, inv.Token, T0)
	testutil.Must(t, err)
	_, _, err = auth.CreateSession(d, first.UserID, "1.2.3.4", "ua", T0)
	testutil.Must(t, err)
	testutil.Must(t, exec(d, "INSERT INTO passkey (id, public_key, user_id, credential_id, counter, device_type, backed_up) VALUES ('pk_old', 'x', ?, 'cred', 0, 'singleDevice', 0)", first.UserID))

	inv2, _ := auth.CreateOwnerInvite(d, "http://x", T0+5000)
	st, _ := auth.InspectInvite(d, inv2.Token, T0+5000)
	if !st.OK || !st.Recovery {
		t.Fatalf("%+v", st)
	}
	if b, _ := st.MarshalJSON(); string(b) != `{"ok":true,"expiresAt":1800000905000,"recovery":true}` {
		t.Fatal(string(b))
	}
	second, err := auth.AcceptOwnerInvite(d, inv2.Token, T0+6000)
	testutil.Must(t, err)
	if second.UserID != first.UserID || second.Created {
		t.Fatalf("%+v", second)
	}
	if count(t, d, "user") != 1 || count(t, d, "session") != 0 || count(t, d, "passkey") != 0 {
		t.Fatal("not wiped")
	}
}

// --- sessions -------------------------------------------------------------------------

func TestSessionsStoreOnlyTheTokenHashAndRefreshOnceADay(t *testing.T) {
	d := testutil.DB(t)
	uid := seedUser(t, d)
	token, s, err := auth.CreateSession(d, uid, "203.0.113.7", "Firefox", T0)
	testutil.Must(t, err)
	if len(token) != 32 || s.ExpiresAt != T0+auth.SessionExpiresInMs || *s.IPAddress != "203.0.113.7" || *s.UserAgent != "Firefox" {
		t.Fatalf("%q %+v", token, s)
	}
	var stored string
	testutil.Must(t, d.QueryRow("SELECT token FROM session").Scan(&stored))
	if stored == token || stored != auth.HashToken(token) {
		t.Fatal("raw token stored")
	}
	f, err := auth.GetSession(d, token, true, T0+1000)
	testutil.Must(t, err)
	if f == nil || f.Refreshed || f.User.ID != uid || f.Viewer().User.Username != "Owner" {
		t.Fatalf("%+v", f)
	}
	// A day later the expiry moves forward, once.
	day := T0 + auth.SessionUpdateAgeMs
	f, _ = auth.GetSession(d, token, true, day)
	if !f.Refreshed || f.Session.ExpiresAt != day+auth.SessionExpiresInMs {
		t.Fatalf("%+v", f.Session)
	}
	f, _ = auth.GetSession(d, token, true, day+1000)
	if f.Refreshed {
		t.Fatal("refreshed twice in a day")
	}
	// Without refresh (the read-only lookups) nothing moves.
	f, _ = auth.GetSession(d, token, false, day+2*auth.SessionUpdateAgeMs)
	if f.Refreshed {
		t.Fatal("refreshed")
	}
	if g, _ := auth.GetSession(d, "wrong", true, day); g != nil {
		t.Fatal("wrong token found")
	}
	if g, _ := auth.GetSession(d, "", true, day); g != nil {
		t.Fatal("empty token found")
	}
	// Expired: nil, and the row is gone.
	if g, _ := auth.GetSession(d, token, true, day+auth.SessionExpiresInMs+1); g != nil {
		t.Fatal("expired session found")
	}
	if count(t, d, "session") != 0 {
		t.Fatal("expired row kept")
	}
}

func TestBetterAuthSessionRowsAreHarmless(t *testing.T) {
	d := testutil.DB(t)
	uid := seedUser(t, d)
	// Better Auth stored the raw token. Sending it finds nothing.
	testutil.Must(t, exec(d, "INSERT INTO session (id, expires_at, token, created_at, updated_at, user_id) VALUES ('old', ?, 'rawBetterAuthToken0123456789abcd', ?, ?, ?)", T0+1e9, T0, T0, uid))
	if f, _ := auth.GetSession(d, "rawBetterAuthToken0123456789abcd", true, T0); f != nil {
		t.Fatal("an old raw token signed in")
	}
}

func TestSignOutDeletesTheSession(t *testing.T) {
	d := testutil.DB(t)
	uid := seedUser(t, d)
	token, s, _ := auth.CreateSession(d, uid, "", "", T0)
	if !auth.SessionAlive(d, s.ID, T0) {
		t.Fatal("not alive")
	}
	testutil.Must(t, auth.DeleteSession(d, token))
	if auth.SessionAlive(d, s.ID, T0) {
		t.Fatal("alive after sign-out")
	}
}

// stream-guard.test.ts sessionAlive.
func TestSessionAliveUntilDeletedOrExpired(t *testing.T) {
	d := testutil.DB(t)
	uid := seedUser(t, d)
	testutil.Must(t, exec(d, "INSERT INTO session (id, expires_at, token, created_at, updated_at, user_id) VALUES ('s1', 2000, 'tok', 0, 0, ?)", uid))
	if !auth.SessionAlive(d, "s1", 1000) || auth.SessionAlive(d, "s1", 2000) || auth.SessionAlive(d, "nope", 1000) {
		t.Fatal("alive rules")
	}
	testutil.Must(t, exec(d, "DELETE FROM session WHERE user_id = ?", uid))
	if auth.SessionAlive(d, "s1", 1000) {
		t.Fatal("deleted session alive")
	}
}

func TestCookiesAndClientIP(t *testing.T) {
	c := auth.SessionCookie("https://palui.example.com", "tok")
	if c.Name != "palui_session" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 30*24*3600 {
		t.Fatalf("%+v", c)
	}
	if auth.SessionCookie("http://localhost:4890", "tok").Secure {
		t.Fatal("secure on http")
	}
	h := http.Header{}
	if auth.ClientIP(h) != "local" {
		t.Fatal("local")
	}
	h.Set("x-real-ip", "10.0.0.1")
	h.Set("x-forwarded-for", " 198.51.100.2 , 10.0.0.1")
	if auth.ClientIP(h) != "198.51.100.2" {
		t.Fatal(auth.ClientIP(h))
	}
	h.Set("cf-connecting-ip", "203.0.113.9")
	if auth.ClientIP(h) != "203.0.113.9" {
		t.Fatal(auth.ClientIP(h))
	}
}

// --- same origin (better-auth.test.ts, session.ts checkSameOrigin) ---------------------

func req(method, origin, referer string, extra map[string]string) *http.Request {
	r := httptest.NewRequest(method, "http://localhost:4890/api/rpc/x", nil)
	if origin != "" {
		r.Header.Set("origin", origin)
	}
	if referer != "" {
		r.Header.Set("referer", referer)
	}
	for k, v := range extra {
		r.Header.Set(k, v)
	}
	return r
}

func TestSameOrigin(t *testing.T) {
	app := "http://localhost:4890"
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"app origin", req("POST", "http://localhost:4890", "", nil), true},
		{"evil origin", req("POST", "https://evil.example", "", nil), false},
		{"referer only", req("POST", "", "http://localhost:4890/app/settings", nil), true},
		{"evil referer", req("POST", "", "https://evil.example/x", nil), false},
		{"neither", req("POST", "", "", nil), false},
		{"cross-site fetch", req("POST", "http://localhost:4890", "", map[string]string{"sec-fetch-site": "cross-site"}), false},
		{"bad origin", req("POST", "::nope", "", nil), false},
		{"null origin falls back to the referer", req("POST", "null", "http://localhost:4890/", nil), true},
		{"dev proxy: origin is the host it was sent to", req("POST", "http://localhost:4890", "", nil), true},
		{"forwarded host", req("POST", "https://tunnel.example", "", map[string]string{"x-forwarded-host": "tunnel.example"}), true},
	}
	for _, c := range cases {
		if got := auth.SameOrigin(c.r, app); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	r := req("POST", "https://palui.example.com", "", nil)
	if !auth.SameOrigin(r, "https://palui.example.com") {
		t.Fatal("prod origin")
	}
}

// --- rate limits ---------------------------------------------------------------------

func TestLimiterWindowsPerIPAndPath(t *testing.T) {
	now := int64(0)
	l := auth.NewLimiter()
	l.Clock = func() int64 { return now }
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("1.1.1.1", "/passkey/authenticate"); !ok {
			t.Fatalf("request %d refused", i+1)
		}
	}
	now = 10_000
	ok, retry := l.Allow("1.1.1.1", "/passkey/authenticate")
	if ok || retry != 50 {
		t.Fatalf("6th: %v %d", ok, retry)
	}
	// Another IP, another path: their own buckets.
	if ok, _ := l.Allow("2.2.2.2", "/passkey/authenticate"); !ok {
		t.Fatal("other ip")
	}
	if ok, _ := l.Allow("1.1.1.1", "/session"); !ok {
		t.Fatal("other path")
	}
	// A refusal doesn't extend the window: 60 s after the last counted request it resets.
	now = 60_000
	if ok, _ := l.Allow("1.1.1.1", "/passkey/authenticate"); !ok {
		t.Fatal("not reset")
	}
	// The default rule is 100 per 60 s.
	for i := 0; i < 99; i++ {
		l.Allow("3.3.3.3", "/session")
	}
	if ok, _ := l.Allow("3.3.3.3", "/session"); !ok {
		t.Fatal("100th refused")
	}
	if ok, _ := l.Allow("3.3.3.3", "/session"); ok {
		t.Fatal("101st allowed")
	}
	for path, rule := range auth.PathRules {
		if rule.Window != 60 {
			t.Fatal(path)
		}
	}
	if auth.PathRules["/passkey/authenticate-options"].Max != 10 || auth.PathRules["/passkey/register-options"].Max != 10 || auth.PathRules["/passkey/register"].Max != 5 {
		t.Fatal("rules")
	}
}

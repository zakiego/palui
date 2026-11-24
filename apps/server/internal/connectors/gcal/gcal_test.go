package gcal_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	KEY = "test-master-key-0123456789-abcdefghijklmnop"
	APP = "https://palui.example.com"
	TZ  = "Asia/Makassar"
	// 2026-11-10T10:00:00+08:00
	NOW = int64(1794276000000)
)

var CLIENT = map[string]string{"clientId": "123-abc.apps.googleusercontent.com", "clientSecret": "GOCSPX-secret-value"}

type call struct {
	URL    string
	Body   string
	Header http.Header
}

type google struct {
	mu        sync.Mutex
	calls     []call
	refresh   string
	noRefresh bool
	events    []any
}

func (g *google) Calls() []call {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]call(nil), g.calls...)
}

func (g *google) RoundTrip(r *http.Request) (*http.Response, error) {
	var body string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}
	u := r.URL.String()
	g.mu.Lock()
	g.calls = append(g.calls, call{u, body, r.Header.Clone()})
	g.mu.Unlock()
	reply := func(status int, v any) (*http.Response, error) {
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
	}
	switch {
	case u == "https://oauth2.googleapis.com/token":
		form, _ := url.ParseQuery(body)
		if form.Get("grant_type") == "authorization_code" {
			if form.Get("code") != "good-code" {
				return reply(400, map[string]any{"error": "invalid_grant", "error_description": "Bad Request"})
			}
			out := map[string]any{"access_token": "ya29.access", "expires_in": 3599, "scope": gcal.CalendarScope, "token_type": "Bearer"}
			if !g.noRefresh {
				out["refresh_token"] = "1//refresh-token"
			}
			return reply(200, out)
		}
		if g.refresh == "invalid_grant" {
			return reply(400, map[string]any{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
		}
		return reply(200, map[string]any{"access_token": "ya29.fresh", "expires_in": 3599})
	case u == "https://www.googleapis.com/calendar/v3/calendars/primary":
		return reply(200, map[string]any{"id": "zaki@example.com", "summary": "zaki@example.com"})
	case strings.HasPrefix(u, "https://www.googleapis.com/calendar/v3/calendars/"):
		events := g.events
		if events == nil {
			events = []any{
				map[string]any{"id": "e1", "status": "confirmed", "summary": "Standup", "start": map[string]any{"dateTime": "2026-11-10T09:00:00+08:00"}, "end": map[string]any{"dateTime": "2026-11-10T09:15:00+08:00"}},
				map[string]any{"id": "e2", "status": "cancelled", "summary": "Old", "start": map[string]any{"dateTime": "2026-11-10T11:00:00+08:00"}, "end": map[string]any{"dateTime": "2026-11-10T12:00:00+08:00"}},
				map[string]any{"id": "e3", "summary": "Libur", "start": map[string]any{"date": "2026-11-10"}, "end": map[string]any{"date": "2026-11-11"}},
			}
		}
		return reply(200, map[string]any{"items": events})
	}
	return reply(404, map[string]any{})
}

func (g *google) client() *http.Client { return &http.Client{Transport: g} }

func upsert(t *testing.T, d *db.DB, id string, secrets map[string]string, now int64) {
	sec := jsonx.NewMap[string]()
	for _, k := range []string{"clientId", "clientSecret", "refreshToken"} {
		if v, ok := secrets[k]; ok {
			sec.Set(k, v)
		}
	}
	_, err := connectors.Upsert(d, connectors.UpsertInput{ID: id, Manifest: gcal.Manifest.Row(), Secrets: sec}, KEY, now)
	testutil.Must(t, err)
}

func TestOAuthStateRoundTripsAndBindsTheConnectorID(t *testing.T) {
	s := gcal.SignState(KEY, "gcal", NOW)
	if v := gcal.VerifyState(KEY, s, NOW+1000); !v.OK || v.ConnectorID != "gcal" {
		t.Fatalf("%+v", v)
	}
}

func TestOAuthStateExpiresAfter10MinutesAndRejectsTampering(t *testing.T) {
	s := gcal.SignState(KEY, "gcal", NOW)
	if v := gcal.VerifyState(KEY, s, NOW+gcal.StateTTLMs); v.OK || v.Reason != "expired" {
		t.Fatalf("%+v", v)
	}
	if v := gcal.VerifyState("another-master-key-0123456789-abcdefghij", s, NOW); v.Reason != "bad-signature" {
		t.Fatalf("%+v", v)
	}
	parts := strings.Split(s, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"c":"mcp-vorneo","e":` + jsonx.NumberString(float64(NOW+1e9)) + `,"n":"x"}`))
	if v := gcal.VerifyState(KEY, forged+"."+parts[1], NOW); v.Reason != "bad-signature" {
		t.Fatalf("%+v", v)
	}
	if v := gcal.VerifyState(KEY, parts[0], NOW); v.Reason != "malformed" {
		t.Fatalf("%+v", v)
	}
}

func TestBuildAuthURLAsksForCalendarReadonlyOfflineAccessAndConsent(t *testing.T) {
	u, _ := url.Parse(gcal.BuildAuthURL(gcal.AuthURLInput{ClientID: CLIENT["clientId"], RedirectURI: gcal.RedirectURIFor(APP + "/"), State: "st"}))
	if u.Scheme+"://"+u.Host+u.Path != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Fatal(u)
	}
	q := u.Query()
	want := map[string]string{"client_id": CLIENT["clientId"], "redirect_uri": "https://palui.example.com/api/oauth/google/callback", "response_type": "code", "scope": "https://www.googleapis.com/auth/calendar.readonly", "access_type": "offline", "prompt": "consent", "state": "st"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Fatal(k, q.Get(k))
		}
	}
}

func TestGoogleAuthURLReadsTheClientIDFromTheStoredSecrets(t *testing.T) {
	d := testutil.DB(t)
	upsert(t, d, "gcal", CLIENT, 1)
	raw, err := gcal.GoogleAuthURLFor(d, "gcal", KEY, APP, NOW)
	testutil.Must(t, err)
	u, _ := url.Parse(raw)
	if u.Query().Get("client_id") != CLIENT["clientId"] {
		t.Fatal(raw)
	}
	if v := gcal.VerifyState(KEY, u.Query().Get("state"), NOW); !v.OK || v.ConnectorID != "gcal" {
		t.Fatalf("%+v", v)
	}
	row, _, _ := connectors.Get(d, "gcal")
	nonce, _ := row.Config.Get("oauthNonce")
	exp, _ := row.Config.Get("oauthNonceExp")
	if _, ok := nonce.(string); !ok || exp != float64(NOW+gcal.StateTTLMs) {
		t.Fatal(nonce, exp)
	}
	if _, err := gcal.GoogleAuthURLFor(d, "nope", KEY, APP, NOW); err == nil || !strings.Contains(err.Error(), "No Google Calendar") {
		t.Fatal(err)
	}
}

func callbackSetup(t *testing.T) *db.DB {
	d := testutil.DB(t)
	upsert(t, d, "gcal", CLIENT, 1)
	return d
}

func cb(params map[string]string) url.Values {
	v := url.Values{}
	for k, x := range params {
		v.Set(k, x)
	}
	return v
}

func issue(t *testing.T, d *db.DB, now int64) string {
	raw, err := gcal.GoogleAuthURLFor(d, "gcal", KEY, APP, now)
	testutil.Must(t, err)
	u, _ := url.Parse(raw)
	return u.Query().Get("state")
}

func handle(t *testing.T, d *db.DB, q url.Values, g *google) gcal.CallbackResult {
	t.Helper()
	res, err := gcal.HandleGoogleCallback(context.Background(), d, q, KEY, APP, g.client(), NOW)
	testutil.Must(t, err)
	return res
}

func TestCallbackExchangesTheCodeStoresTheRefreshTokenEncryptedAndLearnsTheEmail(t *testing.T) {
	d := callbackSetup(t)
	g := &google{}
	res := handle(t, d, cb(map[string]string{"code": "good-code", "state": issue(t, d, NOW)}), g)
	if res != (gcal.CallbackResult{OK: true, ConnectorID: "gcal", Detail: "Connected as zaki@example.com"}) {
		t.Fatalf("%+v", res)
	}
	row, _, _ := connectors.Get(d, "gcal")
	if _, ok := row.Config.Get("oauthNonce"); ok {
		t.Fatal("nonce kept")
	}
	sec, err := connectors.Secrets(row, KEY)
	testutil.Must(t, err)
	if jsonx.Stringify(sec) != `{"clientId":"123-abc.apps.googleusercontent.com","clientSecret":"GOCSPX-secret-value","refreshToken":"1//refresh-token"}` {
		t.Fatal(jsonx.Stringify(sec))
	}
	if strings.Contains(*row.Secrets, "refresh-token") {
		t.Fatal("stored in clear")
	}
	if email, _ := row.Config.Get("email"); email != "zaki@example.com" {
		t.Fatal(email)
	}
	form, _ := url.ParseQuery(g.Calls()[0].Body)
	if form.Get("redirect_uri") != "https://palui.example.com/api/oauth/google/callback" || form.Get("client_secret") != CLIENT["clientSecret"] {
		t.Fatal(form)
	}
}

func TestCallbackRefusesABadOrExpiredStateBeforeCallingGoogle(t *testing.T) {
	d := callbackSetup(t)
	g := &google{}
	if handle(t, d, cb(map[string]string{"code": "good-code", "state": "x.y"}), g).OK {
		t.Fatal("bad state")
	}
	old := issue(t, d, NOW-gcal.StateTTLMs-1)
	if r := handle(t, d, cb(map[string]string{"code": "good-code", "state": old}), g); r.OK || !strings.Contains(r.Detail, "expired") {
		t.Fatalf("%+v", r)
	}
	if len(g.Calls()) != 0 {
		t.Fatal("called Google")
	}
}

func TestCallbackReportsACancelledSignInARejectedCodeAndAMissingRefreshToken(t *testing.T) {
	d := callbackSetup(t)
	if r := handle(t, d, cb(map[string]string{"error": "access_denied", "state": issue(t, d, NOW)}), &google{}); r.OK || !strings.Contains(r.Detail, "cancelled") {
		t.Fatalf("%+v", r)
	}
	if r := handle(t, d, cb(map[string]string{"code": "bad", "state": issue(t, d, NOW)}), &google{}); r.OK || !strings.Contains(r.Detail, "revoked or expired") {
		t.Fatalf("%+v", r)
	}
	if r := handle(t, d, cb(map[string]string{"code": "good-code", "state": issue(t, d, NOW)}), &google{noRefresh: true}); r.OK || !strings.Contains(r.Detail, "refresh token") {
		t.Fatalf("%+v", r)
	}
	row, _, _ := connectors.Get(d, "gcal")
	sec, _ := connectors.Secrets(row, KEY)
	if v, _ := sec.Get("refreshToken"); v != "" {
		t.Fatal(v)
	}
}

func TestCallbackAcceptsAStateOnceAndRefusesOneThatWasNeverStoredOrWasReplaced(t *testing.T) {
	d := callbackSetup(t)
	state := issue(t, d, NOW)
	g := &google{}
	if !handle(t, d, cb(map[string]string{"code": "good-code", "state": state}), g).OK {
		t.Fatal("first")
	}
	n := len(g.Calls())
	if r := handle(t, d, cb(map[string]string{"code": "good-code", "state": state}), g); r.OK || !strings.Contains(r.Detail, "already used") {
		t.Fatalf("%+v", r)
	}
	if len(g.Calls()) != n {
		t.Fatal("called Google")
	}
	if r := handle(t, d, cb(map[string]string{"code": "good-code", "state": gcal.SignState(KEY, "gcal", NOW)}), g); r.OK || !strings.Contains(r.Detail, "already used") {
		t.Fatalf("%+v", r)
	}
	first := issue(t, d, NOW)
	second := issue(t, d, NOW)
	if handle(t, d, cb(map[string]string{"code": "good-code", "state": first}), g).OK {
		t.Fatal("replaced state accepted")
	}
	if r := handle(t, d, cb(map[string]string{"code": "good-code", "state": second}), g); r.OK || !strings.Contains(r.Detail, "already used") {
		t.Fatalf("%+v", r)
	}
}

func TestCallbackRefusesAStateForAConnectorOfAnotherType(t *testing.T) {
	d := callbackSetup(t)
	_, err := connectors.Upsert(d, connectors.UpsertInput{ID: "mcp-x", Manifest: connectors.RowManifest{ID: "mcp", Kind: "tools", Name: "MCP"}}, KEY, 1)
	testutil.Must(t, err)
	if r := handle(t, d, cb(map[string]string{"code": "good-code", "state": gcal.SignState(KEY, "mcp-x", NOW)}), &google{}); r.OK || !strings.Contains(r.Detail, "no longer exists") {
		t.Fatalf("%+v", r)
	}
}

func TestRefreshAccessTokenPostsTheRefreshGrant(t *testing.T) {
	g := &google{}
	tok, exp, err := gcal.RefreshAccessToken(context.Background(), CLIENT["clientId"], CLIENT["clientSecret"], "1//r", g.client(), NOW)
	testutil.Must(t, err)
	if tok != "ya29.fresh" || exp != NOW+3599_000 {
		t.Fatal(tok, exp)
	}
	form, _ := url.ParseQuery(g.Calls()[0].Body)
	if form.Get("grant_type") != "refresh_token" {
		t.Fatal(form)
	}
}

func TestListEventsDropsCancelledEventsAndMapsAllDayOnes(t *testing.T) {
	g := &google{}
	events, err := gcal.ListEvents(context.Background(), gcal.ListEventsInput{AccessToken: "t", CalendarID: "primary", TimeMin: "a", TimeMax: "b"}, g.client())
	testutil.Must(t, err)
	if len(events) != 2 || events[0].ID != "e1" || events[0].AllDay || events[1].ID != "e3" || !events[1].AllDay {
		t.Fatalf("%+v", events)
	}
	u, _ := url.Parse(g.Calls()[0].URL)
	if u.Path != "/calendar/v3/calendars/primary/events" || u.Query().Get("singleEvents") != "true" || g.Calls()[0].Header.Get("authorization") != "Bearer t" {
		t.Fatal(u)
	}
}

func iso(r gcal.Range) [2]string {
	return [2]string{gcal.ISO(r.Min, TZ, false), gcal.ISO(r.Max, TZ, false)}
}

func s(v string) *string { return &v }

func TestResolveRangeUnderstandsKeywordsInTheTurnTimezone(t *testing.T) {
	r, _ := gcal.ResolveRange(s("today"), nil, TZ, NOW)
	if iso(r) != [2]string{"2026-11-10T00:00:00.000+08:00", "2026-11-11T00:00:00.000+08:00"} {
		t.Fatal(iso(r))
	}
	r, _ = gcal.ResolveRange(nil, nil, TZ, NOW)
	if iso(r)[0] != "2026-11-10T00:00:00.000+08:00" {
		t.Fatal(iso(r))
	}
	r, _ = gcal.ResolveRange(s("besok"), nil, TZ, NOW)
	if iso(r)[0] != "2026-11-11T00:00:00.000+08:00" {
		t.Fatal(iso(r))
	}
	r, _ = gcal.ResolveRange(s("week"), nil, TZ, NOW)
	if iso(r)[1] != "2026-11-17T00:00:00.000+08:00" {
		t.Fatal(iso(r))
	}
}

func TestResolveRangeTreatsADateOnlyToAsInclusiveAndCapsLongRanges(t *testing.T) {
	r, _ := gcal.ResolveRange(s("2026-11-12"), s("2026-11-13"), TZ, NOW)
	if iso(r) != [2]string{"2026-11-12T00:00:00.000+08:00", "2026-11-14T00:00:00.000+08:00"} {
		t.Fatal(iso(r))
	}
	long, _ := gcal.ResolveRange(s("2026-01-01"), s("2026-12-31"), TZ, NOW)
	if long.Max-long.Min != 31*24*3600_000 {
		t.Fatal(long.Max - long.Min)
	}
	if _, err := gcal.ResolveRange(s("kapan-kapan"), nil, TZ, NOW); err == nil || !strings.Contains(err.Error(), "Can't read") {
		t.Fatal(err)
	}
	if _, err := gcal.ResolveRange(s("2026-11-12"), s("2026-11-01"), TZ, NOW); err == nil || !strings.Contains(err.Error(), "after the start") {
		t.Fatal(err)
	}
}

func TestPresentsEventsInTheTurnTimezone(t *testing.T) {
	e := gcal.PresentEvent(gcal.Event{ID: "1", CalendarID: "primary", Title: "Call", Start: "2026-11-10T01:00:00Z", End: "2026-11-10T02:00:00Z"}, TZ)
	v := conntest.Of(e)
	if v.S("when") != "Tue 10 Nov 09:00-10:00" || v.S("start") != "2026-11-10T09:00:00+08:00" {
		t.Fatal(v)
	}
}

var runtimeTurn = connectors.TurnInfo{RunID: "r", ChatID: "c", Channel: "telegram", Trigger: "message", TZ: TZ, Now: NOW}

func cfg(t *testing.T) *jsonx.Object {
	o, err := gcal.Manifest.ParseConfig(kit.O("email", "zaki@example.com"))
	testutil.Must(t, err)
	return o
}

func with(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range CLIENT {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestRuntimeTestExplainsWhenOAuthHasntBeenDone(t *testing.T) {
	res, err := gcal.New(gcal.Options{Client: (&google{}).client()}).Test(conntest.Ctx(t, nil, "gcal", cfg(t), with(nil)))
	testutil.Must(t, err)
	if res.OK || !regexp.MustCompile(`sign-in isn't done`).MatchString(res.Detail) {
		t.Fatalf("%+v", res)
	}
}

func TestRuntimeTestRefreshesAndListsOneEvent(t *testing.T) {
	g := &google{}
	res, err := gcal.New(gcal.Options{Client: g.client(), Clock: func() int64 { return NOW }}).Test(conntest.Ctx(t, nil, "gcal", cfg(t), with(map[string]string{"refreshToken": "1//r"})))
	testutil.Must(t, err)
	if !res.OK || res.Detail != "Connected as zaki@example.com. Next event: Standup" {
		t.Fatalf("%+v", res)
	}
	u, _ := url.Parse(g.Calls()[1].URL)
	if u.Query().Get("maxResults") != "1" {
		t.Fatal(u)
	}
}

func TestRuntimeTestExplainsARevokedToken(t *testing.T) {
	res, _ := gcal.New(gcal.Options{Client: (&google{refresh: "invalid_grant"}).client()}).Test(conntest.Ctx(t, nil, "gcal", cfg(t), with(map[string]string{"refreshToken": "1//r"})))
	if res.OK || !strings.Contains(res.Detail, "Connect with Google again") {
		t.Fatalf("%+v", res)
	}
}

func TestGetCalendarReturnsEventsForTodayInTheTurnTZCachingTheToken(t *testing.T) {
	g := &google{}
	running, err := gcal.New(gcal.Options{Client: g.client(), Clock: func() int64 { return NOW }}).Start(conntest.Ctx(t, nil, "gcal", cfg(t), with(map[string]string{"refreshToken": "1//r"})))
	testutil.Must(t, err)
	set, err := running.Tools(context.Background(), runtimeTurn)
	testutil.Must(t, err)
	pt, _ := set.Get("get_calendar")
	if pt.Class != "read" || !pt.Untrusted {
		t.Fatalf("%+v", pt)
	}
	out := conntest.Exec(t, pt.Tool, `{"from":"today"}`)
	if out.N("count") != 2 || strings.Join(out.Strs("events", "title"), ",") != "Libur,Standup" {
		t.Fatal(out)
	}
	var listed *url.URL
	for _, c := range g.Calls() {
		if strings.Contains(c.URL, "/events") {
			listed, _ = url.Parse(c.URL)
			break
		}
	}
	if listed.Query().Get("timeMin") != "2026-11-09T16:00:00.000Z" || listed.Query().Get("timeMax") != "2026-11-10T16:00:00.000Z" {
		t.Fatal(listed)
	}
	conntest.Exec(t, pt.Tool, `{}`)
	n := 0
	for _, c := range g.Calls() {
		if strings.Contains(c.URL, "oauth2") {
			n++
		}
	}
	if n != 1 {
		t.Fatal(n)
	}
	if r := conntest.Exec(t, pt.Tool, `{"from":"nanti"}`); !strings.Contains(r.S("error"), "Can't read") {
		t.Fatal(r)
	}
}

func TestRuntimeStartRefusesWithoutARefreshToken(t *testing.T) {
	if _, err := gcal.New(gcal.Options{Client: (&google{}).client()}).Start(conntest.Ctx(t, nil, "gcal", cfg(t), with(nil))); err == nil || !strings.Contains(err.Error(), "sign-in isn't done") {
		t.Fatal(err)
	}
}

func TestActionAuthURLUsesAppURLAndSignsTheStateForThisConnector(t *testing.T) {
	prev := config.Get()
	next := prev
	next.AppURL, next.MasterKey = APP, KEY
	config.Set(&next)
	t.Cleanup(func() { config.Set(&prev) })
	f := conntest.NewCtx(t, nil, "gcal", cfg(t), with(map[string]string{"refreshToken": "1//r"}))
	running, err := gcal.New(gcal.Options{Client: (&google{}).client()}).Start(f.Context)
	testutil.Must(t, err)
	res, err := running.Action(context.Background(), "authUrl", nil)
	testutil.Must(t, err)
	v := conntest.Of(res)
	if v.S("redirectUri") != APP+"/api/oauth/google/callback" {
		t.Fatal(v)
	}
	u, _ := url.Parse(v.S("url"))
	verified := gcal.VerifyState(KEY, u.Query().Get("state"), NOW)
	if !verified.OK || verified.ConnectorID != "gcal" {
		t.Fatalf("%+v", verified)
	}
	patches := f.PatchList()
	if nonce, _ := patches[len(patches)-1].Get("oauthNonce"); nonce != verified.Nonce {
		t.Fatal(nonce)
	}
}

func TestEventsActionReturnsIDsVideoLinksAndSkipsNothingTheEngineNeeds(t *testing.T) {
	g := &google{events: []any{
		map[string]any{"id": "m1", "summary": "Call", "start": map[string]any{"dateTime": "2026-11-10T13:00:00+08:00"}, "end": map[string]any{"dateTime": "2026-11-10T14:00:00+08:00"}, "hangoutLink": "https://meet.google.com/abc-defg-hij"},
		map[string]any{"id": "z1", "summary": "Zoom", "start": map[string]any{"dateTime": "2026-11-10T15:00:00+08:00"}, "end": map[string]any{"dateTime": "2026-11-10T16:00:00+08:00"}, "conferenceData": map[string]any{"entryPoints": []any{map[string]any{"entryPointType": "phone", "uri": "tel:+1"}, map[string]any{"entryPointType": "video", "uri": "https://zoom.us/j/1"}}}},
		map[string]any{"id": "d1", "summary": "Declined", "start": map[string]any{"dateTime": "2026-11-10T17:00:00+08:00"}, "end": map[string]any{"dateTime": "2026-11-10T18:00:00+08:00"}, "attendees": []any{map[string]any{"self": true, "responseStatus": "declined"}}},
	}}
	running, err := gcal.New(gcal.Options{Client: g.client(), Clock: func() int64 { return NOW }}).Start(conntest.Ctx(t, nil, "gcal", cfg(t), with(map[string]string{"refreshToken": "1//r"})))
	testutil.Must(t, err)
	in := jsonx.NewMap[any]()
	in.Set("from", NOW)
	in.Set("to", NOW+24*3600_000)
	out, err := running.Action(context.Background(), gcal.EventsAction, in)
	testutil.Must(t, err)
	got := jsonx.Stringify(out)
	for _, want := range []string{`"id":"m1"`, `"meetLink":"https://meet.google.com/abc-defg-hij"`, `"meetLink":"https://zoom.us/j/1"`, `"id":"d1"`, `"declined":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if _, err := running.Action(context.Background(), gcal.EventsAction, jsonx.NewMap[any]()); err == nil {
		t.Fatal("events needs a range")
	}
}

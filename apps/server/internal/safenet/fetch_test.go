package safenet_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/safenet"
)

// Ports packages/core/src/net/fetch.test.ts.

const PUBLIC = "93.184.216.34"

type route struct {
	status  int
	headers map[string]string
	body    string
	chunks  [][]byte
}

// fakeTransport answers by URL and records every request it saw.
func fakeTransport(routes map[string]route) (safenet.Transport, *[]safenet.TransportRequest) {
	var mu sync.Mutex
	seen := []safenet.TransportRequest{}
	tr := func(req safenet.TransportRequest) (*safenet.TransportResponse, error) {
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
		r, ok := routes[req.URL.Href()]
		if !ok {
			return &safenet.TransportResponse{Status: 404, Header: http.Header{}, Body: strings.NewReader(""), Cancel: func() {}}, nil
		}
		h := http.Header{}
		if r.headers == nil {
			h.Set("content-type", "text/plain")
		}
		for k, v := range r.headers {
			h.Set(k, v)
		}
		var body io.Reader = strings.NewReader(r.body)
		if r.chunks != nil {
			body = bytes.NewReader(bytes.Join(r.chunks, nil))
		}
		status := r.status
		if status == 0 {
			status = 200
		}
		return &safenet.TransportResponse{Status: status, Header: h, Body: body, Cancel: func() {}}, nil
	}
	return tr, &seen
}

func fetchCode(err error) string {
	var e *safenet.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func TestFetchesAPublicURLPinnedToTheResolvedAddress(t *testing.T) {
	tr, seen := fakeTransport(map[string]route{"https://example.com/": {body: "hello", headers: map[string]string{"content-type": "text/plain; charset=utf-8"}}})
	res, err := safenet.Fetch(context.Background(), "https://example.com/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"example.com": {PUBLIC}})})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Text() != "hello" || res.ContentType != "text/plain" || res.Charset != "utf-8" {
		t.Fatalf("%+v", res)
	}
	if (*seen)[0].Address != (safenet.ResolvedAddress{Address: PUBLIC, Family: 4}) || !strings.Contains((*seen)[0].Headers["user-agent"], "Palui") {
		t.Fatalf("%+v", (*seen)[0])
	}
}

func TestRefusesHostsThatResolveInsideBeforeConnecting(t *testing.T) {
	tr, seen := fakeTransport(nil)
	_, err := safenet.Fetch(context.Background(), "https://evil.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"evil.test": {"169.254.169.254"}})})
	if fetchCode(err) != "blocked_address" || len(*seen) != 0 {
		t.Fatal(err)
	}
}

func TestRefusesLiteralInternalAddressesAndBadSchemes(t *testing.T) {
	tr, _ := fakeTransport(nil)
	if _, err := safenet.Fetch(context.Background(), "http://127.0.0.1:4891/", safenet.Options{Transport: tr}); fetchCode(err) != "blocked_address" {
		t.Fatal(err)
	}
	if _, err := safenet.Fetch(context.Background(), "file:///etc/passwd", safenet.Options{Transport: tr}); fetchCode(err) != "blocked_protocol" {
		t.Fatal(err)
	}
}

func TestFollowsRedirectsAndRechecksEveryHop(t *testing.T) {
	tr, seen := fakeTransport(map[string]route{
		"https://a.test/":      {status: 302, headers: map[string]string{"location": "/b"}},
		"https://a.test/b":     {status: 301, headers: map[string]string{"location": "https://c.test/final"}},
		"https://c.test/final": {body: "done"},
	})
	res, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}, "c.test": {"1.1.1.1"}})})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "done" || res.URL != "https://c.test/final" || !reflect.DeepEqual(res.Redirects, []string{"https://a.test/", "https://a.test/b"}) {
		t.Fatalf("%+v", res)
	}
	var addrs []string
	for _, s := range *seen {
		addrs = append(addrs, s.Address.Address)
	}
	if !reflect.DeepEqual(addrs, []string{PUBLIC, PUBLIC, "1.1.1.1"}) {
		t.Fatal(addrs)
	}
}

func TestRefusesARedirectIntoTheInternalNetwork(t *testing.T) {
	tr, seen := fakeTransport(map[string]route{"https://a.test/": {status: 302, headers: map[string]string{"location": "http://169.254.169.254/latest/meta-data/"}}})
	_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
	if fetchCode(err) != "blocked_address" || len(*seen) != 1 {
		t.Fatal(err)
	}
}

func TestRefusesARedirectToANameThatRebindsToLoopback(t *testing.T) {
	tr, _ := fakeTransport(map[string]route{"https://a.test/": {status: 307, headers: map[string]string{"location": "https://rebind.test/"}}})
	_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}, "rebind.test": {"127.0.0.1"}})})
	if fetchCode(err) != "blocked_address" {
		t.Fatal(err)
	}
}

func TestStopsAfterTooManyRedirects(t *testing.T) {
	routes := map[string]route{}
	for i := 0; i < 10; i++ {
		routes["https://a.test/"+string(rune('0'+i))] = route{status: 302, headers: map[string]string{"location": "/" + string(rune('0'+i+1))}}
	}
	tr, _ := fakeTransport(routes)
	_, err := safenet.Fetch(context.Background(), "https://a.test/0", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
	if fetchCode(err) != "too_many_redirects" {
		t.Fatal(err)
	}
}

func TestDropsCredentialsOnCrossOriginRedirects(t *testing.T) {
	tr, seen := fakeTransport(map[string]route{
		"https://a.test/": {status: 302, headers: map[string]string{"location": "https://b.test/"}},
		"https://b.test/": {body: "ok"},
	})
	_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}, "b.test": {PUBLIC}}), Headers: map[string]string{"Authorization": "Bearer x"}})
	if err != nil {
		t.Fatal(err)
	}
	if (*seen)[0].Headers["authorization"] != "Bearer x" {
		t.Fatal("first hop lost the header")
	}
	if _, ok := (*seen)[1].Headers["authorization"]; ok {
		t.Fatal("credentials crossed origins")
	}
}

func TestPostSwitchesToGetOn303(t *testing.T) {
	tr, seen := fakeTransport(map[string]route{
		"https://a.test/":     {status: 303, headers: map[string]string{"location": "/done"}},
		"https://a.test/done": {body: "ok"},
	})
	body := "{}"
	_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Method: "POST", Body: &body, Headers: map[string]string{"content-type": "application/json"}, Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
	if err != nil {
		t.Fatal(err)
	}
	second := (*seen)[1]
	if second.Method != "GET" || second.Body != nil || second.Headers["content-type"] != "" {
		t.Fatalf("%+v", second)
	}
}

func TestEnforcesTheByteLimitOrTruncates(t *testing.T) {
	big := bytes.Repeat([]byte{65}, 1000)
	tr, _ := fakeTransport(map[string]route{"https://a.test/": {chunks: [][]byte{big, big, big}}})
	opts := safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}}), MaxBytes: 1500}
	if _, err := safenet.Fetch(context.Background(), "https://a.test/", opts); fetchCode(err) != "too_large" {
		t.Fatal(err)
	}
	opts.Truncate = true
	res, err := safenet.Fetch(context.Background(), "https://a.test/", opts)
	if err != nil || len(res.Bytes) != 1500 || !res.Truncated {
		t.Fatal(err)
	}
}

func TestRefusesADeclaredContentLengthOverTheLimit(t *testing.T) {
	tr, _ := fakeTransport(map[string]route{"https://a.test/": {headers: map[string]string{"content-length": "999999999", "content-type": "text/plain"}, body: "x"}})
	_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}}), MaxBytes: 10})
	if fetchCode(err) != "too_large" {
		t.Fatal(err)
	}
}

func TestChecksTheContentTypeAllowlist(t *testing.T) {
	tr, _ := fakeTransport(map[string]route{
		"https://a.test/pdf":  {headers: map[string]string{"content-type": "application/pdf"}, body: "%PDF"},
		"https://a.test/json": {headers: map[string]string{"content-type": "application/ld+json"}, body: "{}"},
	})
	opts := safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}}), AllowedContentTypes: []string{"text/*", "application/json", "+json"}}
	if _, err := safenet.Fetch(context.Background(), "https://a.test/pdf", opts); fetchCode(err) != "content_type" {
		t.Fatal(err)
	}
	res, err := safenet.Fetch(context.Background(), "https://a.test/json", opts)
	if err != nil || res.ContentType != "application/ld+json" {
		t.Fatal(err)
	}
	var v map[string]any
	if err := res.JSON(&v); err != nil {
		t.Fatal(err)
	}
}

func TestTimesOut(t *testing.T) {
	tr := func(req safenet.TransportRequest) (*safenet.TransportResponse, error) {
		<-req.Ctx.Done()
		return nil, errors.New("aborted")
	}
	_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}}), TimeoutMs: 30})
	if fetchCode(err) != "timeout" {
		t.Fatal(err)
	}
}

func TestAbortedByTheCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tr := func(req safenet.TransportRequest) (*safenet.TransportResponse, error) {
		cancel()
		<-req.Ctx.Done()
		return nil, errors.New("aborted")
	}
	_, err := safenet.Fetch(ctx, "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
	if fetchCode(err) != "aborted" {
		t.Fatal(err)
	}
}

func TestReturnsNon2xxAnswersInsteadOfFailing(t *testing.T) {
	tr, _ := fakeTransport(map[string]route{"https://a.test/": {status: 500, body: "boom"}})
	res, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
	if err != nil || res.OK || res.Status != 500 {
		t.Fatal(err)
	}
}

func TestDecodesTheDeclaredCharset(t *testing.T) {
	tr, _ := fakeTransport(map[string]route{"https://a.test/": {headers: map[string]string{"content-type": "text/plain; charset=ISO-8859-1"}, body: "caf\xe9"}})
	res, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
	if err != nil || res.Text() != "café" || res.Charset != "iso-8859-1" {
		t.Fatal(err, res.Text())
	}
}

// The real transport against a loopback server: the socket goes to the pinned
// address no matter what the hostname would resolve to.
func TestHTTPTransport(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		if r.URL.Path == "/gz" {
			w.Header().Set("content-type", "text/plain")
			w.Header().Set("content-encoding", "gzip")
			zw := gzip.NewWriter(w)
			zw.Write([]byte("compressed hello"))
			zw.Close()
			return
		}
		w.Header().Set("content-type", "text/plain")
		w.Write([]byte("pinned"))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	read := func(raw string) (int, string) {
		u, err := safenet.ParseURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		res, err := safenet.HTTPTransport(safenet.TransportRequest{URL: u, Address: safenet.ResolvedAddress{Address: "127.0.0.1", Family: 4}, Method: "GET", Headers: map[string]string{}, Ctx: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		defer res.Cancel()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.Status, string(b)
	}
	if status, text := read("http://does-not-exist.invalid:" + port + "/"); status != 200 || text != "pinned" {
		t.Fatal(status, text)
	}
	if hosts[len(hosts)-1] != "does-not-exist.invalid:"+port {
		t.Fatal(hosts)
	}
	if _, text := read("http://x.invalid:" + port + "/gz"); text != "compressed hello" {
		t.Fatal(text)
	}
}

func TestManualRedirectsReturnsTheRedirect(t *testing.T) {
	tr, seen := fakeTransport(map[string]route{
		"https://a.test/": {status: 302, headers: map[string]string{"location": "https://b.test/"}},
		"https://b.test/": {body: "followed"},
	})
	res, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{ManualRedirects: true, Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}, "b.test": {PUBLIC}})})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 302 || res.OK || res.Header.Get("location") != "https://b.test/" || len(res.Redirects) != 0 || len(*seen) != 1 {
		t.Fatalf("%+v, %d calls", res, len(*seen))
	}
}

func TestPassesAnyMethodAndBodyThrough(t *testing.T) {
	for _, method := range []string{"DELETE", "PATCH"} {
		tr, seen := fakeTransport(map[string]route{"https://a.test/": {body: "ok"}})
		body := `{"a":1}`
		_, err := safenet.Fetch(context.Background(), "https://a.test/", safenet.Options{Method: method, Body: &body, Transport: tr, Resolver: resolver(map[string][]string{"a.test": {PUBLIC}})})
		if err != nil {
			t.Fatal(err)
		}
		got := (*seen)[0]
		if got.Method != method || got.Body == nil || *got.Body != body {
			t.Fatalf("%s: %+v", method, got)
		}
	}
}

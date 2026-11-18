package webfetch_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func response(body, contentType string, status int, truncated bool) *safenet.Response {
	if status == 0 {
		status = 200
	}
	return &safenet.Response{URL: "https://example.com/post", Status: status, OK: status < 300, ContentType: contentType, Charset: "utf-8", Bytes: []byte(body), Truncated: truncated}
}

func fake(r *safenet.Response, err error) webfetch.Fetcher {
	return func(context.Context, string, safenet.Options) (*safenet.Response, error) { return r, err }
}

var article = `<!doctype html><html><head><title>Kopi Banjar | Blog</title><script>var x = 1</script></head>
<body><nav>Home About Contact</nav>
<article><h1>Kopi Banjar</h1>
<p>` + strings.Repeat("Kopi dari Kalimantan Selatan punya rasa yang khas dan aromanya kuat sekali. ", 8) + `</p>
<p>` + strings.Repeat("Banyak warung di Banjarmasin menyajikannya dengan susu kental manis. ", 8) + `</p>
</article><footer>Copyright</footer></body></html>`

func TestExtractReadableKeepsTheArticleTextAndTitle(t *testing.T) {
	title, text := webfetch.ExtractReadable(article)
	if !strings.Contains(title, "Kopi Banjar") || !strings.Contains(text, "Kalimantan Selatan") || !strings.Contains(text, "susu kental manis") || strings.Contains(text, "var x") {
		t.Fatal(title, text)
	}
}

func TestExtractReadableFallsBackToBodyTextForTinyPages(t *testing.T) {
	title, text := webfetch.ExtractReadable("<html><head><title>Hi</title><style>p{}</style></head><body><p>Just a line</p></body></html>")
	if title != "Hi" || text != "Just a line" {
		t.Fatal(title, text)
	}
}

func TestFetchPageReturnsTitleAndReadableTextForHTML(t *testing.T) {
	out := conntest.Of(webfetch.FetchPage(context.Background(), "https://example.com/post", fake(response(article, "text/html", 0, false), nil)))
	if !out.B("ok") || out.S("url") != "https://example.com/post" || out.S("contentType") != "text/html" || out.B("truncated") || !strings.Contains(out.S("text"), "Kalimantan Selatan") {
		t.Fatal(out)
	}
}

func TestFetchPagePassesJSONAndPlainTextThrough(t *testing.T) {
	j := conntest.Of(webfetch.FetchPage(context.Background(), "https://api.example.com/x", fake(response(`{"a":1,"b":[1,2]}`, "application/json", 0, false), nil)))
	if !j.B("ok") || j.S("text") != "{\n  \"a\": 1,\n  \"b\": [\n    1,\n    2\n  ]\n}" {
		t.Fatal(j)
	}
	txt := conntest.Of(webfetch.FetchPage(context.Background(), "https://example.com/robots.txt", fake(response("  User-agent: *\n", "text/plain", 0, false), nil)))
	if !txt.B("ok") || txt.S("title") != "" || txt.S("text") != "User-agent: *" {
		t.Fatal(txt)
	}
}

func TestFetchPageTruncatesLongTextToAbout12kCharacters(t *testing.T) {
	out := conntest.Of(webfetch.FetchPage(context.Background(), "https://example.com/big", fake(response(strings.Repeat("a", 50_000), "text/plain", 0, false), nil)))
	if !out.B("truncated") || strx.Len16(out.S("text")) > webfetch.MaxTextChars+20 {
		t.Fatal(len(out.S("text")))
	}
}

func TestFetchPageReportsGuardRefusalsAndHTTPErrorsAsResultsNotThrows(t *testing.T) {
	blocked := conntest.Of(webfetch.FetchPage(context.Background(), "http://169.254.169.254/", fake(nil, &safenet.Error{Code: "blocked_address", Message: "Address 169.254.169.254 is link-local"})))
	if blocked.B("ok") || !regexp.MustCompile(`SSRF guard`).MatchString(blocked.S("error")) {
		t.Fatal(blocked)
	}
	notFound := conntest.Of(webfetch.FetchPage(context.Background(), "https://example.com/404", fake(response("nope", "text/html", 404, false), nil)))
	if notFound.String() != `{"ok":false,"url":"https://example.com/post","error":"The server answered 404"}` {
		t.Fatal(notFound)
	}
}

func TestFetchPageUsesTheRealGuardByDefaultLoopbackIsRefusedWithoutARequest(t *testing.T) {
	out := conntest.Of(webfetch.FetchPage(context.Background(), "http://127.0.0.1:4891/control", nil))
	if out.B("ok") || !strings.Contains(out.S("error"), "SSRF guard") {
		t.Fatal(out)
	}
}

func TestRuntimeExposesFetchPageAsAnUntrustedReadTool(t *testing.T) {
	c := conntest.Ctx(t, nil, "webfetch", nil, nil)
	running, err := webfetch.Runtime.Start(c)
	testutil.Must(t, err)
	set, err := running.Tools(context.Background(), connectors.TurnInfo{RunID: "r", ChatID: "c", Channel: "web", Trigger: "message", TZ: "Asia/Makassar"})
	testutil.Must(t, err)
	if strings.Join(set.Keys(), ",") != "fetch_page,search_web" {
		t.Fatal(set.Keys())
	}
	for _, name := range set.Keys() {
		// Both stay guest-safe: a group agent's guests get these and nothing else.
		pt, _ := set.Get(name)
		if pt.Class != "read" || !pt.Untrusted || !pt.GuestSafe || pt.Data != connectors.DataNone {
			t.Fatalf("%s: %+v", name, pt)
		}
	}
	res, err := webfetch.Runtime.Test(c)
	testutil.Must(t, err)
	if !res.OK {
		t.Fatal(res)
	}
}

const ddgPage = `<!doctype html><html><body><div id="links">
<div class="result results_links result--ad"><h2><a class="result__a" href="https://ads.example.com/buy">Beli sawit murah</a></h2><a class="result__snippet">Iklan</a></div>
<div class="result results_links results_links_deep web-result"><h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.go.id%2Fid%2Fstatistics%2Dtable%2F2%2Fproduksi.html&amp;rut=abc">Produksi Tanaman <b>Perkebunan</b> menurut Kecamatan</a></h2>
<a class="result__snippet" href="//duckduckgo.com/l/?uddg=x">Produksi tanaman perkebunan  menurut kecamatan dan jenis tanaman.</a></div>
<div class="result results_links web-result"><h2><a class="result__a" href="https://example.org/direct">Direct link</a></h2></div>
<div class="result results_links web-result"><h2><a class="result__a" href="javascript:alert(1)">Bad scheme</a></h2></div>
</div></body></html>`

func TestParseSearchResultsSkipsAdsAndUnwrapsRedirects(t *testing.T) {
	got := webfetch.ParseSearchResults(ddgPage)
	want := []webfetch.SearchResult{
		{Title: "Produksi Tanaman Perkebunan menurut Kecamatan", URL: "https://example.go.id/id/statistics-table/2/produksi.html", Snippet: "Produksi tanaman perkebunan menurut kecamatan dan jenis tanaman."},
		{Title: "Direct link", URL: "https://example.org/direct"},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSearchWebQueriesDuckDuckGoAndReturnsResults(t *testing.T) {
	var asked string
	fetcher := func(_ context.Context, u string, opts safenet.Options) (*safenet.Response, error) {
		asked = u
		return response(ddgPage, "text/html", 0, false), nil
	}
	out := conntest.Of(webfetch.SearchWeb(context.Background(), "produksi sawit tanah laut", fetcher))
	if asked != "https://html.duckduckgo.com/html/?q=produksi+sawit+tanah+laut" {
		t.Fatal(asked)
	}
	if !out.B("ok") || out.S("query") != "produksi sawit tanah laut" || !strings.Contains(out.String(), `"url":"https://example.go.id/id/statistics-table/2/produksi.html"`) || strings.Contains(out.String(), "ads.example.com") {
		t.Fatal(out)
	}
}

func TestSearchWebTellsAnEmptySearchFromABlockedOne(t *testing.T) {
	none := conntest.Of(webfetch.SearchWeb(context.Background(), "zzqx", fake(response(`<html><body><div class="no-results">No results.</div></body></html>`, "text/html", 0, false), nil)))
	if none.String() != `{"ok":true,"query":"zzqx","results":[]}` {
		t.Fatal(none)
	}
	blocked := conntest.Of(webfetch.SearchWeb(context.Background(), "zzqx", fake(response(`<html><body><form id="challenge-form"></form></body></html>`, "text/html", 0, false), nil)))
	if blocked.B("ok") || !strings.Contains(blocked.S("error"), "without results") {
		t.Fatal(blocked)
	}
	down := conntest.Of(webfetch.SearchWeb(context.Background(), "zzqx", fake(response("busy", "text/html", 503, false), nil)))
	if down.String() != `{"ok":false,"query":"zzqx","error":"The search engine answered 503"}` {
		t.Fatal(down)
	}
}

// sequence answers each call with the next step and counts the calls.
func sequence(calls *int, steps ...func() (*safenet.Response, error)) webfetch.Fetcher {
	return func(_ context.Context, _ string, opts safenet.Options) (*safenet.Response, error) {
		if opts.TimeoutMs != webfetch.SearchTimeoutMs {
			panic("search should use its own short timeout")
		}
		step := steps[min(*calls, len(steps)-1)]
		*calls++
		return step()
	}
}

func timedOut() (*safenet.Response, error) {
	return nil, &safenet.Error{Code: safenet.CodeTimeout, Message: "Timed out after 7000 ms"}
}

func TestSearchWebTriesAgainOnceWhenDuckDuckGoHangs(t *testing.T) {
	ok := func() (*safenet.Response, error) { return response(ddgPage, "text/html", 0, false), nil }

	calls := 0
	out := conntest.Of(webfetch.SearchWeb(context.Background(), "cara pakai inhaler", sequence(&calls, timedOut, ok)))
	if !out.B("ok") || calls != 2 {
		t.Fatal(calls, out)
	}

	calls = 0
	dropped := func() (*safenet.Response, error) {
		return nil, &safenet.Error{Code: safenet.CodeNetwork, Message: "connection reset"}
	}
	if out := conntest.Of(webfetch.SearchWeb(context.Background(), "q", sequence(&calls, dropped, ok))); !out.B("ok") || calls != 2 {
		t.Fatal(calls, out)
	}

	calls = 0
	busy := func() (*safenet.Response, error) { return response("busy", "text/html", 503, false), nil }
	if out := conntest.Of(webfetch.SearchWeb(context.Background(), "q", sequence(&calls, busy, ok))); !out.B("ok") || calls != 2 {
		t.Fatal(calls, out)
	}

	// Two hung tries in a row: give up with the timeout, no third try.
	calls = 0
	out = conntest.Of(webfetch.SearchWeb(context.Background(), "q", sequence(&calls, timedOut)))
	if out.String() != `{"ok":false,"query":"q","error":"The page took too long to answer"}` || calls != 2 {
		t.Fatal(calls, out)
	}
}

func TestSearchWebDoesNotRetryAnAnswerOrACancelledTurn(t *testing.T) {
	for name, step := range map[string]func() (*safenet.Response, error){
		"4xx": func() (*safenet.Response, error) { return response("no", "text/html", 403, false), nil },
		"blocked": func() (*safenet.Response, error) {
			return response(`<form id="challenge-form"></form>`, "text/html", 0, false), nil
		},
		"refused": func() (*safenet.Response, error) {
			return nil, &safenet.Error{Code: safenet.CodeBlockedAddress, Message: "private"}
		},
	} {
		calls := 0
		webfetch.SearchWeb(context.Background(), "q", sequence(&calls, step))
		if calls != 1 {
			t.Fatal(name, calls)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	webfetch.SearchWeb(ctx, "q", sequence(&calls, timedOut))
	if calls != 1 {
		t.Fatal("cancelled", calls)
	}
}

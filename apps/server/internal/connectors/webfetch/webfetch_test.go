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
	if strings.Join(set.Keys(), ",") != "fetch_page" {
		t.Fatal(set.Keys())
	}
	pt, _ := set.Get("fetch_page")
	if pt.Class != "read" || !pt.Untrusted {
		t.Fatalf("%+v", pt)
	}
	res, err := webfetch.Runtime.Test(c)
	testutil.Must(t, err)
	if !res.OK {
		t.Fatal(res)
	}
}

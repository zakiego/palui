package httpapi_test

import (
	"bytes"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func saveFile(t *testing.T, d *db.DB, title, mime, filename string, b []byte, now int64) db.AttachmentMeta {
	t.Helper()
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, now)
	testutil.Must(t, err)
	saved, err := attachments.Save(d, attachments.SaveInput{ChatID: chat.ID, Title: title, MimeType: mime, Filename: filename, Bytes: b}, testutil.TestKey, now)
	testutil.Must(t, err)
	return saved.Row
}

// --- attachments-route.test.ts ----------------------------------------------------------

var pdf = []byte("%PDF-1.7 tiket")

func TestAttachmentRefusesWithoutASession(t *testing.T) {
	e := newEnv(t, nil)
	row := saveFile(t, e.d, "Tiket", "application/pdf", `tiket "BDJ".pdf`, pdf, 2000)
	if res := e.do("GET", "/api/attachments/"+row.ID, ""); res.StatusCode != 401 || readBody(res) != `{"error":"UNAUTHORIZED"}` {
		t.Fatal(res.StatusCode)
	}
}

func TestAttachmentServesTheDecryptedFileAsADownloadWithSafeHeaders(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	row := saveFile(t, e.d, "Tiket", "application/pdf", `tiket "BDJ".pdf`, pdf, 2000)
	res := e.do("GET", "/api/attachments/"+row.ID, "", cookie(token))
	b, _ := io.ReadAll(res.Body)
	h := res.Header
	if res.StatusCode != 200 || !bytes.Equal(b, pdf) || h.Get("content-type") != "application/pdf" || h.Get("x-content-type-options") != "nosniff" || h.Get("cache-control") != "private, no-store" {
		t.Fatalf("%d %v", res.StatusCode, h)
	}
	if got := h.Get("content-disposition"); got != `attachment; filename="tiket BDJ.pdf"; filename*=UTF-8''tiket%20BDJ.pdf` {
		t.Fatal(got)
	}
	res = e.do("GET", "/api/attachments/"+row.ID+"?inline=1", "", cookie(token))
	if !strings.HasPrefix(res.Header.Get("content-disposition"), `inline; filename="tiket BDJ.pdf"`) || res.Header.Get("x-content-type-options") != "nosniff" || res.Header.Get("content-security-policy") != "default-src 'none'; frame-ancestors 'self'" {
		t.Fatal(res.Header)
	}
}

func TestAttachment404sOnUnknownOrMalformedIDs(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	for _, id := range []string{"att_nope", "x", "..%2Fetc"} {
		if res := e.do("GET", "/api/attachments/"+id, "", cookie(token)); res.StatusCode != 404 {
			t.Errorf("%s: %d", id, res.StatusCode)
		}
	}
}

// --- share-route.test.ts ---------------------------------------------------------------

var (
	png  = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	docx = []byte{0x50, 0x4b, 0x03, 0x04, 0, 0}
)

func share(t *testing.T, e *env, id, visibility string) string {
	t.Helper()
	s, ok, err := attachments.CreateShare(e.d, attachments.ShareInput{AttachmentID: id, Visibility: visibility, Duration: "1h", CreatedBy: "owner"}, e.now.Load())
	testutil.Must(t, err)
	if !ok {
		t.Fatal("no attachment")
	}
	return s.Token
}

func TestShareOpensAPublicImageInlineAndCountsTheOpen(t *testing.T) {
	e := newEnv(t, nil)
	row := saveFile(t, e.d, "Struk", "image/png", "struk.png", png, e.now.Load())
	token := share(t, e, row.ID, "public")
	res := e.do("GET", "/api/share/"+token, "")
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.Equal(b, png) || res.Header.Get("content-type") != "image/png" || !strings.HasPrefix(res.Header.Get("content-disposition"), `inline; filename="struk.png"`) || !strings.Contains(res.Header.Get("content-security-policy"), "sandbox") {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	if res := e.do("GET", "/api/share/"+token+"?download=1", ""); !strings.HasPrefix(res.Header.Get("content-disposition"), "attachment;") {
		t.Fatal(res.Header)
	}
	var opens int
	testutil.Must(t, e.d.QueryRow("SELECT open_count FROM attachment_shares").Scan(&opens))
	if opens != 2 {
		t.Fatalf("opens %d", opens)
	}
}

func TestShareAlwaysDownloadsATypeTheBrowserCantPreview(t *testing.T) {
	e := newEnv(t, nil)
	row := saveFile(t, e.d, "Kontrak", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "kontrak.docx", docx, e.now.Load())
	token := share(t, e, row.ID, "public")
	if res := e.do("GET", "/api/share/"+token, ""); !strings.HasPrefix(res.Header.Get("content-disposition"), `attachment; filename="kontrak.docx"`) {
		t.Fatal(res.Header)
	}
}

func TestShareNeedsASessionForAPrivateLink(t *testing.T) {
	e := newEnv(t, nil)
	row := saveFile(t, e.d, "Struk", "image/png", "struk.png", png, e.now.Load())
	token := share(t, e, row.ID, "private")
	res := e.do("GET", "/api/share/"+token, "")
	if res.StatusCode != 401 || readBody(res) != "This link is private. Sign in to the dashboard first, then open the link again.\n" {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("GET", "/api/share/"+token, "", cookie(e.signIn())); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
}

func TestShare410sAnExpiredLinkAnd404sAnUnknownOne(t *testing.T) {
	e := newEnv(t, nil)
	row := saveFile(t, e.d, "Struk", "image/png", "struk.png", png, e.now.Load())
	token := share(t, e, row.ID, "public")
	e.now.Add(60 * 60 * 1000)
	res := e.do("GET", "/api/share/"+token, "")
	if res.StatusCode != 410 || res.Header.Get("content-type") != "text/plain; charset=utf-8" || readBody(res) != "This link has expired. Ask for a new one.\n" {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("GET", "/api/share/"+strings.Repeat("x", 32), ""); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
}

func TestFileNamesWithNonASCII(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	row := saveFile(t, e.d, "Struk", "image/png", "struk é 😀.png", png, e.now.Load())
	res := e.do("GET", "/api/attachments/"+row.ID, "", cookie(token))
	if got := res.Header.Get("content-disposition"); got != `attachment; filename="struk _ __.png"; filename*=UTF-8''struk%20%C3%A9%20%F0%9F%98%80.png` {
		t.Fatal(got)
	}
}

// --- oauth-return.test.ts -----------------------------------------------------------------

func TestOAuthReturnStripsControlAndBidiCharacters(t *testing.T) {
	if got := httpapi.CleanDetail("Google said:\r\nSet-Cookie: x=1\u0000‮ evil"); got != "Google said: Set-Cookie: x=1 evil" {
		t.Fatalf("%q", got)
	}
}

func TestOAuthReturnCapsTheDetail(t *testing.T) {
	if got := httpapi.CleanDetail(strings.Repeat("a", 500)); len(got) != httpapi.DetailMax {
		t.Fatal(len(got))
	}
	if got := httpapi.CleanDetail(strings.Repeat("\U0001F600", 300)); got != strings.Repeat("\U0001F600", httpapi.DetailMax) {
		t.Fatal(len([]rune(got)))
	}
}

func TestOAuthReturnAlwaysPointsAtTheConnectorsPage(t *testing.T) {
	raw := httpapi.OAuthReturnURL("https://palui.example.com", false, "Google said: //evil.example\n")
	u, err := url.Parse(raw)
	testutil.Must(t, err)
	if u.Scheme+"://"+u.Host != "https://palui.example.com" || u.Path != "/app/connectors" || u.Query().Get("oauth") != "error" || u.Query().Get("detail") != "Google said: //evil.example" {
		t.Fatal(raw)
	}
	if raw != "https://palui.example.com/app/connectors?oauth=error&detail=Google+said%3A+%2F%2Fevil.example" {
		t.Fatal(raw)
	}
}

func TestGoogleCallbackRedirectsWithTheProblem(t *testing.T) {
	e := newEnv(t, nil)
	res := e.do("GET", "/api/oauth/google/callback?error=access_denied", "")
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("location"), appURL+"/app/connectors?oauth=error&detail=") {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("location"))
	}
}

func TestRootRedirectsToTheDashboard(t *testing.T) {
	e := newEnv(t, nil)
	res := e.do("GET", "/", "")
	if res.StatusCode != 302 || res.Header.Get("location") != "/app" {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("GET", "/app/connectors", ""); res.StatusCode != 404 {
		t.Fatalf("no web handler: %d", res.StatusCode)
	}
}

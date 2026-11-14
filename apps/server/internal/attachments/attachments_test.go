package attachments_test

import (
	"bytes"
	"encoding/base64"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/attachments/service.test.ts, refs.test.ts and shares.test.ts.

const KEY = "test-master-key-0123456789abcdef0123456789"

var (
	NOW  = testutil.NOW
	HOUR = testutil.HOUR
	PDF  = []byte("%PDF-1.7\nktp")
	PNG  = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	JPEG = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46}
)

func save(t *testing.T, d db.Q, in attachments.SaveInput, now int64) attachments.Saved {
	t.Helper()
	s, err := attachments.Save(d, in, KEY, now)
	testutil.Must(t, err)
	return s
}

func refuses(t *testing.T, mime string, b []byte, contains string) {
	t.Helper()
	_, err := attachments.CheckFile(mime, b)
	if !attachments.IsError(err) || !strings.Contains(err.Error(), contains) {
		t.Errorf("CheckFile(%s): %v", mime, err)
	}
}

func TestCheckFileAcceptsAllowedTypesWhoseBytesMatch(t *testing.T) {
	for _, c := range []struct {
		mime string
		b    []byte
		want string
	}{{"application/pdf", PDF, "document"}, {"image/png; charset=binary", PNG, "image"}, {"text/plain", []byte("halo"), "document"}} {
		if got, err := attachments.CheckFile(c.mime, c.b); err != nil || got != c.want {
			t.Errorf("%s: %s %v", c.mime, got, err)
		}
	}
}

func TestCheckFileRefusesUnknownMismatchedEmptyAndOversized(t *testing.T) {
	refuses(t, "image/svg+xml", []byte("<svg/>"), "")
	refuses(t, "text/html", []byte("<script>"), "can't be saved")
	refuses(t, "audio/ogg", []byte("OggS"), "")
	refuses(t, "application/pdf", []byte("MZ\x90\x00"), "doesn't match")
	refuses(t, "application/pdf", []byte{}, "empty")
	big := make([]byte, attachments.MaxBytes+1)
	copy(big, PDF)
	refuses(t, "application/pdf", big, "over the 10.0 MB limit")
}

func TestSavedAttachmentsEncryptDedupeAndScope(t *testing.T) {
	d, chatID, otherID := testutil.Setup(t)
	s := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "KTP Zaki", Description: "KTP, NIK 6371...", MimeType: "application/pdf", Filename: "../../ktp.pdf", Bytes: PDF}, NOW)
	row := s.Row
	if s.Deduped || row.Title != "KTP Zaki" || row.Kind != "document" || row.Size != int64(len(PDF)) || *row.Filename != "ktp.pdf" || row.CreatedAt != NOW {
		t.Fatalf("%+v", s)
	}
	if strings.Contains(jsonx.Stringify(row), `"data"`) {
		t.Fatal("meta has data")
	}
	var stored []byte
	testutil.Must(t, d.QueryRow("SELECT data FROM saved_attachments").Scan(&stored))
	if bytes.Contains(stored, PDF) {
		t.Fatal("stored in plain")
	}
	f, ok, err := attachments.Read(d, row.ID, KEY)
	if err != nil || !ok || !bytes.Equal(f.Bytes, PDF) {
		t.Fatal(err)
	}
	if _, _, err := attachments.Read(d, row.ID, KEY+"x"); err == nil {
		t.Fatal("wrong key read")
	}
	if again := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "lagi", MimeType: "application/pdf", Bytes: PDF}, NOW+1); !again.Deduped || again.Row.ID != row.ID {
		t.Fatalf("%+v", again)
	}
	save(t, d, attachments.SaveInput{ChatID: otherID, Title: "KTP di chat lain", MimeType: "application/pdf", Bytes: PDF}, NOW+2)
	save(t, d, attachments.SaveInput{ChatID: chatID, Title: "Struk 50%_off", MimeType: "image/png", Bytes: PNG}, NOW+3)

	if n, _ := attachments.Count(d); n != 3 {
		t.Fatal(n)
	}
	titles := func(opts attachments.ListOptions) []string {
		list, err := attachments.List(d, opts)
		testutil.Must(t, err)
		out := []string{}
		for _, a := range list {
			out = append(out, a.Title)
		}
		return out
	}
	for _, c := range []struct {
		opts attachments.ListOptions
		want []string
	}{
		{attachments.ListOptions{ChatID: chatID}, []string{"Struk 50%_off", "KTP Zaki"}},
		{attachments.ListOptions{ChatID: chatID, Query: "ktp"}, []string{"KTP Zaki"}},
		{attachments.ListOptions{ChatID: chatID, Query: "6371"}, []string{"KTP Zaki"}},
		{attachments.ListOptions{ChatID: chatID, Query: "%"}, []string{"Struk 50%_off"}},
	} {
		if got := titles(c.opts); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: %v", c.opts, got)
		}
	}
	if len(titles(attachments.ListOptions{})) != 3 {
		t.Fatal("all")
	}
	if ok, _ := attachments.Delete(d, row.ID); !ok {
		t.Fatal("delete")
	}
	if _, ok, _ := attachments.Get(d, row.ID); ok {
		t.Fatal("still there")
	}
	if ok, _ := attachments.Delete(d, row.ID); ok {
		t.Fatal("deleted twice")
	}
}

func addMessage(t *testing.T, d db.Q, chatID, role, runID, text string, media []map[string]any, at int64) {
	t.Helper()
	content, err := db.Raw(map[string]any{"text": text, "media": media})
	testutil.Must(t, err)
	_, err = d.Exec("INSERT INTO messages (id, chat_id, role, content, run_id, created_at) VALUES (?, ?, ?, ?, ?, ?)", secret.NewID("msg"), chatID, role, content, runID, at)
	testutil.Must(t, err)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestTurnCandidatesFindsImagesAndDocumentsInOrder(t *testing.T) {
	d, chatID, otherID := testutil.Setup(t)
	addMessage(t, d, chatID, "user", "run_1", "", []map[string]any{{"kind": "image", "mimeType": "image/png", "data": b64(PNG)}, {"kind": "audio", "mimeType": "audio/ogg", "note": "transcribed"}}, NOW)
	addMessage(t, d, chatID, "user", "run_1", "", []map[string]any{{"kind": "document", "mimeType": "application/pdf", "filename": "tiket.pdf", "data": b64(PDF)}, {"kind": "document", "mimeType": "application/pdf", "filename": "old.pdf"}}, NOW+1)
	addMessage(t, d, chatID, "user", "run_0", "", []map[string]any{{"kind": "image", "mimeType": "image/png", "data": b64(PNG)}}, NOW-1)
	addMessage(t, d, otherID, "user", "run_1", "", []map[string]any{{"kind": "image", "mimeType": "image/png", "data": b64(PNG)}}, NOW)
	c, err := attachments.TurnCandidates(d, chatID, "run_1")
	testutil.Must(t, err)
	if len(c) != 2 || c[0].Index != 1 || c[0].Kind != "image" || c[0].Filename != nil || c[1].Index != 2 || c[1].Kind != "document" || *c[1].Filename != "tiket.pdf" {
		t.Fatalf("%+v", c)
	}
	if c[1].Size != int64(len(PDF)) {
		t.Fatal(c[1].Size)
	}
}

func TestRecentCandidatesNewestFirstWithin24HoursAndCapped(t *testing.T) {
	d, chatID, otherID := testutil.Setup(t)
	png := map[string]any{"kind": "image", "mimeType": "image/png", "data": b64(PNG)}
	addMessage(t, d, chatID, "user", "run_x", "lama", []map[string]any{png}, NOW-25*HOUR)
	addMessage(t, d, chatID, "user", "run_x", "Nasi   goreng", []map[string]any{png}, NOW-60_000)
	addMessage(t, d, chatID, "user", "run_x", "voice", []map[string]any{{"kind": "audio", "mimeType": "audio/ogg", "data": "AAAA"}}, NOW-50_000)
	addMessage(t, d, chatID, "user", "run_x", "Gorengan", []map[string]any{png, {"kind": "document", "mimeType": "application/pdf", "filename": "a.pdf", "data": b64(PDF)}}, NOW-30_000)
	addMessage(t, d, chatID, "assistant", "run_x", "dari bot", []map[string]any{png}, NOW-20_000)
	addMessage(t, d, otherID, "user", "run_x", "lain", []map[string]any{png}, NOW-10_000)
	c, err := attachments.RecentCandidates(d, chatID, NOW)
	testutil.Must(t, err)
	type got struct {
		i       int
		kind    string
		caption string
	}
	var g []got
	for _, x := range c {
		g = append(g, got{x.Index, x.Kind, x.Caption})
	}
	if !reflect.DeepEqual(g, []got{{1, "image", "Gorengan"}, {2, "document", "Gorengan"}, {3, "image", "Nasi goreng"}}) {
		t.Fatalf("%+v", g)
	}
	for i := 0; i < attachments.RecentFilesMax; i++ {
		addMessage(t, d, chatID, "user", "run_x", "n", []map[string]any{png}, NOW-1000+int64(i))
	}
	if c, _ := attachments.RecentCandidates(d, chatID, NOW); len(c) != attachments.RecentFilesMax {
		t.Fatal(len(c))
	}
}

func TestSafeDownloadNames(t *testing.T) {
	if got := attachments.CleanFilename("C:\\x\\a\"b\n.pdf"); got != "ab.pdf" {
		t.Fatal(got)
	}
	if attachments.CleanFilename("///") != "file" {
		t.Fatal("empty")
	}
	if attachments.DownloadName(nil, "KTP Zaki", "image/jpeg") != "KTP Zaki.jpg" || attachments.DownloadName(testutil.Ptr("tiket.pdf"), "x", "application/pdf") != "tiket.pdf" {
		t.Fatal("download name")
	}
	if attachments.FormatBytes(1280) != "1.3 KB" || attachments.FormatBytes(20*1024) != "20 KB" || attachments.FormatBytes(10) != "10 B" {
		t.Fatal(attachments.FormatBytes(1280))
	}
}

// --- refs ------------------------------------------------------------------------------

func parse(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Parse([]byte(s))
	testutil.Must(t, err)
	return v
}

func TestRefsSwapAnywhereInTheArgs(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	row := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "Struk laundry", MimeType: "image/jpeg", Bytes: JPEG}, NOW).Row
	ref := attachments.Ref(row.ID)
	args := parse(t, `{"file":"`+ref+`","name":"Struk","nested":{"files":["`+ref+`","keep me"]},"note":"see `+ref+`","n":3}`)
	if ids := attachments.FindRefs(args); !reflect.DeepEqual(ids, []string{row.ID}) {
		t.Fatal(ids)
	}
	out, err := attachments.ResolveRefs(d, args, attachments.ResolveOptions{ChatID: chatID, MasterKey: KEY})
	testutil.Must(t, err)
	url := "data:image/jpeg;base64," + b64(JPEG)
	want := `{"file":"` + url + `","name":"Struk","nested":{"files":["` + url + `","keep me"]},"note":"see ` + ref + `","n":3}`
	if jsonx.Stringify(out) != want {
		t.Fatal(jsonx.Stringify(out))
	}
	if f, _ := args.(*jsonx.Object).Get("file"); f != ref {
		t.Fatal("args changed")
	}
}

func TestRefsLeaveArgsWithoutRefsAlone(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	args := parse(t, `{"attachmentId":"att_from_another_app","ids":["a"]}`)
	out, err := attachments.ResolveRefs(d, args, attachments.ResolveOptions{ChatID: chatID, MasterKey: KEY})
	if err != nil || out != args {
		t.Fatal("not the same value")
	}
}

func TestRefsRefuseAnotherChatOrAMissingFile(t *testing.T) {
	d, chatID, otherID := testutil.Setup(t)
	row := save(t, d, attachments.SaveInput{ChatID: otherID, Title: "KTP", MimeType: "image/jpeg", Bytes: JPEG}, NOW).Row
	opts := attachments.ResolveOptions{ChatID: chatID, MasterKey: KEY}
	if _, err := attachments.ResolveRefs(d, map[string]any{"file": attachments.Ref(row.ID)}, opts); err == nil {
		t.Fatal("other chat")
	} else if _, ok := err.(*attachments.RefError); !ok {
		t.Fatal(err)
	}
	if _, err := attachments.ResolveRefs(d, map[string]any{"file": attachments.Ref("att_missing")}, opts); err == nil || !strings.Contains(err.Error(), "No saved attachment att_missing") {
		t.Fatal(err)
	}
}

func TestRefsRefuseFilesOverTheCallCap(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	a := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "a", MimeType: "image/jpeg", Bytes: JPEG}, NOW).Row
	b := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "b", MimeType: "image/jpeg", Bytes: append(append([]byte{}, JPEG...), 'x')}, NOW).Row
	args := map[string]any{"files": []any{attachments.Ref(a.ID), attachments.Ref(b.ID)}}
	if _, err := attachments.ResolveRefs(d, args, attachments.ResolveOptions{ChatID: chatID, MasterKey: KEY, MaxBytes: int64(len(JPEG) * 2)}); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatal(err)
	}
	_, files, err := attachments.ResolveFileRefs(d, args, attachments.ResolveOptions{ChatID: chatID, MasterKey: KEY, MaxBytes: int64(len(JPEG)*2 + 1)})
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
}

func TestRefsScrubEchoedFilesAndLabelForApprovals(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	row := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "Struk laundry", MimeType: "image/jpeg", Bytes: JPEG}, NOW).Row
	ref := attachments.Ref(row.ID)
	_, files, err := attachments.ResolveFileRefs(d, map[string]any{"file": ref}, attachments.ResolveOptions{ChatID: chatID, MasterKey: KEY})
	testutil.Must(t, err)
	result := parse(t, `{"content":[{"type":"text","text":"bad file: `+files[0].URL+`"}],"n":1}`)
	if got := jsonx.Stringify(attachments.ScrubFileURLs(result, files)); got != `{"content":[{"type":"text","text":"bad file: `+ref+`"}],"n":1}` {
		t.Fatal(got)
	}
	labeled, err := attachments.LabelFileRefs(d, parse(t, `{"file":"`+ref+`","transactionId":"txn_1"}`))
	testutil.Must(t, err)
	if got := jsonx.Stringify(labeled); got != `{"file":"[file: Struk laundry, 10 B]","transactionId":"txn_1"}` {
		t.Fatal(got)
	}
}

// --- shares ----------------------------------------------------------------------------

func withFile(t *testing.T) (*db.DB, string) {
	d, chatID, _ := testutil.Setup(t)
	return d, save(t, d, attachments.SaveInput{ChatID: chatID, Title: "KTP", MimeType: "application/pdf", Bytes: PDF}, NOW).Row.ID
}

func share(t *testing.T, d db.Q, id, vis, dur, by string, now int64) attachments.CreatedShare {
	t.Helper()
	s, ok, err := attachments.CreateShare(d, attachments.ShareInput{AttachmentID: id, Visibility: vis, Duration: dur, CreatedBy: by}, now)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	return s
}

func resolve(t *testing.T, d db.Q, token string, now int64) attachments.Resolved {
	t.Helper()
	r, err := attachments.ResolveShare(d, token, now)
	testutil.Must(t, err)
	return r
}

func TestShareResolvesUntilItExpiresStoringOnlyTheHash(t *testing.T) {
	d, id := withFile(t)
	made := share(t, d, id, "public", "1h", "owner", NOW)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`).MatchString(made.Token) || strings.Contains(jsonx.Stringify(made.Share), "tokenHash") || made.Share.ExpiresAt != NOW+HOUR {
		t.Fatalf("%+v", made)
	}
	var hash string
	testutil.Must(t, d.QueryRow("SELECT token_hash FROM attachment_shares").Scan(&hash))
	if strings.Contains(hash, made.Token) {
		t.Fatal("token stored")
	}
	if attachments.ShareURL("https://palui.test", made.Token) != "https://palui.test/api/share/"+made.Token {
		t.Fatal("url")
	}
	if r := resolve(t, d, made.Token, NOW+HOUR-1); !r.OK || r.Attachment.ID != id || r.Attachment.Title != "KTP" || r.Share.Visibility != "public" {
		t.Fatalf("%+v", r)
	}
	if r := resolve(t, d, made.Token, NOW+HOUR); r.OK || r.Reason != "expired" {
		t.Fatalf("%+v", r)
	}
	if r := resolve(t, d, "nope", NOW); r.Reason != "not_found" {
		t.Fatalf("%+v", r)
	}
	if r := resolve(t, d, strings.Repeat("x", 32), NOW); r.Reason != "not_found" {
		t.Fatalf("%+v", r)
	}
}

func TestShareRefusesAMissingAttachment(t *testing.T) {
	d, _, _ := testutil.Setup(t)
	if _, ok, err := attachments.CreateShare(d, attachments.ShareInput{AttachmentID: "att_nope", Visibility: "private", Duration: "24h", CreatedBy: "owner"}, NOW); ok || err != nil {
		t.Fatal(ok, err)
	}
}

func TestShareCountsOpensListsLiveLinksAndRevokes(t *testing.T) {
	d, id := withFile(t)
	a := share(t, d, id, "public", "24h", "owner", NOW)
	b := share(t, d, id, "private", "1h", "chat_1", NOW+1)
	testutil.Must(t, attachments.RecordOpen(d, a.Share.ID, NOW+5))
	testutil.Must(t, attachments.RecordOpen(d, a.Share.ID, NOW+6))
	live, _ := attachments.ListActiveShares(d, id, NOW+10)
	if len(live) != 2 || live[0].ID != b.Share.ID || live[1].ID != a.Share.ID || live[1].OpenCount != 2 || *live[1].LastOpenedAt != NOW+6 {
		t.Fatalf("%+v", live)
	}
	if later, _ := attachments.ListActiveShares(d, id, NOW+2*HOUR); len(later) != 1 || later[0].ID != a.Share.ID {
		t.Fatalf("%+v", later)
	}
	if counts, _ := attachments.ActiveShareCounts(d, NOW+10); counts[id] != 2 {
		t.Fatal(counts)
	}
	if ok, _ := attachments.RevokeShare(d, a.Share.ID, NOW+20); !ok {
		t.Fatal("revoke")
	}
	if ok, _ := attachments.RevokeShare(d, a.Share.ID, NOW+21); ok {
		t.Fatal("revoked twice")
	}
	if r := resolve(t, d, a.Token, NOW+30); r.Reason != "revoked" {
		t.Fatalf("%+v", r)
	}
	if n, _ := attachments.RevokeShares(d, id, NOW+30); n != 1 {
		t.Fatal(n)
	}
	if counts, _ := attachments.ActiveShareCounts(d, NOW+40); counts[id] != 0 {
		t.Fatal(counts)
	}
}

func TestShareLinksDropWhenTheFileIsDeleted(t *testing.T) {
	d, id := withFile(t)
	made := share(t, d, id, "public", "7d", "owner", NOW)
	if ok, _ := attachments.Delete(d, id); !ok {
		t.Fatal("delete")
	}
	if r := resolve(t, d, made.Token, NOW); r.Reason != "not_found" {
		t.Fatalf("%+v", r)
	}
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM attachment_shares").Scan(&n))
	if n != 0 {
		t.Fatal(n)
	}
}

func TestPreviewKind(t *testing.T) {
	cases := map[string]string{"image/png": "image", "application/pdf": "pdf", "text/csv": "text", "image/heic": "", "application/msword": ""}
	for mime, want := range cases {
		if got := attachments.PreviewKind(mime); got != want {
			t.Errorf("%s: %q", mime, got)
		}
	}
}

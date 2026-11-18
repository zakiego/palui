package attachments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	attconn "github.com/zakiego/palui/apps/server/internal/connectors/attachments"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outmedia"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

var jpegBytes = append([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, make([]byte, 32)...)

type fetchLog struct{ urls []string }

func (f *fetchLog) fetch(body []byte, contentType string) outmedia.Fetcher {
	return func(_ context.Context, url string, _ safenet.Options) (*safenet.Response, error) {
		f.urls = append(f.urls, url)
		return &safenet.Response{URL: url, Status: 200, OK: true, ContentType: contentType, Bytes: body}, nil
	}
}

func sendTools(e env, turn connectors.TurnInfo, fetch outmedia.Fetcher) *connectors.ToolSet {
	o := opts()
	o.Fetch = fetch
	return attconn.Tools(e.db, turn, o)
}

func outboxRows(t *testing.T, d *db.DB) []db.Outbox {
	rows, err := d.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY created_at, id")
	list, err := db.All(rows, err, db.ScanOutbox)
	testutil.Must(t, err)
	return list
}

func TestSendMediaFromAURL(t *testing.T) {
	e := setup(t)
	var f fetchLog
	set := sendTools(e, e.turn, f.fetch(jpegBytes, "image/jpeg"))
	r := conntest.Call(t, set, "send_media", `{"kind":"image","url":"https://example.com/img/inhaler-spacer.jpg","caption":"Ini inhaler dengan spacer"}`)
	if !r.B("ok") || r.S("kind") != db.MediaImage || r.S("filename") != "inhaler-spacer.jpg" || r.Has("note") {
		t.Fatal(r)
	}
	rows := outboxRows(t, e.db)
	if len(rows) != 1 {
		t.Fatal(len(rows))
	}
	o := rows[0]
	m := o.Payload.Media
	if o.ChatID != e.chatID || o.Channel != "whatsapp" || o.Source != "media:run_2" || o.Status != "pending" || o.Payload.Text != "Ini inhaler dengan spacer" || m == nil || m.Kind != db.MediaImage || m.MimeType != "image/jpeg" || o.ExpiresAt == nil || *o.ExpiresAt != NOW+attconn.SendTTLMs {
		t.Fatalf("%+v %+v", o, m)
	}
	got, err := outmedia.Load(e.db, m.ID, KEY)
	testutil.Must(t, err)
	if !bytes.Equal(got, jpegBytes) {
		t.Fatal("stored bytes")
	}
	var source, ref string
	testutil.Must(t, e.db.QueryRow("SELECT source, source_ref FROM outbox_media WHERE id = ?", m.ID).Scan(&source, &ref))
	if source != "url" || ref != "https://example.com/img/inhaler-spacer.jpg" || r.S("queued") != o.ID {
		t.Fatal(source, ref)
	}
}

func TestSendMediaFromASavedAttachment(t *testing.T) {
	e := setup(t)
	saved, err := attachments.Save(e.db, attachments.SaveInput{ChatID: e.chatID, Title: "Jadwal imunisasi", MimeType: "application/pdf", Bytes: []byte("%PDF-1.7 jadwal")}, KEY, NOW)
	testutil.Must(t, err)
	set := sendTools(e, e.turn, nil)
	r := conntest.Call(t, set, "send_media", `{"kind":"file","attachment_id":"`+saved.Row.ID+`"}`)
	if !r.B("ok") || r.S("kind") != db.MediaDocument || r.S("filename") != "Jadwal imunisasi.pdf" {
		t.Fatal(r)
	}
	// Asked as an image, a PDF still goes, as a document, and the model is told.
	r = conntest.Call(t, set, "send_media", `{"kind":"image","attachment_id":"`+saved.Row.ID+`","filename":"jadwal.pdf"}`)
	if !r.B("ok") || r.S("kind") != db.MediaDocument || r.S("filename") != "jadwal.pdf" || !strings.Contains(r.S("note"), "sent as a document") {
		t.Fatal(r)
	}
	// Another chat's file is out of reach.
	other := otherTools(e)
	if r := conntest.Call(t, other, "send_media", `{"kind":"file","attachment_id":"`+saved.Row.ID+`"}`); r.B("ok") || !strings.Contains(r.S("error"), "No saved attachment") {
		t.Fatal(r)
	}
	if n := len(outboxRows(t, e.db)); n != 2 {
		t.Fatal(n)
	}
}

func TestSendMediaRefusals(t *testing.T) {
	e := setup(t)
	var f fetchLog
	page := sendTools(e, e.turn, f.fetch([]byte("<!doctype html><html></html>"), "text/html"))
	cases := []struct{ input, want string }{
		{`{"kind":"image"}`, "exactly one of url or attachment_id"},
		{`{"kind":"image","url":"https://a.test/x.jpg","attachment_id":"att_1"}`, "exactly one of url or attachment_id"},
		{`{"kind":"image","url":"ftp://a.test/x.jpg"}`, "http(s)"},
		{`{"kind":"image","url":"https://a.test/page"}`, "web page"},
	}
	for _, c := range cases {
		if r := conntest.Call(t, page, "send_media", c.input); r.B("ok") || !strings.Contains(r.S("error"), c.want) {
			t.Errorf("%s: %s", c.input, r)
		}
	}
	// The real SSRF guard, no fetcher injected.
	guarded := sendTools(e, e.turn, nil)
	if r := conntest.Call(t, guarded, "send_media", `{"kind":"image","url":"http://127.0.0.1:3000/x.jpg"}`); r.B("ok") || !strings.Contains(r.S("error"), "SSRF") {
		t.Fatal(r)
	}
	noChat := sendTools(e, connectors.TurnInfo{RunID: "run_x"}, f.fetch(jpegBytes, "image/jpeg"))
	if r := conntest.Call(t, noChat, "send_media", `{"kind":"image","url":"https://a.test/x.jpg"}`); r.B("ok") || !strings.Contains(r.S("error"), "no chat") {
		t.Fatal(r)
	}
	if n := len(outboxRows(t, e.db)); n != 0 {
		t.Fatal(n)
	}
}

func TestSendMediaIsAChatScopedWriteInEveryAudience(t *testing.T) {
	e := setup(t)
	for _, mode := range []string{"", "owner", "members", "outside"} {
		turn := e.turn
		turn.Mode = mode
		set := attconn.Tools(e.db, turn, opts())
		pt, ok := set.Get("send_media")
		if !ok || pt.Class != "write" || pt.Data != connectors.DataChat || pt.Untrusted || pt.NeedsApproval != nil || pt.Preview == nil {
			t.Fatalf("%s: %+v", mode, pt)
		}
	}
	pt, _ := e.tools.Get("send_media")
	got := pt.Preview(json.RawMessage(`{"kind":"image","url":"https://a.test/x.jpg","caption":"Ini"}`))
	if got != "Kirim image: https://a.test/x.jpg\nIni" {
		t.Fatalf("%q", got)
	}
}

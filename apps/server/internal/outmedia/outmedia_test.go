package outmedia_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/outmedia"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

var (
	jpeg     = append([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, make([]byte, 64)...)
	pdf      = []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")
	gif      = []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	html     = []byte("<!doctype html><html><body>hi</body></html>")
	oggOpus  = append([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x13OpusHead\x01\x02"), make([]byte, 32)...)
	oggVorb  = append([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x1e\x01vorbis"), make([]byte, 32)...)
	mp3      = append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 32)...)
	mp4      = append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), make([]byte, 32)...)
	zipBytes = append([]byte{0x50, 0x4b, 0x03, 0x04}, make([]byte, 40)...)
)

func pngBytes(t *testing.T) []byte {
	var b bytes.Buffer
	testutil.Must(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	return b.Bytes()
}

func webp(t *testing.T) []byte {
	b, err := os.ReadFile("../sticker/testdata/still-lossy-alpha.webp")
	testutil.Must(t, err)
	return b
}

func TestSniffTrustsTheBytes(t *testing.T) {
	cases := []struct {
		declared, name string
		b              []byte
		want           string
	}{
		{"image/png", "x.png", jpeg, "image/jpeg"},
		{"", "", pdf, "application/pdf"},
		{"application/octet-stream", "a.ogg", oggOpus, "audio/ogg"},
		{"", "", mp3, "audio/mpeg"},
		{"", "", mp4, "video/mp4"},
		{"audio/mp4", "", mp4, "audio/mp4"},
		{"", "voice.m4a", mp4, "audio/mp4"},
		{"application/zip", "report.docx", zipBytes, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"", "a.zip", zipBytes, "application/zip"},
		{"text/csv", "", []byte("a,b\n1,2\n"), "text/csv"},
		{"text/html", "", html, ""},
		{"application/x-msdownload", "", []byte("MZ\x90\x00\x03\x00\x00\x00"), ""},
	}
	for _, c := range cases {
		if got := outmedia.Sniff(c.declared, c.name, c.b); got != c.want {
			t.Errorf("Sniff(%q, %q) = %q, want %q", c.declared, c.name, got, c.want)
		}
	}
}

func TestPrepareKindsAndFallbacks(t *testing.T) {
	cases := []struct {
		want     string
		b        []byte
		name     string
		kind     string
		fallback string
		mime     string
	}{
		{outmedia.WantImage, jpeg, "inhaler", db.MediaImage, "", "image/jpeg"},
		{outmedia.WantImage, webp(t), "", db.MediaImage, "", "image/png"},
		{outmedia.WantImage, gif, "", db.MediaDocument, db.MediaImage, "image/gif"},
		{outmedia.WantImage, pdf, "", db.MediaDocument, db.MediaImage, "application/pdf"},
		{outmedia.WantVoice, oggOpus, "", db.MediaVoice, "", "audio/ogg"},
		{outmedia.WantVoice, oggVorb, "", db.MediaAudio, db.MediaVoice, "audio/ogg"},
		{outmedia.WantVoice, mp3, "", db.MediaAudio, db.MediaVoice, "audio/mpeg"},
		{outmedia.WantVoice, pdf, "", db.MediaDocument, db.MediaVoice, "application/pdf"},
		{outmedia.WantVideo, mp4, "", db.MediaVideo, "", "video/mp4"},
		{outmedia.WantVideo, jpeg, "", db.MediaDocument, db.MediaVideo, "image/jpeg"},
		{outmedia.WantSticker, webp(t), "", db.MediaSticker, "", "image/webp"},
		{outmedia.WantSticker, jpeg, "", db.MediaImage, db.MediaSticker, "image/jpeg"},
		{outmedia.WantFile, jpeg, "", db.MediaDocument, "", "image/jpeg"},
	}
	for _, c := range cases {
		p, err := outmedia.Prepare(c.want, "", c.name, c.b)
		testutil.Must(t, err)
		if p.Kind != c.kind || p.Fallback != c.fallback || p.MimeType != c.mime {
			t.Errorf("Prepare(%s, %s) = kind %s fallback %q mime %s", c.want, c.mime, p.Kind, p.Fallback, p.MimeType)
		}
	}
	p, _ := outmedia.Prepare(outmedia.WantImage, "", "inhaler", jpeg)
	if p.Filename != "inhaler.jpg" {
		t.Errorf("filename %q", p.Filename)
	}
	if p, _ := outmedia.Prepare(outmedia.WantImage, "", "", webp(t)); p.Filename != "file.png" {
		t.Errorf("converted filename %q", p.Filename)
	}
}

func TestPrepareRefuses(t *testing.T) {
	_, err := outmedia.Prepare(outmedia.WantImage, "text/html", "", html)
	if !outmedia.IsError(err) || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("html: %v", err)
	}
	if _, err := outmedia.Prepare(outmedia.WantFile, "", "", nil); !outmedia.IsError(err) {
		t.Fatalf("empty: %v", err)
	}
	big := append(append([]byte{}, jpeg...), make([]byte, outmedia.MaxBytes)...)
	if _, err := outmedia.Prepare(outmedia.WantFile, "", "", big); !outmedia.IsError(err) || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("big: %v", err)
	}
	// An image over the image cap still goes out, as a document.
	large := append(append([]byte{}, jpeg...), make([]byte, outmedia.MaxImageBytes)...)
	p, err := outmedia.Prepare(outmedia.WantImage, "", "", large)
	testutil.Must(t, err)
	if p.Kind != db.MediaDocument {
		t.Fatalf("large image kind %s", p.Kind)
	}
	if _, err := outmedia.Prepare(outmedia.WantImage, "", "", pngBytes(t)); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLoadAndSweep(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	key := config.DevMasterKey
	p, err := outmedia.Prepare(outmedia.WantImage, "", "inhaler.jpg", jpeg)
	testutil.Must(t, err)
	const now = int64(1_700_000_000_000)
	ref, err := outmedia.Store(d, outmedia.StoreInput{ChatID: chatID, Media: p, Source: "url", SourceRef: "https://example.com/inhaler.jpg"}, key, now)
	testutil.Must(t, err)
	if ref.Kind != db.MediaImage || ref.Size != int64(len(jpeg)) || ref.Filename != "inhaler.jpg" || !strings.HasPrefix(ref.ID, "med_") {
		t.Fatalf("%+v", ref)
	}
	var stored []byte
	testutil.Must(t, d.QueryRow("SELECT data FROM outbox_media WHERE id = ?", ref.ID).Scan(&stored))
	if bytes.Contains(stored, jpeg[:10]) {
		t.Fatal("stored in the clear")
	}
	got, err := outmedia.Load(d, ref.ID, key)
	testutil.Must(t, err)
	if !bytes.Equal(got, jpeg) {
		t.Fatal("round trip")
	}
	row, err := outbox.Enqueue(d, outbox.EnqueueInput{ChatID: chatID, Channel: "telegram", Payload: db.OutboxPayload{Text: "caption", Media: &ref}, Source: "media:run_1"}, now)
	testutil.Must(t, err)
	if row.Payload.Media == nil || row.Payload.Media.ID != ref.ID {
		t.Fatalf("payload %+v", row.Payload)
	}
	later := now + outmedia.SweepGraceMs + 1
	if n, _ := outmedia.Sweep(d, later); n != 0 {
		t.Fatal("swept media a pending row still needs")
	}
	_, err = d.Exec("UPDATE outbox SET status = 'sent' WHERE id = ?", row.ID)
	testutil.Must(t, err)
	if n, _ := outmedia.Sweep(d, now+1); n != 0 {
		t.Fatal("swept inside the grace period")
	}
	if n, _ := outmedia.Sweep(d, later); n != 1 {
		t.Fatal("kept media nothing can send")
	}
	if _, err := outmedia.Load(d, ref.ID, key); !errors.Is(err, outmedia.ErrGone) {
		t.Fatalf("load after sweep: %v", err)
	}
}

func TestFetch(t *testing.T) {
	var opts safenet.Options
	fetch := func(_ context.Context, url string, o safenet.Options) (*safenet.Response, error) {
		opts = o
		return &safenet.Response{URL: "https://cdn.example.com/img/inhaler%20spacer.jpg?x=1", Status: 200, OK: true, ContentType: "image/jpeg", Bytes: jpeg}, nil
	}
	f, err := outmedia.Fetch(context.Background(), "https://example.com/a", fetch)
	testutil.Must(t, err)
	if f.Filename != "inhaler spacer.jpg" || f.ContentType != "image/jpeg" || opts.MaxBytes != outmedia.MaxBytes || opts.Truncate {
		t.Fatalf("%+v %+v", f, opts)
	}
	blocked := func(context.Context, string, safenet.Options) (*safenet.Response, error) {
		return nil, &safenet.Error{Code: "blocked_address", Message: "127.0.0.1 is a loopback address"}
	}
	if _, err := outmedia.Fetch(context.Background(), "http://127.0.0.1/", blocked); !outmedia.IsError(err) || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("blocked: %v", err)
	}
	notFound := func(context.Context, string, safenet.Options) (*safenet.Response, error) {
		return &safenet.Response{Status: 404}, nil
	}
	if _, err := outmedia.Fetch(context.Background(), "https://example.com/x", notFound); !outmedia.IsError(err) || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404: %v", err)
	}
	// The real guard refuses a private address before any request.
	if _, err := outmedia.Fetch(context.Background(), "http://10.0.0.1/a.jpg", nil); !outmedia.IsError(err) {
		t.Fatalf("private: %v", err)
	}
}

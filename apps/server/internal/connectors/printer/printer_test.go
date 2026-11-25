package printer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	NOW = int64(1794276000000)
	KEY = "test-master-key-0123456789abcdef0123456789"
)

var (
	pdfBytes = []byte("%PDF-1.7 tiket")
	PDF      = base64.StdEncoding.EncodeToString(pdfBytes)
	JPG      = base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10})
)

// fakePrinter answers IPP like a CUPS queue and records what it was sent.
type fakePrinter struct {
	mu        sync.Mutex
	formats   []string
	accepting bool
	status    uint16
	reasons   []string
	got       []Message
	hosts     []string
}

func (f *fakePrinter) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	req, err := Decode(raw)
	if err != nil || r.Header.Get("Content-Type") != "application/ipp" {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.got = append(f.got, req)
	f.hosts = append(f.hosts, r.Host)
	f.mu.Unlock()
	res := Message{Major: 1, Minor: 1, Code: f.status, RequestID: req.RequestID}
	res.Groups = []Group{{Tag: tagOperation, Attrs: []Attr{
		{Name: "attributes-charset", Tag: tagCharset, Values: []any{"utf-8"}},
		{Name: "attributes-natural-language", Tag: tagNaturalLanguage, Values: []any{"en"}},
	}}}
	if f.status >= 0x0100 {
		res.Groups[0].Attrs = append(res.Groups[0].Attrs, Attr{Name: "status-message", Tag: tagText, Values: []any{"nope"}})
	} else {
		switch req.Code {
		case opGetPrinterAttributes:
			reasons := []any{"none"}
			if len(f.reasons) > 0 {
				reasons = nil
				for _, r := range f.reasons {
					reasons = append(reasons, r)
				}
			}
			formats := make([]any, len(f.formats))
			for i, s := range f.formats {
				formats[i] = s
			}
			res.Groups = append(res.Groups, Group{Tag: tagPrinter, Attrs: []Attr{
				{Name: "printer-name", Tag: tagName, Values: []any{"Test"}},
				{Name: "printer-make-and-model", Tag: tagText, Values: []any{"Ink Tank 100"}},
				{Name: "printer-state", Tag: tagEnum, Values: []any{int32(3)}},
				{Name: "printer-state-reasons", Tag: tagKeyword, Values: reasons},
				{Name: "printer-is-accepting-jobs", Tag: tagBoolean, Values: []any{f.accepting}},
				{Name: "document-format-supported", Tag: tagMimeMediaType, Values: formats},
				{Name: "marker-names", Tag: tagName, Values: []any{"Black", "Cyan"}},
				{Name: "marker-levels", Tag: tagInteger, Values: []any{int32(70), int32(-2)}},
			}})
		case opGetJobs:
			res.Groups = append(res.Groups,
				Group{Tag: tagJob, Attrs: []Attr{{Name: "job-id", Tag: tagInteger, Values: []any{int32(5)}}, {Name: "job-name", Tag: tagName, Values: []any{"a.pdf"}}, {Name: "job-state", Tag: tagEnum, Values: []any{int32(3)}}}},
				Group{Tag: tagJob, Attrs: []Attr{{Name: "job-id", Tag: tagInteger, Values: []any{int32(6)}}, {Name: "job-name", Tag: tagName, Values: []any{"b.jpg"}}, {Name: "job-state", Tag: tagEnum, Values: []any{int32(5)}}}},
			)
		case opPrintJob:
			res.Groups = append(res.Groups, Group{Tag: tagJob, Attrs: []Attr{
				{Name: "job-id", Tag: tagInteger, Values: []any{int32(42)}},
				{Name: "job-state", Tag: tagEnum, Values: []any{int32(3)}},
			}})
		}
	}
	b, _ := res.Encode()
	w.Header().Set("Content-Type", "application/ipp")
	_, _ = w.Write(b)
}

func (f *fakePrinter) last(op uint16) (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.got) - 1; i >= 0; i-- {
		if f.got[i].Code == op {
			return f.got[i], true
		}
	}
	return Message{}, false
}

func cupsQueue() *fakePrinter {
	return &fakePrinter{formats: []string{"application/octet-stream", "application/pdf", "image/jpeg", "image/png"}, accepting: true}
}

func serve(t *testing.T, f *fakePrinter) string {
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return "ipp://" + strings.TrimPrefix(srv.URL, "http://") + "/printers/Test"
}

type env struct {
	db     *db.DB
	chatID string
	other  string
	turn   connectors.TurnInfo
}

func setup(t *testing.T) env {
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "62800000000@s.whatsapp.net"}, NOW)
	testutil.Must(t, err)
	other, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, NOW)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_2", BatchRunID: "run_1", ChatID: chat.ID, Channel: "whatsapp", Trigger: "message", Now: NOW}
	return env{d, chat.ID, other.ID, turn}
}

type media map[string]any

func inbound(t *testing.T, d *db.DB, chatID, runID string, list []media, at int64, text string) {
	t.Helper()
	items := make([]any, len(list))
	for i, m := range list {
		o := jsonx.NewMap[any]()
		for _, k := range []string{"kind", "mimeType", "filename", "data"} {
			if v, ok := m[k]; ok {
				o.Set(k, v)
			}
		}
		items[i] = o
	}
	b, err := jsonx.Marshal(kit.O("text", text, "media", items))
	testutil.Must(t, err)
	_, err = d.Exec("INSERT INTO messages (id, chat_id, role, content, text, run_id, status, from_owner, created_at) VALUES (?, ?, 'user', ?, '', ?, 'done', 0, ?)", secret.NewID("msg"), chatID, string(b), runID, at)
	testutil.Must(t, err)
}

func tools(e env, url string) *connectors.ToolSet {
	return Tools(e.db, e.turn, Config{URL: url}, Options{Key: func() string { return KEY }})
}

func attr(t *testing.T, m Message, group byte, name string) Attr {
	t.Helper()
	a, ok := m.Get(group, name)
	if !ok {
		t.Fatalf("no %s in the request", name)
	}
	return a
}

func TestEncodesAndDecodesEveryValueKindAndSkipsCollections(t *testing.T) {
	m := Message{Major: 1, Minor: 1, Code: opPrintJob, RequestID: 7, Groups: []Group{
		{Tag: tagOperation, Attrs: []Attr{{Name: "attributes-charset", Tag: tagCharset, Values: []any{"utf-8"}}}},
		{Tag: tagJob, Attrs: []Attr{
			{Name: "copies", Tag: tagInteger, Values: []any{int32(3)}},
			{Name: "page-ranges", Tag: tagRange, Values: []any{[2]int32{1, 3}, [2]int32{5, 5}}},
			{Name: "fit", Tag: tagBoolean, Values: []any{true}},
			{Name: "sides", Tag: tagKeyword, Values: []any{"one-sided", "two-sided-long-edge"}},
		}},
	}, Data: pdfBytes}
	raw, err := m.Encode()
	testutil.Must(t, err)
	got, err := Decode(raw)
	testutil.Must(t, err)
	if got.Code != opPrintJob || got.RequestID != 7 || !bytes.Equal(got.Data, pdfBytes) {
		t.Fatalf("header or data: %+v", got)
	}
	if n, _ := attr(t, got, tagJob, "copies").Int(); n != 3 {
		t.Errorf("copies %d", n)
	}
	if r := attr(t, got, tagJob, "page-ranges").Values; len(r) != 2 || r[1] != [2]int32{5, 5} {
		t.Errorf("page-ranges %v", r)
	}
	if b, _ := attr(t, got, tagJob, "fit").Bool(); !b {
		t.Error("fit")
	}
	if s := attr(t, got, tagJob, "sides").Strs(); len(s) != 2 || s[1] != "two-sided-long-edge" {
		t.Errorf("sides %v", s)
	}

	// A collection's members don't leak into the group, and the attribute after it parses.
	var b bytes.Buffer
	b.Write([]byte{2, 0, 0, 0, 0, 0, 0, 1, tagPrinter})
	chunk := func(tag byte, name, val string) {
		b.WriteByte(tag)
		b.Write([]byte{byte(len(name) >> 8), byte(len(name))})
		b.WriteString(name)
		b.Write([]byte{byte(len(val) >> 8), byte(len(val))})
		b.WriteString(val)
	}
	chunk(tagBegCollection, "media-col-default", "")
	chunk(tagMemberAttrName, "", "media-size")
	chunk(tagBegCollection, "", "")
	chunk(tagEndCollection, "", "")
	chunk(tagEndCollection, "", "")
	chunk(tagTextLang, "printer-info", "\x00\x02en\x00\x05Hallo")
	b.WriteByte(tagEnd)
	got, err = Decode(b.Bytes())
	testutil.Must(t, err)
	if len(got.Groups[0].Attrs) != 2 || attr(t, got, tagPrinter, "printer-info").Str() != "Hallo" {
		t.Errorf("collection handling: %+v", got.Groups)
	}
	if _, err := Decode([]byte{1, 1, 0}); err == nil {
		t.Error("a short message decoded")
	}
}

func TestNamesLocalhostToCUPSWhenReachedThroughHostDockerInternal(t *testing.T) {
	c, err := NewClient("ipp://host.docker.internal/printers/Office", nil)
	testutil.Must(t, err)
	if c.endpoint != "http://host.docker.internal:631/printers/Office" || c.host != "localhost:631" || c.printerURI != "ipp://localhost:631/printers/Office" {
		t.Fatalf("got %+v", c)
	}
	var seen string
	c.hc = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		seen = r.Host
		return nil, io.EOF
	})}
	_, _ = c.Printer(context.Background())
	if seen != "localhost:631" {
		t.Errorf("Host header %q", seen)
	}

	c, err = NewClient("ipps://printer.lan:8631/ipp/print", nil)
	testutil.Must(t, err)
	if c.endpoint != "https://printer.lan:8631/ipp/print" || c.host != "" || c.printerURI != "ipps://printer.lan:8631/ipp/print" {
		t.Errorf("ipps: %+v", c)
	}
	if _, err := NewClient("lpd://printer/queue", nil); err == nil || !strings.Contains(err.Error(), "ipp://") {
		t.Errorf("lpd: %v", err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTestReportsTheQueueOrSaysWhatIsWrong(t *testing.T) {
	ctx := context.Background()
	f := cupsQueue()
	res := Test(ctx, Config{URL: serve(t, f)}, nil)
	if !res.OK || res.Detail != "Connected to Ink Tank 100 (idle). Prints PDF, JPEG, PNG." {
		t.Errorf("cups: %+v", res)
	}

	raster := &fakePrinter{formats: []string{"application/octet-stream", "image/pwg-raster"}, accepting: true}
	res = Test(ctx, Config{URL: serve(t, raster)}, nil)
	if res.OK || !strings.Contains(res.Detail, "doesn't take PDF, JPEG or PNG (it lists application/octet-stream, image/pwg-raster)") || !strings.Contains(res.Detail, "CUPS queue") {
		t.Errorf("raster-only: %+v", res)
	}

	missing := cupsQueue()
	missing.status = statusNotFound
	res = Test(ctx, Config{URL: serve(t, missing)}, nil)
	if res.OK || !strings.Contains(res.Detail, "queue wasn't found") {
		t.Errorf("not found: %+v", res)
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	gone := "ipp://" + strings.TrimPrefix(srv.URL, "http://") + "/printers/Test"
	srv.Close()
	res = Test(ctx, Config{URL: gone}, nil)
	if res.OK || !strings.Contains(res.Detail, "Can't reach the printer at "+gone) {
		t.Errorf("unreachable: %+v", res)
	}

	rt := New(Options{})
	res, err := rt.Test(conntest.Ctx(t, testutil.DB(t), "printer", nil, nil))
	testutil.Must(t, err)
	if res.OK || !strings.Contains(res.Detail, "Set the printer URL first") {
		t.Errorf("no config: %+v", res)
	}
}

func TestExposesAReadStatusToolAndAWritePrintTool(t *testing.T) {
	e := setup(t)
	if got := strings.Join(conntest.Classes(tools(e, "ipp://x/printers/y")), ","); got != "printer_status:read,print_file:write" {
		t.Errorf("classes %s", got)
	}
}

func TestPrintsTheFileInTheCurrentMessageWithTheAskedOptions(t *testing.T) {
	e := setup(t)
	f := cupsQueue()
	url := serve(t, f)
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "document", "mimeType": "application/pdf", "filename": "tiket.pdf", "data": PDF}}, NOW, "print ya")
	got := conntest.Call(t, tools(e, url), "print_file", `{"copies":2,"color":"gray","paper":"A4","pages":"1-3,5","fit_to_page":true}`)
	if got.Get("ok") != true || got.Get("jobId") != float64(42) || got.Get("jobState") != "pending" || got.Get("message") != `Oke, "tiket.pdf" udah aku kirim ke printer. Cek kertasnya ya.` {
		t.Fatalf("got %s", jsonx.Stringify(got.V))
	}
	req, ok := f.last(opPrintJob)
	if !ok {
		t.Fatal("no Print-Job")
	}
	if !bytes.Equal(req.Data, pdfBytes) {
		t.Errorf("document %q", req.Data)
	}
	if s := attr(t, req, tagOperation, "document-format").Str(); s != "application/pdf" {
		t.Errorf("format %s", s)
	}
	if s := attr(t, req, tagOperation, "job-name").Str(); s != "tiket.pdf" {
		t.Errorf("job-name %s", s)
	}
	if s := attr(t, req, tagOperation, "requesting-user-name").Str(); s != "palui" {
		t.Errorf("user %s", s)
	}
	if n, _ := attr(t, req, tagJob, "copies").Int(); n != 2 {
		t.Errorf("copies %d", n)
	}
	if s := attr(t, req, tagJob, "media").Str(); s != "iso_a4_210x297mm" {
		t.Errorf("media %s", s)
	}
	if s := attr(t, req, tagJob, "print-color-mode").Str(); s != "monochrome" {
		t.Errorf("color %s", s)
	}
	if r := attr(t, req, tagJob, "page-ranges").Values; len(r) != 2 || r[0] != [2]int32{1, 3} || r[1] != [2]int32{5, 5} {
		t.Errorf("pages %v", r)
	}
	if s := attr(t, req, tagJob, "print-scaling").Str(); s != "fit" {
		t.Errorf("scaling %s", s)
	}
}

func TestLeavesThePrintersDefaultsAloneWhenNotAsked(t *testing.T) {
	e := setup(t)
	f := cupsQueue()
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW, "")
	got := conntest.Call(t, tools(e, serve(t, f)), "print_file", `{}`)
	if got.Get("ok") != true || got.Get("file") != "photo" {
		t.Fatalf("got %s", jsonx.Stringify(got.V))
	}
	req, _ := f.last(opPrintJob)
	for _, g := range req.Groups {
		if g.Tag == tagJob {
			t.Errorf("job attributes sent: %+v", g.Attrs)
		}
	}
}

func TestAsksWhichFileAndFindsEarlierAndSavedOnes(t *testing.T) {
	e := setup(t)
	f := cupsQueue()
	url := serve(t, f)
	set := tools(e, url)

	got := conntest.Call(t, set, "print_file", `{}`)
	if got.Get("ok") != false || !strings.Contains(got.Get("error").(string), "none was sent in this chat") {
		t.Errorf("nothing: %s", jsonx.Stringify(got.V))
	}

	inbound(t, e.db, e.chatID, "run_0", []media{{"kind": "document", "mimeType": "application/pdf", "filename": "lama.pdf", "data": PDF}}, NOW-60_000, "surat")
	got = conntest.Call(t, set, "print_file", `{}`)
	if got.Get("ok") != false || !strings.Contains(got.Get("error").(string), "earlier: true") || got.Get("files.0.caption") != "surat" {
		t.Errorf("hint earlier: %s", jsonx.Stringify(got.V))
	}
	got = conntest.Call(t, set, "print_file", `{"earlier":true}`)
	if got.Get("ok") != true || got.Get("file") != "lama.pdf" {
		t.Errorf("earlier: %s", jsonx.Stringify(got.V))
	}

	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}, {"kind": "document", "mimeType": "application/pdf", "filename": "b.pdf", "data": PDF}}, NOW, "")
	got = conntest.Call(t, set, "print_file", `{}`)
	if got.Get("ok") != false || !strings.Contains(got.Get("error").(string), "more than one file") || got.Get("files.1.filename") != "b.pdf" {
		t.Errorf("two files: %s", jsonx.Stringify(got.V))
	}
	got = conntest.Call(t, set, "print_file", `{"nomor":2}`)
	if got.Get("ok") != true || got.Get("file") != "b.pdf" {
		t.Errorf("nomor 2: %s", jsonx.Stringify(got.V))
	}
	got = conntest.Call(t, set, "print_file", `{"nomor":3}`)
	if got.Get("ok") != false || got.Get("error") != "There is no file number 3." {
		t.Errorf("nomor 3: %s", jsonx.Stringify(got.V))
	}

	saved, err := attachments.Save(e.db, attachments.SaveInput{ChatID: e.chatID, Title: "KTP", MimeType: "application/pdf", Filename: "ktp.pdf", Bytes: pdfBytes}, KEY, NOW)
	testutil.Must(t, err)
	got = conntest.Call(t, set, "print_file", `{"attachment_id":"`+saved.Row.ID+`"}`)
	if got.Get("ok") != true || got.Get("file") != "ktp.pdf" {
		t.Errorf("saved: %s", jsonx.Stringify(got.V))
	}
	elsewhere, err := attachments.Save(e.db, attachments.SaveInput{ChatID: e.other, Title: "Lain", MimeType: "application/pdf", Filename: "x.pdf", Bytes: []byte("%PDF-1.7 lain")}, KEY, NOW)
	testutil.Must(t, err)
	got = conntest.Call(t, set, "print_file", `{"attachment_id":"`+elsewhere.Row.ID+`"}`)
	if got.Get("ok") != false || !strings.Contains(got.Get("error").(string), "No saved attachment") {
		t.Errorf("other chat: %s", jsonx.Stringify(got.V))
	}
}

func TestRefusesWhatItCantPrint(t *testing.T) {
	e := setup(t)
	cases := []struct {
		name, input, want string
		media             media
		printer           *fakePrinter
	}{
		{"word file", `{}`, "Can't print a application/msword file. Only PDF, JPEG and PNG can be printed.", media{"kind": "document", "mimeType": "application/msword", "filename": "a.doc", "data": PDF}, cupsQueue()},
		{"bad pages", `{"pages":"3-1"}`, `pages "3-1" isn't a valid range. Use page numbers and ranges, e.g. 1-3,5`, media{"kind": "document", "mimeType": "application/pdf", "data": PDF}, cupsQueue()},
		{"pages out of order", `{"pages":"5,1-3"}`, "list pages in order without overlaps, e.g. 1-3,5", media{"kind": "document", "mimeType": "application/pdf", "data": PDF}, cupsQueue()},
		{"raster printer", `{}`, "This printer can't take PDF files directly. Ask the owner to point the printer connector at the Mac's CUPS queue.", media{"kind": "document", "mimeType": "application/pdf", "data": PDF}, &fakePrinter{formats: []string{"image/pwg-raster"}, accepting: true}},
		{"paused queue", `{}`, "The printer isn't accepting jobs right now. Ask the owner to check whether its queue is paused.", media{"kind": "document", "mimeType": "application/pdf", "data": PDF}, &fakePrinter{formats: []string{"application/pdf"}}},
	}
	for i, c := range cases {
		run := "run_c" + string(rune('a'+i))
		turn := e.turn
		turn.BatchRunID, turn.RunID = run, run
		inbound(t, e.db, e.chatID, run, []media{c.media}, NOW, "")
		set := Tools(e.db, turn, Config{URL: serve(t, c.printer)}, Options{Key: func() string { return KEY }})
		got := conntest.Call(t, set, "print_file", c.input)
		if got.Get("ok") != false || got.Get("error") != c.want {
			t.Errorf("%s: %s", c.name, jsonx.Stringify(got.V))
		}
		if _, sent := c.printer.last(opPrintJob); sent {
			t.Errorf("%s: a job was sent", c.name)
		}
	}
}

func TestStatusShowsStateInkAndWaitingJobs(t *testing.T) {
	e := setup(t)
	f := cupsQueue()
	f.reasons = []string{"media-empty-error"}
	got := conntest.Call(t, tools(e, serve(t, f)), "printer_status", `{}`)
	want := `{"ok":true,"printer":"Ink Tank 100","state":"idle","problems":["media-empty-error"],"acceptingJobs":true,"prints":["PDF","JPEG","PNG"],"ink":[{"name":"Black","percent":70}],"jobs":[{"id":5,"name":"a.pdf","state":"pending"},{"id":6,"name":"b.jpg","state":"processing"}]}`
	if s := jsonx.Stringify(got.V); s != want {
		t.Errorf("got  %s\nwant %s", s, want)
	}
}

func TestAsksToPrintOnlyWhenTheOwnerSaysPrint(t *testing.T) {
	for text, want := range map[string]bool{
		"print ini":                       true,
		"PRINT":                           true,
		"tolong ngeprint dong":            true,
		"nge-print yang tadi":             true,
		"Perlu ngerpint ini":              true,
		"diprint 2 rangkap ya":            true,
		"printin halaman 1":               true,
		"cetakin A4":                      true,
		"":                                false,
		"printernya nyala?":               false,
		"sprint review besok":             false,
		"blueprint rumah":                 false,
		"ini tanggal menginap hotelnya":   false,
		"cek tanggal check-in di sini ya": false,
	} {
		if got := AsksToPrint(text); got != want {
			t.Errorf("AsksToPrint(%q) = %v, want %v", text, got, want)
		}
	}
}

func ownerTurn(e env, text string) connectors.TurnInfo {
	turn := e.turn
	turn.Origin, turn.Mode = "message", "owner"
	if text != "" {
		turn.OwnerText = &text
	}
	return turn
}

func printTool(t *testing.T, d *db.DB, turn connectors.TurnInfo, url string) connectors.PaluiTool {
	t.Helper()
	pt, ok := Tools(d, turn, Config{URL: url}, Options{Key: func() string { return KEY }}).Get("print_file")
	if !ok || pt.NeedsApproval == nil || pt.Preview == nil {
		t.Fatal("print_file has no approval rule")
	}
	return pt
}

// Rpt rpt_0mwvtuo1aaci2gqrnq6: the owner once said "Perlu ngerpint ini" about
// a Drive link, Palui answered "kirim ke sini sebagai file, nanti langsung
// kuprint", and turns later a caption-less PDF (sent for something else) got
// printed with no question asked.
func TestAFileWithNoCaptionAfterAnEarlierPrintPromiseAsksFirst(t *testing.T) {
	e := setup(t)
	inbound(t, e.db, e.chatID, "run_0", nil, NOW-3600_000, "Perlu ngerpint ini https://drive.google.com/file/d/x")
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "document", "mimeType": "application/pdf", "filename": "91fb6954.pdf", "data": PDF}}, NOW, "")
	pt := printTool(t, e.db, ownerTurn(e, ""), "ipp://x/printers/y")
	if !pt.NeedsApproval(json.RawMessage(`{}`)) {
		t.Fatal("a caption-less file printed without asking")
	}
	if got := pt.Preview(json.RawMessage(`{"copies":2}`)); got != "Print 91fb6954.pdf?\nRangkap: 2" {
		t.Errorf("preview %q", got)
	}
	// Words that aren't a print request ask too.
	if !printTool(t, e.db, ownerTurn(e, "ini tanggal menginapnya"), "ipp://x/printers/y").NeedsApproval(nil) {
		t.Error("a caption without print didn't ask")
	}
}

func TestAFileCaptionedPrintPrintsRightAway(t *testing.T) {
	e := setup(t)
	f := cupsQueue()
	url := serve(t, f)
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "document", "mimeType": "application/pdf", "filename": "tiket.pdf", "data": PDF}}, NOW, "print ini")
	turn := ownerTurn(e, "print ini")
	if printTool(t, e.db, turn, url).NeedsApproval(json.RawMessage(`{}`)) {
		t.Fatal("an explicit print request asked first")
	}
	got := conntest.Call(t, Tools(e.db, turn, Config{URL: url}, Options{Key: func() string { return KEY }}), "print_file", `{}`)
	if got.Get("ok") != true {
		t.Fatalf("got %s", jsonx.Stringify(got.V))
	}
	if _, ok := f.last(opPrintJob); !ok {
		t.Fatal("no Print-Job")
	}
}

func TestAPrintRequestNamingAnEarlierFileRunsRightAway(t *testing.T) {
	e := setup(t)
	inbound(t, e.db, e.chatID, "run_0", []media{{"kind": "document", "mimeType": "application/pdf", "filename": "tiket.pdf", "data": PDF}}, NOW-60_000, "")
	if printTool(t, e.db, ownerTurn(e, "tolong print pdf yang tadi"), "ipp://x/printers/y").NeedsApproval(json.RawMessage(`{"earlier":true}`)) {
		t.Error("an explicit request for the earlier file asked first")
	}
}

func TestPrintingOutsideTheOwnersOwnMessageAsksFirst(t *testing.T) {
	e := setup(t)
	for _, turn := range []connectors.TurnInfo{
		e.turn, // no origin: a job or a flow
		func() connectors.TurnInfo { t := ownerTurn(e, "print ini"); t.Origin = "schedule"; return t }(),
		func() connectors.TurnInfo { t := ownerTurn(e, "print ini"); t.Mode = "members"; return t }(),
	} {
		if !printTool(t, e.db, turn, "ipp://x/printers/y").NeedsApproval(nil) {
			t.Errorf("turn %+v printed without asking", turn)
		}
	}
}

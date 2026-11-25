package mcp_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

var (
	png = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}
	pdf = []byte("%PDF-1.4 test")
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func texts(t *testing.T, out conntest.Value) []string {
	t.Helper()
	var got []string
	for i := 0; ; i++ {
		typ := out.S(fmt.Sprintf("content.%d.type", i))
		if typ == "" {
			return got
		}
		if typ == "text" {
			got = append(got, out.S(fmt.Sprintf("content.%d.text", i)))
		} else {
			got = append(got, "<"+typ+">")
		}
	}
}

// A chart, a PDF, a CSV and a zip come back from a tool, the way a code
// sandbox returns them. Every file but the zip becomes an attachment of the
// turn's chat, also in an outside turn; the model keeps the image and reads
// short lines in place of the bytes.
func TestSavesTheFilesInAToolResultAsAttachmentsOfTheTurnsChat(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: func(s *sdk.Server) {
		conntest.AddRawTool(s, "run_code", "Run code", "", nil, func(map[string]any) *sdk.CallToolResult {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: "done"},
				&sdk.ImageContent{Data: png, MIMEType: "image/png"},
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "sandbox://runs/r1/out/report.pdf", MIMEType: "application/pdf", Blob: pdf}},
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "sandbox://runs/r1/out/data.csv", Text: "a,b\n1,2\n"}},
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "sandbox://runs/r1/out/all.zip", MIMEType: "application/zip", Blob: []byte("PK\x03\x04zip")}},
			}}
		})
	}})
	f := ctx(t, config_(t, srv.URL, `,"authMode":"none"`), nil, "external")
	chat, err := chats.Ensure(f.DB, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120363@g.us"}, 0)
	testutil.Must(t, err)
	running, err := mcpconn.Runtime.Start(f.Context)
	testutil.Must(t, err)
	defer running.Stop(context.Background())
	tt := turn
	tt.ChatID, tt.Mode = chat.ID, "outside"
	set, err := running.Tools(context.Background(), tt)
	testutil.Must(t, err)

	out := exec(t, set, "run_code", `{}`)
	rows, err := attachments.List(f.DB, attachments.ListOptions{ChatID: chat.ID})
	testutil.Must(t, err)
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	ids := map[string]db.AttachmentMeta{}
	for _, r := range rows {
		ids[*r.Filename] = r
	}
	img, rep, csv := ids["image-1.png"], ids["report.pdf"], ids["data.csv"]
	if img.MimeType != "image/png" || rep.MimeType != "application/pdf" || csv.MimeType != "text/csv" || *img.RunID != "run_1" || img.Origin != attachments.OriginTool {
		t.Fatal(rows)
	}
	key, _ := config.MasterKey(config.Get())
	file, _, err := attachments.Read(f.DB, rep.ID, key)
	testutil.Must(t, err)
	if string(file.Bytes) != string(pdf) {
		t.Fatal(string(file.Bytes))
	}
	want := []string{
		"done",
		"<image>",
		"Saved file " + img.ID + ": image-1.png (image/png, 10 B).",
		"Saved file " + rep.ID + ": report.pdf (application/pdf, 13 B).",
		"Saved file " + csv.ID + ": data.csv (text/csv, 8 B).",
		"a,b\n1,2\n",
		"File not saved: all.zip (application/zip, 7 B): File type application/zip can't be saved. Allowed: images (JPEG, PNG, WebP, GIF, HEIC), PDF, plain text, CSV and Office documents",
		"Saved files belong to this chat. To send one here, call send_media with its attachment_id (kind image for a picture, file for anything else).",
	}
	if got := texts(t, out); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if out.S("content.1.data") != b64(png) {
		t.Fatal(out)
	}
	model := conntest.Of(mcpconn.ToModelOutput(out.V)).String()
	if strings.Contains(model, b64(pdf)) || !strings.Contains(model, `"type":"file","mediaType":"image/png"`) {
		t.Fatal(model)
	}

	// The same files again keep their ids.
	again := texts(t, exec(t, set, "run_code", `{}`))
	if again[2] != want[2] || again[3] != want[3] {
		t.Fatal(again)
	}
}

func save(t *testing.T, q db.Q, rf mcpconn.ResultFiles, content ...any) conntest.Value {
	t.Helper()
	out, err := mcpconn.SaveResultFiles(q, conntest.Of(map[string]any{"content": content}).V, rf, testutil.NOW)
	testutil.Must(t, err)
	return conntest.Of(out)
}

func rf(t *testing.T, q db.Q) mcpconn.ResultFiles {
	chat, err := chats.Ensure(q, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 0)
	testutil.Must(t, err)
	key, _ := config.MasterKey(config.Get())
	return mcpconn.ResultFiles{ChatID: chat.ID, RunID: "run_1", Tool: "Run code", MasterKey: key}
}

func TestResultFilesNamesFromTheMimeTypeWhenTheURIHasNoPath(t *testing.T) {
	d := testutil.DB(t)
	got := texts(t, save(t, d, rf(t, d),
		map[string]any{"type": "resource", "resource": map[string]any{"uri": "sandbox://", "blob": b64(pdf)}},
		map[string]any{"type": "resource", "resource": map[string]any{"uri": "sandbox://runs/r1/out/notes", "mimeType": "text/csv", "text": "x"}},
		map[string]any{"type": "resource", "resource": map[string]any{"uri": "sandbox://runs/r1/out/sheet.xlsx", "mimeType": "application/octet-stream", "blob": b64([]byte("PK\x03\x04x"))}},
	))
	if !strings.Contains(got[0], ": file-1.pdf (application/pdf") || !strings.Contains(got[1], ": notes (text/csv") || !strings.Contains(got[3], ": sheet.xlsx (application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
		t.Fatalf("%q", got)
	}
}

func TestResultFilesSaysWhatItSkipped(t *testing.T) {
	d := testutil.DB(t)
	r := rf(t, d)
	var many []any
	for i := range mcpconn.MaxResultFiles + 1 {
		many = append(many, map[string]any{"type": "resource", "resource": map[string]any{"uri": fmt.Sprintf("x:/f%d.txt", i), "text": fmt.Sprint(i)}})
	}
	got := texts(t, save(t, d, r, many...))
	if !strings.HasPrefix(got[2*mcpconn.MaxResultFiles], "File not saved: f10.txt (text/plain, 2 B): over the limit of 10 files") {
		t.Fatalf("%q", got)
	}

	big := strings.Repeat("A", int(attachments.MaxBytes)*4/3+8)
	got = texts(t, save(t, d, r,
		map[string]any{"type": "image", "data": big, "mimeType": "image/png"},
		map[string]any{"type": "audio", "data": b64([]byte("OggS")), "mimeType": "audio/ogg"},
		map[string]any{"type": "image", "data": "%%%", "mimeType": "image/png"},
	))
	if got[0] != "<image>" || !strings.HasPrefix(got[1], "File not saved: image-1.png (image/png, 10.0 MB): it is over the 10.0 MB limit") ||
		!strings.HasPrefix(got[2], "File not saved: audio-2.ogg (audio/ogg, 4 B): File type audio/ogg can't be saved") ||
		!strings.HasPrefix(got[4], "File not saved: image-3.png (image/png, 0 B): its data isn't valid base64") || len(got) != 5 {
		t.Fatalf("%q", got)
	}

	r.ChatID = ""
	got = texts(t, save(t, d, r, map[string]any{"type": "image", "data": b64(png), "mimeType": "image/png"}))
	if strings.Join(got, "|") != "<image>|File not saved: image-1.png (image/png, 10 B): this run has no chat to keep it in" {
		t.Fatalf("%q", got)
	}
}

func TestResultFilesCutsALongTextResourceForTheModel(t *testing.T) {
	d := testutil.DB(t)
	long := strings.Repeat("x", mcpconn.MaxResourceText+5)
	got := texts(t, save(t, d, rf(t, d), map[string]any{"type": "resource", "resource": map[string]any{"uri": "x:/big.txt", "text": long}}))
	if got[1] != strings.Repeat("x", mcpconn.MaxResourceText)+"\n[cut at 4000 characters; the whole file is in the attachment]" {
		t.Fatal(len(got[1]))
	}
}

func TestResultFilesLeavesOtherResultsAlone(t *testing.T) {
	d := testutil.DB(t)
	r := rf(t, d)
	for _, v := range []any{"plain", nil, jsonx.NewMap[any]()} {
		out, err := mcpconn.SaveResultFiles(d, v, r, testutil.NOW)
		testutil.Must(t, err)
		if conntest.Of(out).String() != conntest.Of(v).String() {
			t.Fatal(out)
		}
	}
	got := texts(t, save(t, d, r, map[string]any{"type": "text", "text": "hi"}, map[string]any{"type": "resource_link", "uri": "x:/a"}))
	if strings.Join(got, "|") != `hi|<resource_link>` {
		t.Fatal(got)
	}
	if n, _ := attachments.Count(d); n != 0 {
		t.Fatal(n)
	}
}

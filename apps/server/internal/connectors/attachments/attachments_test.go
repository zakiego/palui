package attachments_test

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	attconn "github.com/zakiego/palui/apps/server/internal/connectors/attachments"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	NOW = int64(1794276000000) // 2026-11-10T02:00:00Z
	KEY = "test-master-key-0123456789abcdef0123456789"
)

var (
	PDF = base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 tiket"))
	JPG = base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10})
)

type env struct {
	db              *db.DB
	chatID, otherID string
	turn            connectors.TurnInfo
	tools           *connectors.ToolSet
}

func opts() attconn.Options {
	return attconn.Options{Clock: func() int64 { return NOW }, Key: func() string { return KEY }, AppURL: func() string { return "https://palui.test" }}
}

func setup(t *testing.T) env {
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "628@s.whatsapp.net"}, NOW)
	testutil.Must(t, err)
	other, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, NOW)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_2", BatchRunID: "run_1", ChatID: chat.ID, Channel: "whatsapp", Trigger: "message", TZ: "Asia/Makassar", Now: NOW}
	return env{d, chat.ID, other.ID, turn, attconn.Tools(d, turn, opts())}
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
	content := kit.O("text", text, "media", items)
	b, err := jsonx.Marshal(content)
	testutil.Must(t, err)
	_, err = d.Exec("INSERT INTO messages (id, chat_id, role, content, text, run_id, status, from_owner, created_at) VALUES (?, ?, 'user', ?, '', ?, 'done', 0, ?)", secret.NewID("msg"), chatID, string(b), runID, at)
	testutil.Must(t, err)
}

func otherTools(e env) *connectors.ToolSet {
	return attconn.Tools(e.db, connectors.TurnInfo{RunID: "run_9", ChatID: e.otherID, Channel: "telegram", Trigger: "message", TZ: "Asia/Makassar", Now: NOW}, attconn.Options{Key: func() string { return KEY }, Clock: func() int64 { return NOW }})
}

func TestManifestIsABuiltinSkill(t *testing.T) {
	m := attconn.Manifest
	if m.ID != "attachments" || m.Kind != "skill" || !m.Builtin || m.Icon != "paperclip" {
		t.Fatalf("%+v", m)
	}
}

func TestExposesTheToolsWithClassesAndTellsTheModelToSaveOnlyWhatMatters(t *testing.T) {
	e := setup(t)
	if got := strings.Join(conntest.Classes(e.tools), ","); got != "save_attachment:write,list_attachments:read,get_attachment:read,share_attachment:write,unshare_attachment:write,send_media:write,delete_attachment:destructive" {
		t.Fatal(got)
	}
	pt, _ := e.tools.Get("save_attachment")
	for _, s := range []string{"Only save files that are worth keeping", "Do NOT save casual photos", "pass its `fileRef`"} {
		if !strings.Contains(pt.Tool.Description, s) {
			t.Fatal(s)
		}
	}
}

func TestSavesTheFileFromTheBatchRunAsksForNomorAndDedupes(t *testing.T) {
	e := setup(t)
	if r := conntest.Call(t, e.tools, "save_attachment", `{"title":"x"}`); r.B("ok") || !strings.Contains(r.S("error"), "no image or document") {
		t.Fatal(r)
	}
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}, {"kind": "document", "mimeType": "application/pdf", "filename": "tiket.pdf", "data": PDF}}, NOW, "")
	inbound(t, e.db, e.otherID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW, "")
	amb := conntest.Call(t, e.tools, "save_attachment", `{"title":"Tiket"}`)
	if amb.B("ok") || amb.N("files.0.nomor") != 1 || amb.S("files.0.kind") != "image" || amb.N("files.1.nomor") != 2 || amb.S("files.1.filename") != "tiket.pdf" {
		t.Fatal(amb)
	}
	if r := conntest.Call(t, e.tools, "save_attachment", `{"nomor":3,"title":"Tiket"}`); r.S("error") != "There is no file number 3." {
		t.Fatal(r)
	}
	saved := conntest.Call(t, e.tools, "save_attachment", `{"nomor":2,"title":"Tiket BDJ-CGK","description":"Tiket pesawat 12 Des"}`)
	if !saved.B("ok") || saved.B("deduped") || saved.S("message") != `Oke, "Tiket BDJ-CGK" udah aku simpan.` || saved.S("attachment.title") != "Tiket BDJ-CGK" || saved.S("attachment.kind") != "document" || saved.S("attachment.filename") != "tiket.pdf" || saved.S("attachment.mimeType") != "application/pdf" {
		t.Fatal(saved)
	}
	id := saved.S("attachment.id")
	if saved.S("attachment.url") != "https://palui.test/app/attachments?id="+id || saved.S("attachment.fileRef") != "palui:attachment/"+id || !strings.Contains(saved.S("sendToOtherApp"), "fileRef") {
		t.Fatal(saved)
	}
	f, _, err := attachments.Read(e.db, id, KEY)
	testutil.Must(t, err)
	if f.Meta.ChatID != e.chatID || f.Meta.RunID == nil || *f.Meta.RunID != "run_2" {
		t.Fatalf("%+v", f.Meta)
	}
	if r := conntest.Call(t, e.tools, "save_attachment", `{"nomor":2,"title":"lagi"}`); !r.B("ok") || !r.B("deduped") {
		t.Fatal(r)
	}
}

func TestSavesAReceiptFromAnEarlierMessageAfterTheOwnerConfirms(t *testing.T) {
	e := setup(t)
	goreng := base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0, 1})
	inbound(t, e.db, e.chatID, "run_a", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW-60_000, "Nasi goreng")
	inbound(t, e.db, e.chatID, "run_b", []media{{"kind": "image", "mimeType": "image/jpeg", "data": goreng}}, NOW-30_000, "Gorengan")
	inbound(t, e.db, e.chatID, "run_x", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW-25*3600_000, "kemarin")
	inbound(t, e.db, e.otherID, "run_y", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW-1000, "chat lain")
	inbound(t, e.db, e.chatID, "run_1", nil, NOW, "ya")

	hint := conntest.Call(t, e.tools, "save_attachment", `{"title":"Struk"}`)
	if hint.B("ok") || !strings.Contains(hint.S("error"), "earlier: true") || hint.Len("files") != 2 || hint.S("files.0.caption") != "Gorengan" || hint.S("files.1.caption") != "Nasi goreng" || hint.N("files.1.nomor") != 2 {
		t.Fatal(hint)
	}
	if r := conntest.Call(t, e.tools, "save_attachment", `{"earlier":true,"title":"Struk"}`); r.B("ok") || r.Len("files") != 2 {
		t.Fatal(r)
	}
	if r := conntest.Call(t, e.tools, "save_attachment", `{"earlier":true,"nomor":3,"title":"Struk"}`); r.S("error") != "There is no earlier file number 3." {
		t.Fatal(r)
	}
	saved := conntest.Call(t, e.tools, "save_attachment", `{"earlier":true,"nomor":2,"title":"Struk nasi goreng"}`)
	if !saved.B("ok") || saved.B("deduped") || saved.S("attachment.title") != "Struk nasi goreng" || saved.S("attachment.kind") != "image" {
		t.Fatal(saved)
	}
	f, _, _ := attachments.Read(e.db, saved.S("attachment.id"), KEY)
	if base64.StdEncoding.EncodeToString(f.Bytes) != JPG {
		t.Fatal("wrong bytes")
	}
}

func TestSaysSoWhenNothingWasSentRecently(t *testing.T) {
	e := setup(t)
	if r := conntest.Call(t, e.tools, "save_attachment", `{"earlier":true,"title":"x"}`); r.B("ok") || !strings.Contains(r.S("error"), "last 24 hours") {
		t.Fatal(r)
	}
}

func TestRefusesFilesThatFailValidation(t *testing.T) {
	e := setup(t)
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "document", "mimeType": "application/pdf", "filename": "fake.pdf", "data": base64.StdEncoding.EncodeToString([]byte("MZ"))}}, NOW, "")
	if r := conntest.Call(t, e.tools, "save_attachment", `{"title":"x"}`); r.B("ok") || !strings.Contains(r.S("error"), "doesn't match") {
		t.Fatal(r)
	}
}

func TestListsGetsAndDeletesWithinTheTurnsChatOnly(t *testing.T) {
	e := setup(t)
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW, "")
	id := conntest.Call(t, e.tools, "save_attachment", `{"title":"KTP Zaki"}`).S("attachment.id")
	other := otherTools(e)
	if r := conntest.Call(t, e.tools, "list_attachments", `{"query":"ktp"}`); !r.B("ok") || r.N("count") != 1 || r.S("attachments.0.id") != id {
		t.Fatal(r)
	}
	if r := conntest.Call(t, other, "list_attachments", `{}`); r.N("count") != 0 {
		t.Fatal(r)
	}
	if r := conntest.Call(t, e.tools, "get_attachment", `{"id":"`+id+`"}`); r.S("attachment.title") != "KTP Zaki" || r.S("attachment.size") != "6 B" {
		t.Fatal(r)
	}
	if conntest.Call(t, other, "get_attachment", `{"id":"`+id+`"}`).B("ok") || conntest.Call(t, other, "delete_attachment", `{"id":"`+id+`"}`).B("ok") {
		t.Fatal("other chat reached it")
	}
	if !conntest.Call(t, e.tools, "delete_attachment", `{"id":"`+id+`"}`).B("ok") {
		t.Fatal("delete failed")
	}
	if conntest.Call(t, e.tools, "list_attachments", `{}`).N("count") != 0 {
		t.Fatal("still listed")
	}
}

var shareRe = regexp.MustCompile(`^https://palui\.test/api/share/[A-Za-z0-9_-]{32}$`)

func TestSharesAFileAsAPrivateLinkByDefaultPublicOnRequestAndTurnsThemOff(t *testing.T) {
	e := setup(t)
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW, "")
	id := conntest.Call(t, e.tools, "save_attachment", `{"title":"Struk"}`).S("attachment.id")
	priv := conntest.Call(t, e.tools, "share_attachment", `{"id":"`+id+`"}`)
	if !priv.B("ok") || priv.S("visibility") != "private" || priv.S("expiresAt") != kit.ISO(NOW+24*3600_000) || !shareRe.MatchString(priv.S("url")) {
		t.Fatal(priv)
	}
	pub := conntest.Call(t, e.tools, "share_attachment", `{"id":"`+id+`","visibility":"public","expires":"1h"}`)
	parts := strings.Split(pub.S("url"), "/")
	token := parts[len(parts)-1]
	res, err := attachments.ResolveShare(e.db, token, NOW)
	testutil.Must(t, err)
	if !res.OK || res.Share.Visibility != "public" || res.Share.CreatedBy != e.chatID {
		t.Fatalf("%+v", res)
	}
	other := otherTools(e)
	if conntest.Call(t, other, "share_attachment", `{"id":"`+id+`"}`).B("ok") || conntest.Call(t, other, "unshare_attachment", `{"id":"`+id+`"}`).B("ok") {
		t.Fatal("other chat reached it")
	}
	if r := conntest.Call(t, e.tools, "unshare_attachment", `{"id":"`+id+`"}`); !r.B("ok") || r.N("revoked") != 2 {
		t.Fatal(r)
	}
	res, _ = attachments.ResolveShare(e.db, token, NOW)
	if res.OK || res.Reason != "revoked" {
		t.Fatalf("%+v", res)
	}
}

func TestASharedChatGetsNoDashboardLinksFileRefsOrShareLinks(t *testing.T) {
	e := setup(t)
	inbound(t, e.db, e.chatID, "run_1", []media{{"kind": "image", "mimeType": "image/jpeg", "data": JPG}}, NOW, "")
	outside := e.turn
	outside.Mode = "outside"
	shared := attconn.Tools(e.db, outside, opts())
	if got := strings.Join(conntest.Classes(shared), ","); got != "save_attachment:write,list_attachments:read,get_attachment:read,send_media:write,delete_attachment:destructive" {
		t.Fatal(got)
	}
	for _, name := range []string{"save_attachment", "list_attachments", "get_attachment"} {
		pt, _ := shared.Get(name)
		if strings.Contains(pt.Tool.Description, "fileRef") || strings.Contains(pt.Tool.Description, "dashboard") || strings.Contains(pt.Tool.Description, "share_attachment") {
			t.Fatal(name, pt.Tool.Description)
		}
	}
	saved := conntest.Call(t, shared, "save_attachment", `{"title":"Struk"}`)
	id := saved.S("attachment.id")
	if !saved.B("ok") || id == "" || saved.Has("attachment.url") || saved.Has("attachment.fileRef") || saved.Has("sendToOtherApp") {
		t.Fatal(saved)
	}
	list := conntest.Call(t, shared, "list_attachments", `{}`)
	if list.N("count") != 1 || list.Has("attachments.0.url") || list.Has("attachments.0.fileRef") || list.Has("sendToOtherApp") || strings.Contains(list.String(), "palui.test") {
		t.Fatal(list)
	}
	if r := conntest.Call(t, shared, "get_attachment", `{"id":"`+id+`"}`); !r.B("ok") || strings.Contains(r.String(), "palui.test") || strings.Contains(r.String(), "fileRef") {
		t.Fatal(r)
	}

	// The owner's own chat keeps today's shape.
	owner := e.turn
	owner.Mode = "owner"
	own := attconn.Tools(e.db, owner, opts())
	if got := strings.Join(conntest.Classes(own), ","); got != strings.Join(conntest.Classes(e.tools), ",") {
		t.Fatal(got)
	}
	ol := conntest.Call(t, own, "list_attachments", `{}`)
	if ol.S("attachments.0.url") != "https://palui.test/app/attachments?id="+id || ol.S("attachments.0.fileRef") != "palui:attachment/"+id || !strings.Contains(ol.S("sendToOtherApp"), "fileRef") {
		t.Fatal(ol)
	}
	for _, name := range []string{"save_attachment", "list_attachments", "get_attachment"} {
		a, _ := own.Get(name)
		b, _ := e.tools.Get(name)
		if a.Tool.Description != b.Tool.Description {
			t.Fatal(name)
		}
	}
}

package attachments_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const DAY = 24 * testutil.HOUR

func autoChat(t *testing.T, d db.Q, external string, days int64) string {
	t.Helper()
	c, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: external, Kind: "group"}, NOW)
	testutil.Must(t, err)
	on := true
	if _, _, err := chats.SetSaveMedia(d, c.ID, chats.SaveMediaPatch{On: &on, Days: &days}); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func autoSave(t *testing.T, d db.Q, in attachments.AutoInput, now int64) (attachments.Saved, error) {
	t.Helper()
	return attachments.AutoSave(d, in, KEY, now)
}

func TestAutoTitle(t *testing.T) {
	for _, c := range []struct {
		in   attachments.AutoInput
		want string
	}{
		{attachments.AutoInput{Kind: "document", Filename: "dir/Undangan Rapat.pdf", Caption: "ini"}, "Undangan Rapat.pdf"},
		{attachments.AutoInput{Kind: "image", Filename: "x.jpg", Caption: "  struk makan\nsiang tadi"}, "struk makan"},
		{attachments.AutoInput{Kind: "image", SenderName: "Budi"}, "Foto dari Budi"},
		{attachments.AutoInput{Kind: "document", SenderName: "Budi"}, "Dokumen dari Budi"},
		{attachments.AutoInput{Kind: "image"}, "Foto"},
	} {
		if got := attachments.AutoTitle(c.in); got != c.want {
			t.Errorf("%+v: %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAutoSaveKeepsWhoSentItAndDedupesAcrossBothPaths(t *testing.T) {
	d := testutil.DB(t)
	chatID := autoChat(t, d, "g1@g.us", 30)
	s, err := autoSave(t, d, attachments.AutoInput{ChatID: chatID, MessageID: "msg_1", Kind: "document", MimeType: "application/pdf", Filename: "ktp.pdf", Caption: "punya Budi", SenderID: "77@s.whatsapp.net", SenderName: "Budi", SentAt: NOW - 5000, Bytes: PDF}, NOW)
	testutil.Must(t, err)
	a := s.Row
	if a.Origin != "auto" || *a.SenderID != "77@s.whatsapp.net" || *a.SenderName != "Budi" || *a.SentAt != NOW-5000 || *a.MessageID != "msg_1" || a.Title != "ktp.pdf" || a.Description != "punya Budi" {
		t.Fatalf("%+v", a)
	}
	f, ok, err := attachments.Read(d, a.ID, KEY)
	if err != nil || !ok || string(f.Bytes) != string(PDF) || f.Meta.Origin != "auto" {
		t.Fatal(ok, err)
	}
	again, err := autoSave(t, d, attachments.AutoInput{ChatID: chatID, Kind: "document", MimeType: "application/pdf", Bytes: PDF}, NOW)
	testutil.Must(t, err)
	if !again.Deduped || again.Row.ID != a.ID {
		t.Fatalf("%+v", again)
	}
	// save_attachment of the same file finds the auto-saved copy.
	tool := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "KTP", MimeType: "application/pdf", Bytes: PDF}, NOW)
	if !tool.Deduped {
		t.Fatal("tool save made a second copy")
	}
	// A plain tool save is origin "tool" with no sender.
	png := save(t, d, attachments.SaveInput{ChatID: chatID, Title: "Logo", MimeType: "image/png", Bytes: PNG}, NOW)
	if png.Row.Origin != "tool" || png.Row.SenderID != nil || png.Row.SentAt != nil {
		t.Fatalf("%+v", png.Row)
	}
}

func TestAutoSaveRefusesWhatTheStoreRefuses(t *testing.T) {
	d := testutil.DB(t)
	chatID := autoChat(t, d, "g1@g.us", 30)
	_, err := autoSave(t, d, attachments.AutoInput{ChatID: chatID, Kind: "document", MimeType: "application/zip", Bytes: []byte("PK\x03\x04")}, NOW)
	if !attachments.IsError(err) {
		t.Fatal(err)
	}
}

func TestAutoSaveStopsAtTheChatAndTotalQuotas(t *testing.T) {
	d := testutil.DB(t)
	a := autoChat(t, d, "a@g.us", 30)
	b := autoChat(t, d, "b@g.us", 30)
	// Pretend chat a already holds its quota: the size column is what counts.
	s, err := autoSave(t, d, attachments.AutoInput{ChatID: a, Kind: "image", MimeType: "image/png", Bytes: PNG}, NOW)
	testutil.Must(t, err)
	_, err = d.Exec("UPDATE saved_attachments SET size = ? WHERE id = ?", attachments.AutoChatQuotaBytes, s.Row.ID)
	testutil.Must(t, err)
	if _, err := autoSave(t, d, attachments.AutoInput{ChatID: a, Kind: "image", MimeType: "image/jpeg", Bytes: JPEG}, NOW); !errors.Is(err, attachments.ErrQuota) {
		t.Fatalf("chat quota: %v", err)
	}
	// Files saved on purpose don't count toward it.
	_, err = d.Exec("UPDATE saved_attachments SET origin = 'tool' WHERE id = ?", s.Row.ID)
	testutil.Must(t, err)
	if _, err := autoSave(t, d, attachments.AutoInput{ChatID: a, Kind: "image", MimeType: "image/jpeg", Bytes: JPEG}, NOW); err != nil {
		t.Fatal(err)
	}
	// The total cap holds across chats.
	_, err = d.Exec("UPDATE saved_attachments SET size = ? WHERE chat_id = ? AND origin = 'auto'", attachments.AutoTotalQuotaBytes, a)
	testutil.Must(t, err)
	if _, err := autoSave(t, d, attachments.AutoInput{ChatID: b, Kind: "document", MimeType: "application/pdf", Bytes: PDF}, NOW); !errors.Is(err, attachments.ErrQuota) {
		t.Fatalf("total quota: %v", err)
	}
}

func TestPruneAutoFollowsEachChatsRetentionAndSparesToolSaves(t *testing.T) {
	d := testutil.DB(t)
	week := autoChat(t, d, "week@g.us", 7)
	forever := autoChat(t, d, "forever@g.us", 0)
	old := NOW - 8*DAY
	oldAuto, err := autoSave(t, d, attachments.AutoInput{ChatID: week, Kind: "image", MimeType: "image/png", Bytes: PNG}, old)
	testutil.Must(t, err)
	newAuto, err := autoSave(t, d, attachments.AutoInput{ChatID: week, Kind: "image", MimeType: "image/jpeg", Bytes: JPEG}, NOW-6*DAY)
	testutil.Must(t, err)
	oldTool := save(t, d, attachments.SaveInput{ChatID: week, Title: "KTP", MimeType: "application/pdf", Bytes: PDF}, old)
	kept, err := autoSave(t, d, attachments.AutoInput{ChatID: forever, Kind: "image", MimeType: "image/png", Bytes: PNG}, NOW-400*DAY)
	testutil.Must(t, err)
	_, _, err = attachments.CreateShare(d, attachments.ShareInput{AttachmentID: oldAuto.Row.ID, Visibility: "private", Duration: "24h", CreatedBy: "owner"}, NOW)
	testutil.Must(t, err)

	n, err := attachments.PruneAuto(d, NOW)
	testutil.Must(t, err)
	if n != 1 {
		t.Fatalf("pruned %d", n)
	}
	for id, want := range map[string]bool{oldAuto.Row.ID: false, newAuto.Row.ID: true, oldTool.Row.ID: true, kept.Row.ID: true} {
		if _, ok, _ := attachments.Get(d, id); ok != want {
			t.Errorf("%s: exists %v, want %v", id, ok, want)
		}
	}
	var shares int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM attachment_shares").Scan(&shares))
	if shares != 0 {
		t.Fatal("the pruned file's link survived")
	}
}

func TestDeleteAutoRemovesOnlyThatChatsAutoSavedFiles(t *testing.T) {
	d := testutil.DB(t)
	a := autoChat(t, d, "a@g.us", 30)
	b := autoChat(t, d, "b@g.us", 30)
	for _, id := range []string{a, b} {
		_, err := autoSave(t, d, attachments.AutoInput{ChatID: id, Kind: "image", MimeType: "image/png", Bytes: PNG}, NOW)
		testutil.Must(t, err)
	}
	save(t, d, attachments.SaveInput{ChatID: a, Title: "KTP", MimeType: "application/pdf", Bytes: PDF}, NOW)
	n, err := attachments.DeleteAuto(d, a)
	testutil.Must(t, err)
	count, size, err := attachments.AutoCount(d, a)
	testutil.Must(t, err)
	if n != 1 || count != 0 || size != 0 {
		t.Fatal(n, count, size)
	}
	if left, _, _ := attachments.AutoCount(d, b); left != 1 {
		t.Fatal("deleted another chat's files")
	}
	if all, _ := attachments.Count(d); all != 2 {
		t.Fatalf("%d files left", all)
	}
}

// Channels deliver a forwarded album or a re-sent file as separate messages,
// and each auto-save runs in its own goroutine.
func TestAutoSaveKeepsOneCopyWhenTheSameFileArrivesAtOnce(t *testing.T) {
	d := testutil.DB(t)
	chatID := autoChat(t, d, "g1@g.us", 30)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := attachments.AutoSave(d, attachments.AutoInput{ChatID: chatID, Kind: "document", MimeType: "application/pdf", Bytes: PDF}, KEY, NOW); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	n, _, err := attachments.AutoCount(d, chatID)
	testutil.Must(t, err)
	if n != 1 {
		t.Fatalf("%d copies, want 1", n)
	}
}

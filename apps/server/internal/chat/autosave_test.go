package chat_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/models"
)

var (
	jpegBytes = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46}
	pdfBytes  = []byte("%PDF-1.7\nundangan rapat")
)

func withMedia(m ...connectors.InboundMedia) func(*connectors.InboundMessage) {
	return func(msg *connectors.InboundMessage) { msg.Media = m }
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func turnOnSave(t *testing.T, h *agentWorld, on bool) {
	t.Helper()
	g := groupChat(t, h.DB)
	if _, ok, err := chats.SetSaveMedia(h.DB, g.ID, chats.SaveMediaPatch{On: &on}); err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestAutoSaveMedia(t *testing.T) {
	t.Run("keeps a guest's photo and document from group talk, with who sent it and the caption", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok")}, extra{})
		h.owner("/agent chef")
		h.Engine.Idle()
		turnOnSave(t, h, true)
		runs := len(allRuns(t, h.DB))
		h.guest("jadwal rapat besok", notAddressed, withMedia(
			connectors.InboundMedia{Kind: "image", MimeType: "image/jpeg", Data: b64(jpegBytes), Caption: "jadwal rapat besok"},
			connectors.InboundMedia{Kind: "document", MimeType: "application/pdf", Filename: "undangan.pdf", Data: b64(pdfBytes), Caption: "jadwal rapat besok"},
		))
		h.Engine.Idle()
		g := groupChat(t, h.DB)
		rows := need(attachments.List(h.DB, attachments.ListOptions{ChatID: g.ID}))
		eq(t, len(rows), 2)
		byKind := map[string]int{}
		for _, a := range rows {
			byKind[a.Kind]++
			eq(t, a.Origin, attachments.OriginAuto)
			eq(t, *a.SenderName, "Budi")
			eq(t, *a.SenderID, "77")
			eq(t, a.Description, "jadwal rapat besok")
			if a.MessageID == nil || a.SentAt == nil {
				t.Fatalf("%+v", a)
			}
			if a.Kind == "document" {
				eq(t, a.Title, "undangan.pdf")
			} else {
				eq(t, a.Title, "jadwal rapat besok")
			}
		}
		eq(t, byKind["image"], 1)
		eq(t, byKind["document"], 1)
		// Still group talk: no turn ran, and the message lost its bytes.
		eq(t, len(allRuns(t, h.DB)), runs)
		row := lastUserRow(t, h.DB)
		eq(t, row.Status, "context")
		if strings.Contains(string(row.Content), b64(pdfBytes)) {
			t.Fatal("context message kept the bytes")
		}
	})

	t.Run("keeps nothing while the chat's auto-save is off (the default)", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok")}, extra{})
		h.owner("/agent chef")
		h.Engine.Idle()
		eq(t, groupChat(t, h.DB).SaveMedia, false)
		eq(t, groupChat(t, h.DB).SaveMediaDays, int64(30))
		h.guest("foto", notAddressed, withMedia(connectors.InboundMedia{Kind: "image", MimeType: "image/jpeg", Data: b64(jpegBytes)}))
		h.Engine.Idle()
		eq(t, count(t, h.DB, "saved_attachments"), 0)
	})

	t.Run("skips stickers, audio and media without bytes", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok")}, extra{})
		h.owner("/agent chef")
		h.Engine.Idle()
		turnOnSave(t, h, true)
		h.guest("[sticker]", notAddressed, withMedia(connectors.InboundMedia{Kind: "image", MimeType: "image/png", Data: b64([]byte{0x89, 0x50, 0x4e, 0x47}), Sticker: true}))
		h.guest("vn", notAddressed, withMedia(connectors.InboundMedia{Kind: "audio", MimeType: "audio/ogg", Data: b64([]byte("OggS"))}))
		h.guest("foto", notAddressed, withMedia(connectors.InboundMedia{Kind: "image", MimeType: "image/jpeg"}))
		h.Engine.Idle()
		eq(t, count(t, h.DB, "saved_attachments"), 0)
	})

	t.Run("never keeps a file from a message the sender check drops", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok")}, extra{})
		// A group with no agent: guests are dropped.
		h.owner("@bot halo", addressed)
		h.Engine.Idle()
		turnOnSave(t, h, true)
		if r := h.guest("@bot foto", withMedia(connectors.InboundMedia{Kind: "image", MimeType: "image/jpeg", Data: b64(jpegBytes)})); r != nil {
			t.Fatal(r)
		}
		h.Engine.Idle()
		eq(t, count(t, h.DB, "saved_attachments"), 0)
	})

	t.Run("saves a file fetched only for auto-save but leaves it out of the answered message", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok"), models.Say("ok")}, extra{})
		h.owner("/agent chef")
		h.Engine.Idle()
		turnOnSave(t, h, true)
		h.guest("@bot ini undangannya\n[document undangan.pdf is over the 1 MB limit and was not read]", withMedia(
			connectors.InboundMedia{Kind: "document", MimeType: "application/pdf", Filename: "undangan.pdf", Data: b64(pdfBytes), SaveOnly: true, Caption: "@bot ini undangannya"},
		))
		h.Engine.Idle()
		eq(t, count(t, h.DB, "saved_attachments WHERE origin = 'auto'"), 1)
		row := lastUserRow(t, h.DB)
		if strings.Contains(string(row.Content), "\"media\"") {
			t.Fatalf("the model would see the file: %s", row.Content)
		}
	})

	t.Run("one copy when the same file is sent twice", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok")}, extra{})
		h.owner("/agent chef")
		h.Engine.Idle()
		turnOnSave(t, h, true)
		for range 2 {
			h.guest("foto", notAddressed, withMedia(connectors.InboundMedia{Kind: "image", MimeType: "image/jpeg", Data: b64(jpegBytes)}))
		}
		h.Engine.Idle()
		eq(t, count(t, h.DB, "saved_attachments"), 1)
	})

	t.Run("asks channels to fetch media only in an allowed chat with auto-save on", func(t *testing.T) {
		h := agentsWorld(t, []models.Step{models.Say("ok")}, extra{})
		saves := func() bool { return h.Engine.Services.SavesMedia(tgMsg("foto", guestMsg("foto", notAddressed))) }
		eq(t, saves(), false)
		h.owner("/agent chef")
		h.Engine.Idle()
		eq(t, saves(), false)
		turnOnSave(t, h, true)
		eq(t, saves(), true)
		eq(t, h.Engine.Services.WantsMedia(tgMsg("foto", guestMsg("foto", notAddressed))), false)
	})
}

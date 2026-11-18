package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/zakiego/palui/apps/server/internal/db"
)

func TestBuildMediaMessagePerKind(t *testing.T) {
	up := whatsmeow.UploadResponse{URL: "https://mmg/x", DirectPath: "/x", MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3}, FileLength: 9}
	quote := &waE2E.ContextInfo{StanzaID: new(string)}
	img := buildMediaMessage(&Media{Kind: db.MediaImage, MimeType: "image/png"}, "cap", up, quote, 5).GetImageMessage()
	if img.GetCaption() != "cap" || img.GetMimetype() != "image/png" || img.GetURL() != up.URL || img.GetFileLength() != 9 || img.GetContextInfo() != quote || img.GetMediaKeyTimestamp() != 5 {
		t.Fatalf("image %v", img)
	}
	voice := buildMediaMessage(&Media{Kind: db.MediaVoice, MimeType: "audio/ogg"}, "", up, nil, 5).GetAudioMessage()
	if !voice.GetPTT() || voice.GetMimetype() != "audio/ogg; codecs=opus" {
		t.Fatalf("voice %v", voice)
	}
	audio := buildMediaMessage(&Media{Kind: db.MediaAudio, MimeType: "audio/mpeg"}, "", up, nil, 5).GetAudioMessage()
	if audio.GetPTT() || audio.GetMimetype() != "audio/mpeg" {
		t.Fatalf("audio %v", audio)
	}
	if v := buildMediaMessage(&Media{Kind: db.MediaVideo, MimeType: "video/mp4"}, "c", up, nil, 5).GetVideoMessage(); v.GetCaption() != "c" {
		t.Fatalf("video %v", v)
	}
	if s := buildMediaMessage(&Media{Kind: db.MediaSticker, MimeType: "image/webp"}, "", up, nil, 5).GetStickerMessage(); s.GetMimetype() != "image/webp" {
		t.Fatalf("sticker %v", s)
	}
	doc := buildMediaMessage(&Media{Kind: db.MediaDocument, MimeType: "application/pdf", Filename: "a.pdf"}, "c", up, nil, 5).GetDocumentMessage()
	if doc.GetFileName() != "a.pdf" || doc.GetCaption() != "c" || doc.GetMimetype() != "application/pdf" {
		t.Fatalf("document %v", doc)
	}
}

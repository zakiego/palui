package whatsapp

import (
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestParseVCard(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want VCard
	}{
		{"whatsapp card with waid", "BEGIN:VCARD\nVERSION:3.0\nN:;Primadona ✂️;;;\nFN:Primadona ✂️\nTEL;type=CELL;type=VOICE;waid=6281200001111:+62 812-0000-1111\nEND:VCARD",
			VCard{Name: "Primadona ✂️", Phones: []string{"+62 812-0000-1111"}}},
		{"grouped TEL, CRLF and two numbers", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Toko Contoh\r\nitem1.TEL;waid=6281200002222:+62 812-0000-2222\r\nitem1.X-ABLabel:Ponsel\r\nitem2.TEL:0511-000000\r\nEND:VCARD",
			VCard{Name: "Toko Contoh", Phones: []string{"+62 812-0000-2222", "0511-000000"}}},
		{"no FN falls back to N, empty TEL to waid", "BEGIN:VCARD\nN:Contoh;Budi;;;\nTEL;waid=6281200003333:\nEND:VCARD",
			VCard{Name: "Budi Contoh", Phones: []string{"+6281200003333"}}},
		{"folded line and escapes", "BEGIN:VCARD\nFN:Bengkel\\, Cabang\n  Utara\nEND:VCARD",
			VCard{Name: "Bengkel, Cabang Utara"}},
		{"empty", "", VCard{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseVCard(c.in); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestContentLabel(t *testing.T) {
	card := func(display, vcard string) *waE2E.ContactMessage {
		c := &waE2E.ContactMessage{Vcard: proto.String(vcard)}
		if display != "" {
			c.DisplayName = proto.String(display)
		}
		return c
	}
	opt := func(n string) *waE2E.PollCreationMessage_Option {
		return &waE2E.PollCreationMessage_Option{OptionName: proto.String(n)}
	}
	cases := []struct {
		name string
		in   *waE2E.Message
		want string
	}{
		{"contact", &waE2E.Message{ContactMessage: card("Primadona ✂️", "BEGIN:VCARD\nFN:Primadona\nTEL;waid=6281200001111:+62 812-0000-1111\nEND:VCARD")},
			"[contact: Primadona ✂️, +62 812-0000-1111]"},
		{"contact without a number or name", &waE2E.Message{ContactMessage: card("", "BEGIN:VCARD\nEND:VCARD")},
			"[contact: tanpa nama, tanpa nomor]"},
		{"several contacts", &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{DisplayName: proto.String("2 kontak"), Contacts: []*waE2E.ContactMessage{
			card("Ani", "BEGIN:VCARD\nTEL:+62 811-0000-0001\nEND:VCARD"),
			card("", "BEGIN:VCARD\nFN:Budi\nTEL:+62 811-0000-0002\nTEL:+62 811-0000-0003\nEND:VCARD"),
		}}}, "[contacts: Ani, +62 811-0000-0001; Budi, +62 811-0000-0002, +62 811-0000-0003]"},
		{"empty contacts array", &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{DisplayName: proto.String("2 kontak")}}, "[contacts: 2 kontak]"},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{Name: proto.String("Kantor"), Address: proto.String("Jl. Contoh 1"), DegreesLatitude: proto.Float64(-3.4425), DegreesLongitude: proto.Float64(114.8123)}},
			"[location: Kantor, Jl. Contoh 1, -3.442500,114.812300, https://maps.google.com/?q=-3.442500,114.812300]"},
		{"live location", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{DegreesLatitude: proto.Float64(1), DegreesLongitude: proto.Float64(2)}},
			"[live location: 1.000000,2.000000, https://maps.google.com/?q=1.000000,2.000000]"},
		{"poll", &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{Name: proto.String("Makan di mana?"), Options: []*waE2E.PollCreationMessage_Option{opt("Soto"), opt("Lalapan")}}},
			"[poll: Makan di mana? | options: Soto / Lalapan]"},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("lihat")}}, "[video]"},
		{"gif", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{GifPlayback: proto.Bool(true)}}, "[GIF]"},
		{"video note", &waE2E.Message{PtvMessage: &waE2E.VideoMessage{}}, "[video note]"},
		{"event", &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("Bukber")}}, "[event: Bukber]"},
		{"text has none", &waE2E.Message{Conversation: proto.String("halo")}, ""},
		{"photo has none", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, ""},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ContentLabel(c.in); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
	// A reply to a contact card keeps its number too.
	quoted := &waE2E.Message{ContactMessage: card("Primadona ✂️", "BEGIN:VCARD\nTEL:+62 812-0000-1111\nEND:VCARD")}
	if got := MediaLabel(quoted); got != "[contact: Primadona ✂️, +62 812-0000-1111]" {
		t.Fatalf("quote label %q", got)
	}
}

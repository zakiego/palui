package whatsapp

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// The real socket never connects in tests; these check the store it opens
// and how whatsmeow's events become Baileys-style updates.

func TestMeowSocketOpensTheStoreWithoutConnecting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wa", "whatsapp.db")
	s, err := NewMeowSocket(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Linked() || s.User() != nil {
		t.Fatal("a new store is not linked")
	}
	s.End()
	s.End()
	if got := InspectSession(path); got != SessionIncomplete {
		t.Fatalf("fresh store is %s", got)
	}

	// A paired device makes the socket linked, with the account's JIDs.
	d, err := sql.Open("sqlite", storeDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	c := sqlstore.NewWithDB(d, "sqlite", waLog.Noop)
	if err := c.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	dev := c.NewDevice()
	jid := types.NewADJID("628980001472", 0, 3)
	dev.ID = &jid
	dev.LID = types.NewADJID("111222333", 0, 3)
	dev.LID.Server = types.HiddenUserServer
	dev.PushName = "Palui"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignatureKey: make([]byte, 32), AccountSignature: make([]byte, 64), DeviceSignature: make([]byte, 64)}
	if err := c.PutDevice(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if got := InspectSession(path); got != SessionLinked {
		t.Fatalf("paired store is %s", got)
	}
	s, err = NewMeowSocket(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.End()
	u := s.User()
	if !s.Linked() || u == nil || u.ID != "628980001472:3@s.whatsapp.net" || u.LID != "111222333:3@lid" || u.Name != "Palui" {
		t.Fatalf("%+v", u)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatal("the store runs in WAL mode")
	}
}

func TestMeowEventsMapToBaileysCloseCodes(t *testing.T) {
	s := &meowSocket{updates: map[int]func(Update){}, upserts: map[int]func(Upsert){}}
	var got []Update
	var ups []Upsert
	off := s.OnUpdate(func(u Update) { got = append(got, u) })
	s.OnUpsert(func(u Upsert) { ups = append(ups, u) })
	for _, e := range []any{
		&events.Connected{},
		&events.StreamReplaced{},
		&events.LoggedOut{Reason: events.ConnectFailureLoggedOut},
		&events.Disconnected{},
		&events.TemporaryBan{},
		&events.ClientOutdated{},
		&events.ConnectFailure{Reason: events.ConnectFailureReason(403)},
		&events.ManualLoginReconnect{},
		&events.KeepAliveTimeout{ErrorCount: 1, LastSuccess: time.Now()},
		&events.KeepAliveTimeout{ErrorCount: 9, LastSuccess: time.Now().Add(-4 * time.Minute)},
		&events.PairSuccess{},
	} {
		s.handle(e)
	}
	want := []Update{{Connection: "open"}, {Connection: "close", StatusCode: 440}, {Connection: "close", StatusCode: 401}, {Connection: "close", StatusCode: 428},
		{Connection: "close", StatusCode: 402}, {Connection: "close", StatusCode: 405}, {Connection: "close", StatusCode: 403}, {Connection: "close", StatusCode: 515},
		{Connection: "close", StatusCode: 408}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: %+v, want %+v", i, got[i], want[i])
		}
	}

	// Messages: a DM on a LID with its phone alt, and a group message.
	dmEvt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: types.NewJID("555000111", types.HiddenUserServer), Sender: types.NewJID("555000111", types.HiddenUserServer), SenderAlt: types.NewJID("628123456789", types.DefaultUserServer)},
			ID:            "IN1", PushName: "Zaki", Timestamp: time.Unix(1_700_000_000, 0),
		},
		Message:    &waE2E.Message{Conversation: proto.String("unwrapped")},
		RawMessage: &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("unwrapped")}}},
	}
	s.handle(dmEvt)
	groupEvt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: types.NewJID("120363", types.GroupServer), Sender: types.NewADJID("62811", 0, 4), SenderAlt: types.NewJID("777", types.HiddenUserServer), IsGroup: true},
			ID:            "G1", Timestamp: time.Unix(1_700_000_001, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("hai")},
	}
	s.handle(groupEvt)
	if len(ups) != 2 || ups[0].Type != "notify" {
		t.Fatalf("%+v", ups)
	}
	m := ups[0].Messages[0]
	if m.Key != (Key{RemoteJID: "555000111@lid", RemoteJIDAlt: "628123456789@s.whatsapp.net", ID: "IN1"}) || m.PushName != "Zaki" || m.Timestamp != 1_700_000_000 || m.Message.GetEphemeralMessage() == nil || TextOf(Unwrap(m.Message)) != "unwrapped" {
		t.Fatalf("dm %+v", m)
	}
	g := ups[1].Messages[0]
	if g.Key != (Key{RemoteJID: "120363@g.us", Participant: "62811:4@s.whatsapp.net", ParticipantAlt: "777@lid", ID: "G1"}) {
		t.Fatalf("group %+v", g.Key)
	}

	// Nothing reaches a detached listener or an ended socket.
	off()
	s.handle(&events.Connected{})
	if len(got) != len(want) {
		t.Fatal("detached listener still called")
	}
	s.ended = true
	s.handle(groupEvt)
	if len(ups) != 2 {
		t.Fatal("ended socket still emits")
	}
}

// The real socket must download every kind MediaOf hands over; a sticker once
// fell through to "no media" (rpt_0mwkvfallcmflwkzcev).
func TestDownloadableCoversEveryMediaKind(t *testing.T) {
	for _, c := range []*waE2E.Message{
		{ImageMessage: &waE2E.ImageMessage{}},
		{StickerMessage: &waE2E.StickerMessage{}},
		{AudioMessage: &waE2E.AudioMessage{}},
		{DocumentMessage: &waE2E.DocumentMessage{}},
	} {
		if MediaOf(c) == nil || downloadable(c) == nil {
			t.Fatalf("%v: MediaOf and downloadable disagree", c)
		}
	}
	if downloadable(&waE2E.Message{Conversation: proto.String("hi")}) != nil || downloadable(nil) != nil {
		t.Fatal("text has nothing to download")
	}
}

// Package watest is whatsapp/test-socket.ts: a fake WhatsApp socket. The test
// emits connection and message events and inspects what the runtime sent.
// Nothing here touches the network.
package watest

import (
	"context"
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
)

// Sent is one SendMessage call.
type Sent struct {
	JID     string
	Content whatsapp.Content
	Quoted  *whatsapp.Message
}

// Presence is one presence update.
type Presence struct {
	Type string
	JID  string
}

// FakeSocket is FakeSocket.
type FakeSocket struct {
	mu              sync.Mutex
	seq             int
	updates         map[int]func(whatsapp.Update)
	upserts         map[int]func(whatsapp.Upsert)
	groupChanges    map[int]func(string)
	groupCalls      int
	sent            []Sent
	presence        []Presence
	pairingRequests []string
	PairingCode     string
	ended           bool
	loggedOut       bool
	opened          bool
	user            *whatsapp.User
	registered      bool
	Lids            map[string]string
	nextID          int
	// Media bytes for Download; nil gives "media:<id>".
	Media func(m *whatsapp.Message) ([]byte, error)
	// Downloads is every message id downloaded, in order.
	downloads []string
	// GroupSubjects answers GroupSubject.
	GroupSubjects map[string]string
	// GroupMembers and GroupMembersErr answer GroupParticipants; a group in
	// neither is an error.
	GroupMembers    map[string][]whatsapp.GroupMember
	GroupMembersErr map[string]error
}

// New is new FakeSocket({ registered }).
func New(registered bool) *FakeSocket {
	return &FakeSocket{
		updates: map[int]func(whatsapp.Update){}, upserts: map[int]func(whatsapp.Upsert){},
		PairingCode: "ABCD1234", registered: registered, Lids: map[string]string{}, nextID: 1,
		GroupSubjects: map[string]string{}, groupChanges: map[int]func(string){},
		GroupMembers: map[string][]whatsapp.GroupMember{}, GroupMembersErr: map[string]error{},
	}
}

func (f *FakeSocket) OnUpdate(fn func(whatsapp.Update)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := f.seq
	f.updates[id] = fn
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.updates, id)
	}
}

func (f *FakeSocket) OnUpsert(fn func(whatsapp.Upsert)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := f.seq
	f.upserts[id] = fn
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.upserts, id)
	}
}

// ListenerCount is how many connection.update listeners are attached.
func (f *FakeSocket) ListenerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updates)
}

func (f *FakeSocket) Open() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = true
}

// Emit sends a connection update to the listeners.
func (f *FakeSocket) Emit(u whatsapp.Update) {
	f.mu.Lock()
	fns := make([]func(whatsapp.Update), 0, len(f.updates))
	for i := 1; i <= f.seq; i++ {
		if fn, ok := f.updates[i]; ok {
			fns = append(fns, fn)
		}
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(u)
	}
}

// OpenConn is open(userId): the account logs in.
func (f *FakeSocket) OpenConn(userID string) {
	if userID == "" {
		userID = "628980001472:3@s.whatsapp.net"
	}
	f.mu.Lock()
	f.user = &whatsapp.User{ID: userID, LID: "111222333:3@lid", Name: "Palui"}
	f.registered = true
	f.mu.Unlock()
	f.Emit(whatsapp.Update{Connection: "open"})
}

// Close is close(statusCode).
func (f *FakeSocket) Close(code int) { f.Emit(whatsapp.Update{Connection: "close", StatusCode: code}) }

// QR is qr(value).
func (f *FakeSocket) QR(v string) { f.Emit(whatsapp.Update{QR: v}) }

// Message is message(m, type): one messages.upsert ("notify" when "").
func (f *FakeSocket) Message(m *whatsapp.Message, typ string) {
	if typ == "" {
		typ = "notify"
	}
	f.mu.Lock()
	fns := make([]func(whatsapp.Upsert), 0, len(f.upserts))
	for i := 1; i <= f.seq; i++ {
		if fn, ok := f.upserts[i]; ok {
			fns = append(fns, fn)
		}
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(whatsapp.Upsert{Messages: []*whatsapp.Message{m}, Type: typ})
	}
}

func (f *FakeSocket) User() *whatsapp.User {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.user == nil {
		return nil
	}
	u := *f.user
	return &u
}

func (f *FakeSocket) Linked() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registered
}

func (f *FakeSocket) SendMessage(_ context.Context, jid string, content whatsapp.Content, quoted *whatsapp.Message) (*whatsapp.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, Sent{JID: jid, Content: content, Quoted: quoted})
	id := fmt.Sprintf("OUT%d", f.nextID)
	f.nextID++
	m := &whatsapp.Message{Key: whatsapp.Key{RemoteJID: jid, FromMe: true, ID: id}, Message: &waE2E.Message{}}
	if content.React == nil {
		m.Message.Conversation = proto.String(content.Text)
	}
	return m, nil
}

func (f *FakeSocket) RequestPairingCode(_ context.Context, phone string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairingRequests = append(f.pairingRequests, phone)
	return f.PairingCode, nil
}

func (f *FakeSocket) SendPresenceUpdate(_ context.Context, state, jid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.presence = append(f.presence, Presence{state, jid})
	return nil
}

func (f *FakeSocket) PresenceSubscribe(context.Context, string) error { return nil }

func (f *FakeSocket) GroupSubject(_ context.Context, jid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.GroupSubjects[jid]; ok {
		return s, nil
	}
	return "", fmt.Errorf("no metadata for %s", jid)
}

func (f *FakeSocket) GroupParticipants(_ context.Context, jid string) ([]whatsapp.GroupMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupCalls++
	if err, ok := f.GroupMembersErr[jid]; ok {
		return nil, err
	}
	if list, ok := f.GroupMembers[jid]; ok {
		return append([]whatsapp.GroupMember(nil), list...), nil
	}
	return nil, fmt.Errorf("no participants for %s", jid)
}

// GroupMemberCalls is how many times GroupParticipants was called.
func (f *FakeSocket) GroupMemberCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupCalls
}

func (f *FakeSocket) OnGroupChange(fn func(string)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := f.seq
	f.groupChanges[id] = fn
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.groupChanges, id)
	}
}

// EmitGroupChange reports that a group's metadata or participants changed.
func (f *FakeSocket) EmitGroupChange(jid string) {
	f.mu.Lock()
	fns := make([]func(string), 0, len(f.groupChanges))
	for i := 1; i <= f.seq; i++ {
		if fn, ok := f.groupChanges[i]; ok {
			fns = append(fns, fn)
		}
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(jid)
	}
}

func (f *FakeSocket) PNForLID(_ context.Context, lid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Lids[lid], nil
}

func (f *FakeSocket) LIDForPN(_ context.Context, pn string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for lid, p := range f.Lids {
		if p == pn {
			return lid, nil
		}
	}
	return "", nil
}

func (f *FakeSocket) Download(_ context.Context, m *whatsapp.Message) ([]byte, error) {
	f.mu.Lock()
	f.downloads = append(f.downloads, m.Key.ID)
	media := f.Media
	f.mu.Unlock()
	if media != nil {
		return media(m)
	}
	return []byte("media:" + m.Key.ID), nil
}

func (f *FakeSocket) Logout(context.Context) error {
	f.mu.Lock()
	f.loggedOut = true
	f.mu.Unlock()
	f.Close(401)
	return nil
}

func (f *FakeSocket) End() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = true
}

// --- inspection ---------------------------------------------------------------------

func (f *FakeSocket) SentList() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Sent(nil), f.sent...)
}

func (f *FakeSocket) PresenceList() []Presence {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Presence(nil), f.presence...)
}

func (f *FakeSocket) PairingRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pairingRequests...)
}

func (f *FakeSocket) Downloads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.downloads...)
}

func (f *FakeSocket) Ended() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended
}

func (f *FakeSocket) LoggedOut() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loggedOut
}

func (f *FakeSocket) SetLID(lid, pn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Lids[lid] = pn
}

// HasPresence reports whether {type, jid} was sent.
func (f *FakeSocket) HasPresence(typ, jid string) bool {
	for _, p := range f.PresenceList() {
		if p.Type == typ && p.JID == jid {
			return true
		}
	}
	return false
}

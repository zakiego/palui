package whatsapp

// The slice of a WhatsApp socket the runtime uses (whatsapp/socket.ts). Tests
// pass a fake socket (watest); the real one wraps whatsmeow (meow.go) and is
// only created when the runtime starts for real.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Close codes: Baileys' DisconnectReason values. The real socket maps
// whatsmeow's events onto them (see meow.go and the Decisions log), so the
// runtime's close handling stays the TS state machine.
const (
	CloseLoggedOut       = 401 // events.LoggedOut
	CloseTempBanned      = 402 // events.TemporaryBan
	CloseTimedOut        = 408 // the QR codes ran out, or keepalive failed for too long
	CloseConnectionLost  = 428 // events.Disconnected, or Connect failed
	CloseReplaced        = 440 // events.StreamReplaced
	CloseRestartRequired = 515 // events.ManualLoginReconnect (whatsmeow handles 515 itself)
)

// Key identifies a message, like Baileys' WAMessageKey.
type Key struct {
	RemoteJID      string `json:"remoteJid,omitempty"`
	RemoteJIDAlt   string `json:"remoteJidAlt,omitempty"`
	Participant    string `json:"participant,omitempty"`
	ParticipantAlt string `json:"participantAlt,omitempty"`
	FromMe         bool   `json:"fromMe"`
	ID             string `json:"id,omitempty"`
}

// Message is one message as the runtime sees it (WAMessage).
type Message struct {
	Key      Key
	Message  *waE2E.Message
	PushName string
	// Unix seconds; 0 when unknown.
	Timestamp int64
	// The library's own event, for media downloads.
	Raw any
}

// Update is a connection.update.
type Update struct {
	// "", "connecting", "open" or "close".
	Connection string
	QR         string
	// Close: the Baileys status code (0 = unknown).
	StatusCode int
}

// Upsert is a messages.upsert batch. Type "notify" is real time.
type Upsert struct {
	Messages []*Message
	Type     string
}

// User is the linked account.
type User struct {
	ID   string
	LID  string
	Name string
}

// Content is { text }, { react: { text, key } } or { media } (Text is then the caption).
type Content struct {
	Text  string    `json:"text,omitempty"`
	React *Reaction `json:"react,omitempty"`
	Media *Media    `json:"media,omitempty"`
	// JIDs to @mention (ContextInfo.MentionedJID); Text holds "@<user part>" for each.
	Mentions []string `json:"mentions,omitempty"`
}

// Media is a file to upload and send: Kind is a db.Media* kind.
type Media struct {
	Kind     string `json:"kind"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename,omitempty"`
	Bytes    []byte `json:"-"`
}

type Reaction struct {
	Text string `json:"text"`
	Key  Key    `json:"key"`
}

// Socket is WaSocket.
type Socket interface {
	// ev.on("connection.update") and ev.on("messages.upsert"); the func detaches.
	OnUpdate(fn func(Update)) (off func())
	OnUpsert(fn func(Upsert)) (off func())
	// Start connecting. The TS socket connected on creation; here it waits
	// until the runtime listens, so no event is missed.
	Open()
	// nil before the account is known.
	User() *User
	// isLinked(creds): pairing succeeded.
	Linked() bool
	SendMessage(ctx context.Context, jid string, content Content, quoted *Message) (*Message, error)
	RequestPairingCode(ctx context.Context, phone string) (string, error)
	// "composing" or "paused".
	SendPresenceUpdate(ctx context.Context, state, jid string) error
	PresenceSubscribe(ctx context.Context, jid string) error
	GroupSubject(ctx context.Context, jid string) (string, error)
	// Everyone in the group, bare JIDs. ID is the participant's main JID, Alt its other form ("" when unknown).
	GroupParticipants(ctx context.Context, jid string) ([]GroupMember, error)
	// A group's metadata or participants changed, or this account joined one; the func detaches.
	OnGroupChange(fn func(jid string)) (off func())
	// "" when unknown.
	PNForLID(ctx context.Context, lid string) (string, error)
	LIDForPN(ctx context.Context, pn string) (string, error)
	Download(ctx context.Context, m *Message) ([]byte, error)
	Logout(ctx context.Context) error
	End()
}

// GroupMember is one participant of a group as the socket reports it.
type GroupMember struct{ ID, Alt string }

// SocketFactory creates a socket on the session store at storePath.
type SocketFactory func(ctx context.Context, storePath string) (Socket, error)

// --- session store ---------------------------------------------------------------------

// StorePathFor is the whatsmeow SQL store of a connector: $DATA_DIR/wa/<id>.db.
func StorePathFor(dataDir, connectorID string) string {
	return filepath.Join(dataDir, "wa", connectorID+".db")
}

// LockPathFor is a sibling of the store, so wiping the session never removes the lock.
func LockPathFor(dataDir, connectorID string) string {
	return filepath.Join(dataDir, "wa", connectorID+".lock")
}

// SessionState is none, linked or incomplete.
type SessionState string

const (
	SessionNone       SessionState = "none"
	SessionLinked     SessionState = "linked"
	SessionIncomplete SessionState = "incomplete"
)

// storeDSN opens a whatsmeow store with the modernc driver. whatsmeow's
// sqlstore refuses SQLite without foreign keys.
func storeDSN(path string) string {
	return "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

// InspectSession is inspectSession. "linked": the store has a paired device.
// "incomplete": the store exists without one, left by a pairing that never
// finished; it is worthless and safe to delete. "none": no store, or one that
// can't be read.
func InspectSession(path string) SessionState {
	if _, err := os.Stat(path); err != nil {
		return SessionNone
	}
	d, err := sql.Open("sqlite", storeDSN(path))
	if err != nil {
		return SessionNone
	}
	defer d.Close()
	var n int
	err = d.QueryRow("SELECT COUNT(*) FROM whatsmeow_device").Scan(&n)
	switch {
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return SessionIncomplete
	case err != nil:
		return SessionNone
	case n > 0:
		return SessionLinked
	}
	return SessionIncomplete
}

// WipeSession deletes the store file and its WAL, SHM and journal files. The
// lock file stays.
func WipeSession(path string) error {
	var errs []error
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ClearIncompleteSession wipes an incomplete session and says what it found.
func ClearIncompleteSession(path string) SessionState {
	state := InspectSession(path)
	if state == SessionIncomplete {
		_ = WipeSession(path)
	}
	return state
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

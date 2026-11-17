package whatsapp

// The real socket: whatsmeow behind the Socket interface. whatsmeow's events
// are mapped onto Baileys' connection updates and close codes, so the runtime
// keeps the TS state machine:
//
//	events.Connected              -> open
//	QR channel "code"             -> qr
//	QR channel "timeout"          -> close 408 (codes ran out, pairing window over)
//	events.StreamReplaced         -> close 440 (replaced: pause, no reconnect)
//	events.LoggedOut              -> close 401 (logged out: error "unlinked")
//	events.Disconnected           -> close 428 (connection lost: backoff reconnect)
//	Connect() failing             -> close 428
//	events.KeepAliveTimeout >3min -> close 408 (whatsmeow only resets itself with auto reconnect on)
//	events.TemporaryBan           -> close 402
//	events.ClientOutdated         -> close 405
//	events.ConnectFailure         -> close <reason code>
//	events.ManualLoginReconnect   -> close 515 (whatsmeow restarts after pairing itself, so this stays unused)
//
// whatsmeow's own auto reconnect is off: the runtime reconnects with its
// backoff by creating a new socket on the same store.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/zakiego/palui/apps/server/internal/logx"
)

// Browser identity: Baileys' Browsers.ubuntu("Chrome") was
// ["Ubuntu", "Chrome", "22.04.4"]; the pairing code names "Chrome (Ubuntu)".
const pairDisplayName = "Chrome (Ubuntu)"

var deviceOnce sync.Once

func setDeviceProps() {
	deviceOnce.Do(func() {
		store.SetOSInfo("Ubuntu", [3]uint32{22, 4, 4})
		store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
		// No full history sync (Baileys syncFullHistory: false).
		store.DeviceProps.RequireFullSync = proto.Bool(false)
	})
}

type meowLogger struct {
	log *logx.Logger
	mod string
}

func (l meowLogger) Warnf(msg string, args ...any) {
	l.log.Warn("whatsmeow: "+fmt.Sprintf(msg, args...), map[string]any{"module": l.mod})
}
func (l meowLogger) Errorf(msg string, args ...any) {
	l.log.Error("whatsmeow: "+fmt.Sprintf(msg, args...), map[string]any{"module": l.mod})
}
func (l meowLogger) Infof(string, ...any)  {}
func (l meowLogger) Debugf(string, ...any) {}
func (l meowLogger) Sub(module string) waLog.Logger {
	if l.mod != "" {
		module = l.mod + "/" + module
	}
	return meowLogger{log: l.log, mod: module}
}

type meowSocket struct {
	cli       *whatsmeow.Client
	container *sqlstore.Container
	sqldb     *sql.DB
	ctx       context.Context
	cancel    context.CancelFunc
	handlerID uint32

	mu       sync.Mutex
	seq      int
	updates  map[int]func(Update)
	upserts  map[int]func(Upsert)
	groups   map[int]func(string)
	pending  []any
	ended    bool
	closedAt bool
}

// NewMeowSocket opens the whatsmeow store at storePath (created if missing)
// and returns a socket that connects on Open.
func NewMeowSocket(ctx context.Context, storePath string, log *logx.Logger) (Socket, error) {
	setDeviceProps()
	if log == nil {
		log = logx.Silent
	}
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		return nil, err
	}
	sqldb, err := sql.Open("sqlite", storeDSN(storePath))
	if err != nil {
		return nil, err
	}
	wlog := meowLogger{log: log}
	container := sqlstore.NewWithDB(sqldb, "sqlite", wlog.Sub("Store"))
	if err := container.Upgrade(ctx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("whatsapp store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("whatsapp store: %w", err)
	}
	cli := whatsmeow.NewClient(device, wlog.Sub("Client"))
	cli.EnableAutoReconnect = false
	cli.InitialAutoReconnect = false
	// Baileys' syncFullHistory: false; history blobs are never downloaded.
	cli.ManualHistorySyncDownload = true
	sctx, cancel := context.WithCancel(context.Background())
	s := &meowSocket{cli: cli, container: container, sqldb: sqldb, ctx: sctx, cancel: cancel, updates: map[int]func(Update){}, upserts: map[int]func(Upsert){}, groups: map[int]func(string){}}
	s.handlerID = cli.AddEventHandler(s.handle)
	return s, nil
}

func (s *meowSocket) OnUpdate(fn func(Update)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := s.seq
	s.updates[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.updates, id)
	}
}

func (s *meowSocket) OnUpsert(fn func(Upsert)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := s.seq
	s.upserts[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.upserts, id)
	}
}

func (s *meowSocket) OnGroupChange(fn func(string)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := s.seq
	s.groups[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.groups, id)
	}
}

func (s *meowSocket) emitGroupChange(jid string) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	fns := make([]func(string), 0, len(s.groups))
	for _, fn := range s.groups {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(jid)
	}
}

func (s *meowSocket) emitUpdate(u Update) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	fns := make([]func(Update), 0, len(s.updates))
	for _, fn := range s.updates {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(u)
	}
}

func (s *meowSocket) emitClose(code int) { s.emitUpdate(Update{Connection: "close", StatusCode: code}) }

func (s *meowSocket) emitUpsert(u Upsert) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	fns := make([]func(Upsert), 0, len(s.upserts))
	for _, fn := range s.upserts {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(u)
	}
}

func (s *meowSocket) handle(evt any) {
	switch e := evt.(type) {
	case *events.Connected:
		s.emitUpdate(Update{Connection: "open"})
	case *events.StreamReplaced:
		s.emitClose(CloseReplaced)
	case *events.LoggedOut:
		s.emitClose(CloseLoggedOut)
	case *events.Disconnected:
		s.emitClose(CloseConnectionLost)
	case *events.TemporaryBan:
		s.emitClose(CloseTempBanned)
	case *events.ClientOutdated:
		s.emitClose(405)
	case *events.ConnectFailure:
		s.emitClose(int(e.Reason))
	case *events.ManualLoginReconnect:
		s.emitClose(CloseRestartRequired)
	case *events.KeepAliveTimeout:
		if time.Since(e.LastSuccess) > whatsmeow.KeepAliveMaxFailTime {
			go s.cli.Disconnect()
			s.emitClose(CloseTimedOut)
		}
	case *events.Message:
		s.emitUpsert(Upsert{Type: "notify", Messages: []*Message{fromEvent(e)}})
	case *events.GroupInfo:
		// Any change, not only joins and leaves: cheap and safe.
		s.emitGroupChange(e.JID.String())
	case *events.JoinedGroup:
		s.emitGroupChange(e.JID.String())
	}
}

func fromEvent(e *events.Message) *Message {
	info := e.Info
	m := &Message{
		Key:       Key{RemoteJID: info.Chat.String(), FromMe: info.IsFromMe, ID: info.ID},
		Message:   e.RawMessage,
		PushName:  info.PushName,
		Timestamp: info.Timestamp.Unix(),
		Raw:       e,
	}
	if m.Message == nil {
		m.Message = e.Message
	}
	if info.IsGroup {
		m.Key.Participant = info.Sender.String()
		if !info.SenderAlt.IsEmpty() {
			m.Key.ParticipantAlt = info.SenderAlt.String()
		}
	} else if !info.SenderAlt.IsEmpty() && !info.IsFromMe {
		m.Key.RemoteJIDAlt = info.SenderAlt.String()
	}
	return m
}

func (s *meowSocket) Open() {
	go func() {
		if s.cli.Store.ID == nil {
			qr, err := s.cli.GetQRChannel(s.ctx)
			if err != nil {
				s.emitClose(CloseConnectionLost)
				return
			}
			go func() {
				for item := range qr {
					switch item.Event {
					case whatsmeow.QRChannelEventCode:
						s.emitUpdate(Update{QR: item.Code})
					case whatsmeow.QRChannelSuccess.Event:
					default:
						s.emitClose(CloseTimedOut)
					}
				}
			}()
		}
		if err := s.cli.ConnectContext(s.ctx); err != nil {
			s.emitClose(CloseConnectionLost)
		}
	}()
}

func (s *meowSocket) User() *User {
	st := s.cli.Store
	if st.ID == nil {
		return nil
	}
	u := &User{ID: st.ID.String(), Name: st.PushName}
	if !st.LID.IsEmpty() {
		u.LID = st.LID.String()
	}
	return u
}

func (s *meowSocket) Linked() bool { return s.cli.Store.ID != nil }

func (s *meowSocket) SendMessage(ctx context.Context, jid string, content Content, quoted *Message) (*Message, error) {
	to, err := types.ParseJID(jid)
	if err != nil {
		return nil, err
	}
	var msg *waE2E.Message
	switch {
	case content.React != nil:
		k := content.React.Key
		chat, err := types.ParseJID(k.RemoteJID)
		if err != nil {
			return nil, err
		}
		sender := chat
		if k.Participant != "" {
			if sender, err = types.ParseJID(k.Participant); err != nil {
				return nil, err
			}
		}
		if k.FromMe && s.cli.Store.ID != nil {
			sender = s.cli.Store.ID.ToNonAD()
		}
		msg = s.cli.BuildReaction(chat, sender, k.ID, content.React.Text)
	case quoted != nil:
		participant := quoted.Key.Participant
		if participant == "" {
			participant = quoted.Key.RemoteJID
		}
		if quoted.Key.FromMe && s.cli.Store.ID != nil {
			participant = s.cli.Store.ID.ToNonAD().String()
		}
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String(content.Text),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:      proto.String(quoted.Key.ID),
				Participant:   proto.String(BareJID(participant)),
				QuotedMessage: quoted.Message,
			},
		}}
	default:
		msg = &waE2E.Message{Conversation: proto.String(content.Text)}
	}
	resp, err := s.cli.SendMessage(ctx, to, msg)
	if err != nil {
		return nil, err
	}
	return &Message{Key: Key{RemoteJID: jid, FromMe: true, ID: resp.ID}, Message: msg, Timestamp: resp.Timestamp.Unix()}, nil
}

func (s *meowSocket) RequestPairingCode(ctx context.Context, phone string) (string, error) {
	return s.cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, pairDisplayName)
}

func (s *meowSocket) SendPresenceUpdate(ctx context.Context, state, jid string) error {
	to, err := types.ParseJID(jid)
	if err != nil {
		return err
	}
	p := types.ChatPresencePaused
	if state == "composing" {
		p = types.ChatPresenceComposing
	}
	return s.cli.SendChatPresence(ctx, to, p, types.ChatPresenceMediaText)
}

func (s *meowSocket) PresenceSubscribe(ctx context.Context, jid string) error {
	to, err := types.ParseJID(jid)
	if err != nil {
		return err
	}
	return s.cli.SubscribePresence(ctx, to)
}

func (s *meowSocket) GroupSubject(ctx context.Context, jid string) (string, error) {
	g, err := types.ParseJID(jid)
	if err != nil {
		return "", err
	}
	info, err := s.cli.GetGroupInfo(ctx, g)
	if err != nil {
		return "", err
	}
	return info.Name, nil
}

// GroupParticipants is one GetGroupInfo request. A list shorter than the
// group's participant count is an unknown list, so it's an error.
func (s *meowSocket) GroupParticipants(ctx context.Context, jid string) ([]GroupMember, error) {
	g, err := types.ParseJID(jid)
	if err != nil {
		return nil, err
	}
	info, err := s.cli.GetGroupInfo(ctx, g)
	if err != nil {
		return nil, err
	}
	if info.ParticipantCount > 0 && info.ParticipantCount != len(info.Participants) {
		return nil, fmt.Errorf("group %s lists %d of %d participants", jid, len(info.Participants), info.ParticipantCount)
	}
	out := make([]GroupMember, 0, len(info.Participants))
	for _, part := range info.Participants {
		id := part.JID.ToNonAD()
		m := GroupMember{ID: id.String()}
		for _, other := range []types.JID{part.PhoneNumber, part.LID} {
			if !other.IsEmpty() && other.ToNonAD() != id {
				m.Alt = other.ToNonAD().String()
				break
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *meowSocket) PNForLID(ctx context.Context, lid string) (string, error) {
	j, err := types.ParseJID(lid)
	if err != nil {
		return "", err
	}
	pn, err := s.cli.Store.LIDs.GetPNForLID(ctx, j.ToNonAD())
	if err != nil || pn.IsEmpty() {
		return "", err
	}
	return pn.String(), nil
}

func (s *meowSocket) LIDForPN(ctx context.Context, pn string) (string, error) {
	j, err := types.ParseJID(pn)
	if err != nil {
		return "", err
	}
	lid, err := s.cli.Store.LIDs.GetLIDForPN(ctx, j.ToNonAD())
	if err != nil || lid.IsEmpty() {
		return "", err
	}
	return lid.String(), nil
}

// Download fetches the media of an image, sticker, audio or document message.
func (s *meowSocket) Download(ctx context.Context, m *Message) ([]byte, error) {
	d := downloadable(Unwrap(m.Message))
	if d == nil {
		return nil, errors.New("no media")
	}
	return s.cli.Download(ctx, d)
}

// downloadable: the part of a message whatsmeow can download, nil when there
// is none. It covers every kind MediaOf hands to deliver.
func downloadable(c *waE2E.Message) whatsmeow.DownloadableMessage {
	switch {
	case c == nil:
		return nil
	case c.ImageMessage != nil:
		return c.ImageMessage
	case c.StickerMessage != nil:
		return c.StickerMessage
	case c.AudioMessage != nil:
		return c.AudioMessage
	case c.DocumentMessage != nil:
		return c.DocumentMessage
	}
	return nil
}

func (s *meowSocket) Logout(ctx context.Context) error { return s.cli.Logout(ctx) }

func (s *meowSocket) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.mu.Unlock()
	s.cancel()
	s.cli.RemoveEventHandler(s.handlerID)
	s.cli.Disconnect()
	_ = s.container.Close()
}

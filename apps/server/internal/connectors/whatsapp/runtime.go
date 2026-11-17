package whatsapp

// WhatsApp channel runtime (whatsapp/runtime.ts, PLAN 5.1), as its own linked
// device on whatsmeow.
//
// Lessons carried over from utusan:
//   - One socket per session store, guarded by a lock file (lock.go).
//   - One close per socket starts at most one replacement, or sockets multiply
//     and evict each other.
//   - Replaced (440) means another session took over: pause with that reason,
//     never fight.
//   - Logged out (401) means the link is gone: error "unlinked", a human has
//     to pair again.
//   - An interrupted pairing leaves a store that blocks the next pairing: clear it.
//   - Pairing codes push a notification to the phone, so a code is requested
//     once per pairing attempt, never on every reconnect.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/channelutil"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	TextLimit         = 4096
	DocumentMaxBytes  = 1024 * 1024
	MediaMaxBytes     = 16 * 1024 * 1024
	ThinkingReaction  = "⏳"
	ExpiresHint       = "expires in about 2 min"
	recentMax         = 200
	groupTitleTTL     = 3600_000
	groupMembersTTL   = 10 * 60_000
	defaultLeadMs     = 1500
	defaultRefreshMs  = 8000
	defaultTypingMax  = 10 * 60_000
	defaultBackoffMs  = 1000
	defaultBackoffMax = 60_000
)

// Reasons shown on the connector.
const (
	ReasonReplaced          = "Another session took over this WhatsApp link"
	ReasonUnlinked          = "unlinked"
	ReasonPairingExpired    = "Pairing expired. Press Pair again in the dashboard."
	ReasonWaitingForPairing = "Waiting for pairing"
	ReasonNotConnected      = "WhatsApp is not connected"
)

// Options are the TS WhatsAppDeps. Zero values are the defaults.
type Options struct {
	// nil: whatsmeow (NewMeowSocket).
	CreateSocket SocketFactory
	Lock         LockOptions
	BackoffMs    int64
	BackoffMaxMs int64
	// How often "typing..." is refreshed.
	PresenceRefreshMs int64
	// How long "typing..." waits after the thinking reaction. WhatsApp clears
	// the typing indicator when a message (a reaction too) lands, and presence
	// travels faster than messages, so typing sent right away flashes and
	// vanishes before the reaction.
	ReactionLeadMs int64
	TypingMaxMs    int64
	Timers         channelutil.Timers
	Now            func() int64
}

func (o Options) withDefaults(log *logx.Logger) Options {
	if o.CreateSocket == nil {
		o.CreateSocket = func(ctx context.Context, path string) (Socket, error) { return NewMeowSocket(ctx, path, log) }
	}
	if o.BackoffMs == 0 {
		o.BackoffMs = defaultBackoffMs
	}
	if o.BackoffMaxMs == 0 {
		o.BackoffMaxMs = defaultBackoffMax
	}
	if o.PresenceRefreshMs == 0 {
		o.PresenceRefreshMs = defaultRefreshMs
	}
	if o.ReactionLeadMs == 0 {
		o.ReactionLeadMs = defaultLeadMs
	}
	if o.TypingMaxMs == 0 {
		o.TypingMaxMs = defaultTypingMax
	}
	if o.Timers == nil {
		o.Timers = channelutil.RealTimers{}
	}
	if o.Now == nil {
		o.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if o.Lock.Timers == nil {
		o.Lock.Timers = o.Timers
	}
	return o
}

// BackoffDelay is min(base * 2^attempt, max).
func BackoffDelay(attempt int, baseMs, maxMs int64) int64 {
	d := float64(baseMs) * math.Pow(2, float64(attempt))
	if d > float64(maxMs) {
		return maxMs
	}
	return int64(d)
}

// Live is what the dashboard reads (WhatsAppLive).
type Live struct {
	// idle, connecting, pairing, open or closed.
	Connection   string
	Linked       bool
	LinkedNumber *string
	QR           *string
	QRSvg        *string
	PairingCode  *string
	PairPhone    *string
	ExpiresHint  *string
	Reason       *string
}

func (l Live) Object() *jsonx.Object {
	s := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	return obj("connection", l.Connection, "linked", l.Linked, "linkedNumber", s(l.LinkedNumber), "qr", s(l.QR), "qrSvg", s(l.QRSvg),
		"pairingCode", s(l.PairingCode), "pairPhone", s(l.PairPhone), "expiresHint", s(l.ExpiresHint), "reason", s(l.Reason))
}

func ptr(s string) *string { return &s }

func configString(c *jsonx.Object, key string) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(key)
	s, _ := v.(string)
	return s
}

// New is createWhatsAppRuntime(overrides).
func New(o Options) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			if configString(c.Config, "ownerPhone") == "" {
				return connectors.TestResult{OK: false, Detail: "Enter the owner's phone number first."}, nil
			}
			if InspectSession(StorePathFor(c.DataDir, c.ID)) == SessionLinked {
				detail := "Linked session found"
				if n := configString(c.Config, "linkedNumber"); n != "" {
					detail = "Linked as +" + n
				}
				return connectors.TestResult{OK: true, Detail: detail, Data: obj("linked", true)}, nil
			}
			return connectors.TestResult{OK: true, Detail: "Not paired yet. Start pairing to link the number.", Data: obj("linked", false)}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			return start(c, o)
		},
	}
}

// Runtime is the WhatsApp connector on whatsmeow.
var Runtime = New(Options{})

// RemoveConnectorFiles is the supervisor's OnRemoved (apps/runtime main.ts
// removeConnectorFiles): a removed WhatsApp connector's session store goes.
func RemoveConnectorFiles(dataDir string) func(row db.Connector, unlinked bool) error {
	return func(row db.Connector, _ bool) error {
		if row.Type == "whatsapp" {
			return WipeSession(StorePathFor(dataDir, row.ID))
		}
		return nil
	}
}

type thinking struct {
	key     *Key
	lead    any
	refresh any
	cap     any
}

type titleEntry struct {
	title string
	at    int64
}

type membersEntry struct {
	list []connectors.Participant
	at   int64
}

var _ connectors.GroupLister = (*wa)(nil)

type wa struct {
	c         connectors.Context
	o         Options
	log       *logx.Logger
	storePath string
	lock      *Lock

	mu              sync.Mutex
	sock            Socket
	stopped         bool
	halted          bool
	attempt         int
	reconnectTimer  any
	wantPairingCode bool
	detach          func()
	generation      int
	loggedOut       bool
	live            Live

	// Recent messages by id, for quoting replies (inbound and our own).
	recent      map[string]*Message
	recentOrder []string
	lastInbound map[string]Key
	thinking    map[string]*thinking
	groupTitles map[string]titleEntry
	// Group participant lists (PLAN 19.4), dropped on any group change, on
	// reconnect and when a sender outside the list writes.
	groupMembers map[string]membersEntry
	// groupGen counts the drops per group and groupEpoch the full clears, also
	// when nothing was cached, so a fetch that overlapped one isn't stored.
	groupGen   map[string]uint64
	groupEpoch uint64

	queue chan delivery
	quit  chan struct{}
}

type delivery struct {
	s Socket
	m *Message
}

func start(c connectors.Context, o Options) (*connectors.RunningConnector, error) {
	log := c.Log
	if log == nil {
		log = logx.Silent
	}
	log = log.Child(map[string]any{"connector_id": c.ID})
	o = o.withDefaults(log)

	lock, err := AcquireLock(LockPathFor(c.DataDir, c.ID), o.Lock)
	if err != nil {
		var locked *AuthLockedError
		detail := "Could not lock the WhatsApp session: " + err.Error()
		if errors.As(err, &locked) {
			detail = locked.Error()
		}
		c.SetStatus("error", &detail)
		return nil, err
	}

	// Keep the owner identity in step with the configured number. Same number
	// keeps the learned LID; a new number replaces the identity.
	if phone := configString(c.Config, "ownerPhone"); phone != "" {
		want := owner.PhoneToJid(phone)
		cur, ok, err := owner.GetOwner(c.DB, "whatsapp")
		if err == nil && (!ok || cur.ExternalID != want) {
			if _, err := owner.SetWhatsAppOwner(c.DB, phone, o.Now()); err != nil {
				log.Warn("could not save the WhatsApp owner", map[string]any{"err": err.Error()})
			}
		}
	}

	w := &wa{
		c: c, o: o, log: log, lock: lock,
		storePath:       StorePathFor(c.DataDir, c.ID),
		wantPairingCode: true,
		recent:          map[string]*Message{},
		lastInbound:     map[string]Key{},
		thinking:        map[string]*thinking{},
		groupTitles:     map[string]titleEntry{},
		groupMembers:    map[string]membersEntry{},
		groupGen:        map[string]uint64{},
		queue:           make(chan delivery, 256),
		quit:            make(chan struct{}),
	}
	w.live = Live{Connection: "idle"}
	if n := configString(c.Config, "linkedNumber"); n != "" {
		w.live.LinkedNumber = ptr(n)
	}
	if p := configString(c.Config, "pairPhone"); p != "" {
		w.live.PairPhone = ptr(PairingDigits(p))
	}
	go w.deliverLoop()
	if c.Ctx != nil {
		go func() {
			select {
			case <-c.Ctx.Done():
				w.stop()
			case <-w.quit:
			}
		}()
	}

	c.SetStatus("connecting", nil)
	w.connect()

	return &connectors.RunningConnector{
		Stop:    func(context.Context) error { w.stop(); return nil },
		Channel: w,
		Live: func() *jsonx.Object {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.live.Object()
		},
		Action: w.action,
	}, nil
}

func (w *wa) ctx() context.Context {
	if w.c.Ctx != nil {
		return w.c.Ctx
	}
	return context.Background()
}

func (w *wa) remember(m *Message) {
	if m == nil || m.Key.ID == "" {
		return
	}
	id := m.Key.ID
	if _, ok := w.recent[id]; ok {
		for i, x := range w.recentOrder {
			if x == id {
				w.recentOrder = append(w.recentOrder[:i], w.recentOrder[i+1:]...)
				break
			}
		}
	}
	w.recent[id] = m
	w.recentOrder = append(w.recentOrder, id)
	if len(w.recentOrder) > recentMax {
		delete(w.recent, w.recentOrder[0])
		w.recentOrder = w.recentOrder[1:]
	}
}

func (w *wa) clearPairingLocked() {
	w.live.QR, w.live.QRSvg, w.live.PairingCode, w.live.ExpiresHint = nil, nil, nil, nil
}

// endSocketLocked detaches and forgets the socket; the caller ends it after
// unlocking.
func (w *wa) endSocketLocked() Socket {
	if w.detach != nil {
		w.detach()
		w.detach = nil
	}
	s := w.sock
	w.sock = nil
	return s
}

func endQuietly(s Socket) {
	if s == nil {
		return
	}
	defer func() { _ = recover() }()
	s.End()
}

func (w *wa) status(status string, detail *string) { w.c.SetStatus(status, detail) }

// scheduleReconnectLocked arms the backoff reconnect and returns the status
// detail; the caller reports it after unlocking.
func (w *wa) scheduleReconnectLocked(code int) string {
	delay := w.o.BackoffDelayFor(w.attempt)
	w.attempt++
	w.live.Connection = "connecting"
	c := "unknown"
	if code != 0 {
		c = strconv.Itoa(code)
	}
	detail := fmt.Sprintf("Reconnecting in %ds (close %s)", int64(math.Round(float64(delay)/1000)), c)
	w.reconnectTimer = w.o.Timers.SetTimeout(func() {
		w.mu.Lock()
		w.reconnectTimer = nil
		w.mu.Unlock()
		go w.connect()
	}, delay)
	return detail
}

// BackoffDelayFor is the reconnect delay for an attempt.
func (o Options) BackoffDelayFor(attempt int) int64 {
	return BackoffDelay(attempt, o.BackoffMs, o.BackoffMaxMs)
}

func (w *wa) onClose(u Update) {
	w.mu.Lock()
	old := w.endSocketLocked()
	w.live.Connection = "closed"
	if w.stopped || w.halted {
		w.mu.Unlock()
		endQuietly(old)
		return
	}
	code := u.StatusCode
	linked := old != nil && old.Linked()
	switch {
	case code == CloseReplaced:
		w.halted = true
		w.clearPairingLocked()
		w.live.Reason = ptr(ReasonReplaced)
		w.mu.Unlock()
		endQuietly(old)
		w.log.Warn("whatsapp connection replaced, not reconnecting", nil)
		w.c.Event("wa_replaced", ReasonReplaced, "warn", nil)
		w.status("paused", ptr(ReasonReplaced))
	case code == CloseLoggedOut:
		w.halted = true
		w.loggedOut = true
		w.clearPairingLocked()
		w.live.Linked = false
		w.live.Reason = ptr(ReasonUnlinked)
		w.mu.Unlock()
		endQuietly(old)
		w.log.Warn("whatsapp logged out", nil)
		w.c.Event("wa_unlinked", "WhatsApp device was unlinked", "warn", nil)
		w.status("error", ptr(ReasonUnlinked))
	case !linked && code != CloseRestartRequired:
		// The pairing window closed without a link. Don't loop: every new code
		// pushes a notification to the phone.
		w.halted = true
		w.clearPairingLocked()
		w.live.Reason = ptr(ReasonPairingExpired)
		w.mu.Unlock()
		endQuietly(old)
		w.status("error", ptr(ReasonPairingExpired))
	default:
		detail := w.scheduleReconnectLocked(code)
		w.mu.Unlock()
		endQuietly(old)
		w.status("connecting", &detail)
	}
}

func (w *wa) onUpdate(s Socket, u Update) {
	w.mu.Lock()
	if s != w.sock {
		w.mu.Unlock()
		return
	}
	if u.QR != "" {
		qr := u.QR
		first := w.live.Connection != "pairing"
		w.live.Connection = "pairing"
		w.live.QR = ptr(qr)
		w.live.ExpiresHint = ptr(ExpiresHint)
		if svg, err := channelutil.QRSVG(qr); err != nil {
			w.log.Debug("qr render failed", map[string]any{"err": err.Error()})
		} else {
			w.live.QRSvg = ptr(svg)
		}
		request := w.wantPairingCode && w.live.PairPhone != nil && !s.Linked()
		var digits string
		if request {
			w.wantPairingCode = false
			digits = *w.live.PairPhone
		}
		w.mu.Unlock()
		if first {
			w.status("connecting", ptr(ReasonWaitingForPairing))
		}
		if request {
			go w.requestCode(s, digits)
		}
		w.mu.Lock()
		if s != w.sock {
			w.mu.Unlock()
			return
		}
	}
	switch u.Connection {
	case "open":
		// Changes made while offline may never be reported.
		w.groupMembers = map[string]membersEntry{}
		w.groupEpoch++
		w.attempt = 0
		w.clearPairingLocked()
		w.live.Connection = "open"
		w.live.Linked = true
		w.live.Reason = nil
		number := ""
		if user := s.User(); user != nil {
			number = JIDDigits(user.ID)
		}
		patch := false
		if number != "" && (w.live.LinkedNumber == nil || *w.live.LinkedNumber != number) {
			w.live.LinkedNumber = ptr(number)
			patch = true
		}
		w.mu.Unlock()
		if patch {
			w.c.PatchConfig(obj("linkedNumber", number))
		}
		w.log.Info("whatsapp connected", map[string]any{"linked": number})
		w.status("on", nil)
	case "close":
		w.mu.Unlock()
		w.onClose(u)
	default:
		w.mu.Unlock()
	}
}

func (w *wa) requestCode(s Socket, digits string) {
	code, err := s.RequestPairingCode(w.ctx(), digits)
	w.mu.Lock()
	if err != nil {
		w.live.Reason = ptr("Could not request a pairing code: " + err.Error())
		w.mu.Unlock()
		w.log.Warn("could not request a pairing code", map[string]any{"err": err.Error()})
		return
	}
	if s != w.sock {
		w.mu.Unlock()
		return
	}
	w.live.PairingCode = ptr(FormatPairingCode(code))
	w.live.ExpiresHint = ptr(ExpiresHint)
	w.mu.Unlock()
	w.c.Event("wa_pairing_code", "Pairing code requested for +"+digits, "info", nil)
}

func (w *wa) superseded(mine int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped || w.halted || mine != w.generation
}

func (w *wa) connect() {
	w.mu.Lock()
	if w.stopped || w.halted {
		w.mu.Unlock()
		return
	}
	w.generation++
	mine := w.generation
	old := w.endSocketLocked()
	w.live.Connection = "connecting"
	w.mu.Unlock()
	endQuietly(old)

	if ClearIncompleteSession(w.storePath) == SessionIncomplete {
		w.log.Info("cleared an unfinished WhatsApp pairing", nil)
	}
	if w.superseded(mine) {
		return
	}
	s, err := w.o.CreateSocket(w.ctx(), w.storePath)
	if err != nil {
		w.log.Error("whatsapp socket creation failed", map[string]any{"err": err.Error()})
		w.mu.Lock()
		detail := ""
		if !(w.stopped || w.halted || mine != w.generation) {
			detail = w.scheduleReconnectLocked(0)
		}
		w.mu.Unlock()
		if detail != "" {
			w.status("connecting", &detail)
		}
		return
	}
	w.mu.Lock()
	if w.stopped || w.halted || mine != w.generation {
		w.mu.Unlock()
		endQuietly(s)
		return
	}
	w.sock = s
	w.live.Linked = s.Linked()
	var closedMu sync.Mutex
	closed := false
	offUpdate := s.OnUpdate(func(u Update) {
		if u.Connection == "close" {
			closedMu.Lock()
			if closed {
				closedMu.Unlock()
				return
			}
			closed = true
			closedMu.Unlock()
		}
		w.onUpdate(s, u)
	})
	offUpsert := s.OnUpsert(func(up Upsert) {
		w.mu.Lock()
		current := s == w.sock
		w.mu.Unlock()
		if up.Type != "notify" || !current {
			return
		}
		for _, m := range up.Messages {
			select {
			case w.queue <- delivery{s, m}:
			case <-w.quit:
				return
			}
		}
	})
	offGroup := s.OnGroupChange(func(jid string) {
		w.mu.Lock()
		w.dropGroupLocked(jid)
		w.mu.Unlock()
	})
	w.detach = func() {
		offUpdate()
		offUpsert()
		offGroup()
	}
	w.mu.Unlock()
	s.Open()
}

// deliverLoop hands inbound messages to the router one at a time, in order.
func (w *wa) deliverLoop() {
	for {
		select {
		case d := <-w.queue:
			func() {
				defer func() {
					if p := recover(); p != nil {
						w.log.Error("whatsapp inbound failed", map[string]any{"err": fmt.Sprint(p)})
					}
				}()
				if err := w.deliver(d.s, d.m); err != nil {
					w.log.Error("whatsapp inbound failed", map[string]any{"err": err.Error()})
				}
			}()
		case <-w.quit:
			return
		}
	}
}

// groupTitle: group subjects, so the Chats page shows a name (re-read after an hour).
func (w *wa) groupTitle(s Socket, jid string) string {
	now := w.o.Now()
	w.mu.Lock()
	hit, ok := w.groupTitles[jid]
	w.mu.Unlock()
	if ok && now-hit.at < groupTitleTTL {
		return hit.title
	}
	subject, err := s.GroupSubject(w.ctx(), jid)
	if err != nil {
		w.log.Debug("group metadata lookup failed", map[string]any{"err": err.Error()})
		return hit.title
	}
	title := strx.Trim(subject)
	if title != "" {
		w.mu.Lock()
		w.groupTitles[jid] = titleEntry{title, now}
		w.mu.Unlock()
	}
	return title
}

func overLimitNote(info *MediaInfo, max int) string {
	name := ""
	if info.Filename != "" {
		name = " " + info.Filename
	}
	return fmt.Sprintf("[%s%s is over the %d MB limit and was not read]", info.Kind, name, max/1024/1024)
}

func (w *wa) deliver(s Socket, m *Message) error {
	key := m.Key
	jid := key.RemoteJID
	if jid == "" || key.FromMe {
		return nil
	}
	group := IsGroupChat(jid)
	if !group && !IsDirectChat(jid) {
		return nil
	}
	// In a group the chat is the group and the sender is the participant.
	sender := jid
	senderAlt := key.RemoteJIDAlt
	if group {
		sender, senderAlt = key.Participant, key.ParticipantAlt
	}
	if sender == "" {
		return nil
	}

	content := Unwrap(m.Message)
	alt := ""
	if senderAlt != "" {
		alt = BareJID(senderAlt)
	} else {
		var found string
		var err error
		if IsLID(sender) {
			found, err = s.PNForLID(w.ctx(), sender)
		} else {
			found, err = s.LIDForPN(w.ctx(), sender)
		}
		if err != nil {
			w.log.Debug("lid mapping lookup failed", map[string]any{"err": err.Error()})
		} else if found != "" {
			alt = BareJID(found)
		}
	}

	if group {
		w.mu.Lock()
		// No entry also drops: a fetch in flight may predate this sender.
		if e, ok := w.groupMembers[jid]; !ok || !inParticipants(e.list, BareJID(sender), alt) {
			w.dropGroupLocked(jid)
		}
		w.mu.Unlock()
	}

	text := TextOf(content)
	info := MediaOf(content)
	var media []connectors.InboundMedia
	note := ""
	if info != nil {
		isOwner := w.c.Services.IsOwner("whatsapp", BareJID(sender), alt)
		max := MediaMaxBytes
		if info.Kind == "document" {
			max = DocumentMaxBytes
		}
		switch {
		case !isOwner:
			// Strangers are dropped by the router; don't download their files.
		case info.Bytes > int64(max):
			note = overLimitNote(info, max)
		default:
			data, err := s.Download(w.ctx(), m)
			switch {
			case err != nil:
				w.log.Warn("whatsapp media download failed", map[string]any{"err": err.Error()})
			case len(data) > max:
				note = overLimitNote(info, max)
			default:
				media = append(media, connectors.InboundMedia{Kind: info.Kind, MimeType: info.MimeType, Data: base64.StdEncoding.EncodeToString(data), Filename: info.Filename, DurationSec: info.DurationSec})
			}
		}
	}

	var parts []string
	for _, p := range []string{text, note} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	body := strings.Join(parts, "\n")
	if body == "" && len(media) == 0 {
		return nil
	}

	w.mu.Lock()
	w.remember(m)
	w.lastInbound[jid] = key
	w.mu.Unlock()

	msg := connectors.InboundMessage{
		Channel:          "whatsapp",
		ConnectorID:      w.c.ID,
		ChatExternalID:   jid,
		ChatKind:         "dm",
		SenderExternalID: BareJID(sender),
		Text:             body,
		At:               TimestampMs(m.Timestamp, w.o.Now()),
	}
	if alt != "" && alt != BareJID(sender) {
		msg.SenderAltID = alt
	}
	msg.SenderName = m.PushName
	var self []string
	if u := s.User(); u != nil {
		self = []string{u.ID, u.LID}
	}
	if group {
		msg.ChatKind = "group"
		msg.Addressed = IsAddressed(content, self...)
		msg.ChatTitle = w.groupTitle(s, jid)
	} else {
		msg.ChatTitle = m.PushName
	}
	msg.ChannelMsgID = key.ID
	if q := QuoteOf(content); q != nil {
		msg.ReplyToChannelMsgID = q.ID
		msg.Quoted = &connectors.InboundQuote{From: QuoteAuthor(q.Participant, self, []string{sender, alt}, !group), Text: q.Text, Media: q.Media}
	}
	if len(media) > 0 {
		msg.Media = media
	}
	return w.c.Services.Inbound(w.ctx(), msg)
}

// --- channel handle ------------------------------------------------------------------

func (w *wa) Channel() string { return "whatsapp" }

func (w *wa) Reachable() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.stopped && w.live.Connection == "open"
}

func inParticipants(list []connectors.Participant, ids ...string) bool {
	for _, p := range list {
		for _, id := range ids {
			if id != "" && (p.ExternalID == id || p.AltID == id) {
				return true
			}
		}
	}
	return false
}

// GroupParticipants is who is in a group right now, without this account
// (connectors.GroupLister). The list is cached for 10 minutes and never served
// while disconnected, since change events can't arrive then.
func (w *wa) GroupParticipants(ctx context.Context, jid string) ([]connectors.Participant, error) {
	if !IsGroupChat(jid) {
		return nil, fmt.Errorf("%s is not a group", jid)
	}
	s := w.openSocket()
	if s == nil {
		return nil, errors.New(ReasonNotConnected)
	}
	now := w.o.Now()
	w.mu.Lock()
	hit, ok := w.groupMembers[jid]
	gen, epoch := w.groupGen[jid], w.groupEpoch
	w.mu.Unlock()
	if ok && now-hit.at < groupMembersTTL {
		return slices.Clone(hit.list), nil
	}
	got, err := s.GroupParticipants(ctx, jid)
	if err != nil {
		return nil, err
	}
	user := s.User()
	if user == nil {
		return nil, errors.New("this WhatsApp account is not known yet")
	}
	self := map[string]bool{}
	for _, id := range []string{user.ID, user.LID} {
		if id != "" {
			self[BareJID(id)] = true
		}
	}
	list := make([]connectors.Participant, 0, len(got))
	foundSelf := false
	for _, m := range got {
		id, alt := BareJID(m.ID), ""
		if m.Alt != "" {
			alt = BareJID(m.Alt)
		} else {
			var found string
			var err error
			if IsLID(id) {
				found, err = s.PNForLID(ctx, id)
			} else {
				found, err = s.LIDForPN(ctx, id)
			}
			if err == nil && found != "" {
				alt = BareJID(found)
			}
		}
		if alt == id {
			alt = ""
		}
		if self[id] || (alt != "" && self[alt]) {
			foundSelf = true
			continue
		}
		list = append(list, connectors.Participant{ExternalID: id, AltID: alt})
	}
	if !foundSelf {
		return nil, fmt.Errorf("this WhatsApp account is not in %s", jid)
	}
	if err := ctx.Err(); err != nil {
		// The id lookups may have failed on the deadline: don't keep a list
		// with missing alt ids.
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.groupGen[jid] != gen || w.groupEpoch != epoch {
		return nil, fmt.Errorf("%s changed while its participants were fetched", jid)
	}
	w.groupMembers[jid] = membersEntry{list: list, at: w.o.Now()}
	return slices.Clone(list), nil
}

// dropGroupLocked forgets a group's participants and fails any fetch of them
// already in flight. Callers hold w.mu.
func (w *wa) dropGroupLocked(jid string) {
	delete(w.groupMembers, jid)
	w.groupGen[jid]++
}

func (w *wa) openSocket() Socket {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sock == nil || w.live.Connection != "open" {
		return nil
	}
	return w.sock
}

// Send converts the markdown, adds the actions hint, splits at 4096 and quotes
// the reply target on the first chunk when it is still in the recent list.
func (w *wa) Send(ctx context.Context, jid string, payload db.OutboxPayload) (connectors.SendResult, error) {
	s := w.openSocket()
	if s == nil {
		return connectors.SendResult{}, errors.New(ReasonNotConnected)
	}
	body := payload.Text
	if payload.Format != "plain" {
		body = channelutil.MarkdownToWhatsApp(payload.Text)
	}
	full := body
	if hint := channelutil.ActionsHint(payload.Actions); hint != "" {
		full = body + "\n\n" + hint
	}
	var quoted *Message
	if payload.ReplyToChannelMsgID != "" {
		w.mu.Lock()
		quoted = w.recent[payload.ReplyToChannelMsgID]
		w.mu.Unlock()
	}
	if full == "" {
		full = " "
	}
	lastID := ""
	for i, chunk := range channelutil.SplitMessage(full, TextLimit) {
		var q *Message
		if i == 0 {
			q = quoted
		}
		sent, err := s.SendMessage(ctx, jid, Content{Text: chunk}, q)
		if err != nil {
			return connectors.SendResult{}, err
		}
		w.mu.Lock()
		w.remember(sent)
		w.mu.Unlock()
		if sent != nil && sent.Key.ID != "" {
			lastID = sent.Key.ID
		}
	}
	w.stopThinking(jid)
	return connectors.SendResult{ChannelMsgID: lastID}, nil
}

func (w *wa) clearThinkingTimers(t *thinking) {
	if t.lead != nil {
		w.o.Timers.ClearTimeout(t.lead)
	}
	if t.refresh != nil {
		w.o.Timers.ClearInterval(t.refresh)
	}
	w.o.Timers.ClearTimeout(t.cap)
}

func (w *wa) stopThinking(jid string) {
	w.mu.Lock()
	t := w.thinking[jid]
	if t == nil {
		w.mu.Unlock()
		return
	}
	delete(w.thinking, jid)
	w.clearThinkingTimers(t)
	w.mu.Unlock()
	s := w.openSocket()
	if s == nil {
		return
	}
	ctx := w.ctx()
	if t.key != nil {
		if _, err := s.SendMessage(ctx, jid, Content{React: &Reaction{Text: "", Key: *t.key}}, nil); err != nil {
			w.log.Debug("clear reaction failed", map[string]any{"err": err.Error()})
		}
	}
	_ = s.SendPresenceUpdate(ctx, "paused", jid)
}

// Typing is the thinking indicator: a reaction on the last inbound message,
// then "typing..." after the reaction lead, refreshed every 8 s, capped at 10
// minutes. Off clears the reaction and pauses presence.
func (w *wa) Typing(ctx context.Context, jid string, on bool, opts connectors.TypingOptions) error {
	if !on {
		w.stopThinking(jid)
		return nil
	}
	s := w.openSocket()
	w.mu.Lock()
	if s == nil || w.thinking[jid] != nil {
		w.mu.Unlock()
		return nil
	}
	reaction := ThinkingReaction
	if opts.Reaction != nil {
		reaction = *opts.Reaction
	}
	t := &thinking{}
	if reaction != "" {
		if k, ok := w.lastInbound[jid]; ok {
			t.key = &k
		}
	}
	t.cap = w.o.Timers.SetTimeout(func() { w.stopThinking(jid) }, w.o.TypingMaxMs)
	w.thinking[jid] = t
	w.mu.Unlock()

	compose := func() {
		w.mu.Lock()
		cur := w.sock
		w.mu.Unlock()
		if cur == nil {
			return
		}
		go func() {
			bg := w.ctx()
			if err := cur.PresenceSubscribe(bg, jid); err != nil {
				return
			}
			_ = cur.SendPresenceUpdate(bg, "composing", jid)
		}()
	}
	startTyping := func() {
		w.mu.Lock()
		t.lead = nil
		if w.thinking[jid] != t {
			w.mu.Unlock()
			return
		}
		t.refresh = w.o.Timers.SetInterval(compose, w.o.PresenceRefreshMs)
		w.mu.Unlock()
		compose()
	}
	if t.key == nil {
		startTyping()
		return nil
	}
	// The reaction goes first and typing follows once it has landed (see ReactionLeadMs).
	if _, err := s.SendMessage(ctx, jid, Content{React: &Reaction{Text: reaction, Key: *t.key}}, nil); err != nil {
		w.log.Debug("reaction failed", map[string]any{"err": err.Error()})
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.thinking[jid] != t {
		return nil
	}
	t.lead = w.o.Timers.SetTimeout(startTyping, w.o.ReactionLeadMs)
	return nil
}

// --- actions -------------------------------------------------------------------------

func (w *wa) action(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error) {
	switch name {
	case "pair":
		return w.pair(input)
	case "unlink":
		return w.unlink(ctx)
	}
	return nil, fmt.Errorf("Unknown WhatsApp action: %s", name)
}

func (w *wa) pair(input *jsonx.Object) (*jsonx.Object, error) {
	if v, ok := input.Get("phone"); ok {
		if phone, ok := v.(string); ok && strx.Trim(phone) != "" {
			digits := PairingDigits(phone)
			if len(digits) < 8 {
				return obj("ok", false, "error", "That phone number looks too short."), nil
			}
			w.mu.Lock()
			w.live.PairPhone = ptr(digits)
			w.mu.Unlock()
			w.c.PatchConfig(obj("pairPhone", digits))
		}
	}
	w.mu.Lock()
	if w.live.Connection == "open" && w.sock != nil && w.sock.Linked() {
		w.mu.Unlock()
		return obj("ok", false, "error", "Already linked. Unlink first to pair again."), nil
	}
	loggedOut := w.loggedOut
	w.mu.Unlock()
	// A linked session on disk is never wiped by Pair (it may just be
	// reconnecting, or paused after a 440). Only Unlink removes it, or a 401
	// that logged it out.
	if !loggedOut && InspectSession(w.storePath) == SessionLinked {
		return obj("ok", false, "error", "This number is already linked. Unlink first to pair again."), nil
	}
	// Cancel any connect in flight before touching the store.
	w.mu.Lock()
	w.generation++
	if w.reconnectTimer != nil {
		w.o.Timers.ClearTimeout(w.reconnectTimer)
		w.reconnectTimer = nil
	}
	old := w.endSocketLocked()
	w.mu.Unlock()
	endQuietly(old)
	// A fresh pairing: whatever session is on disk is logged out or unfinished.
	_ = WipeSession(w.storePath)
	w.mu.Lock()
	w.loggedOut = false
	w.clearPairingLocked()
	w.halted = false
	w.attempt = 0
	w.wantPairingCode = true
	w.live.Linked = false
	w.live.Reason = nil
	w.mu.Unlock()
	w.status("connecting", ptr(ReasonWaitingForPairing))
	w.connect()
	w.mu.Lock()
	var pp any
	if w.live.PairPhone != nil {
		pp = *w.live.PairPhone
	}
	w.mu.Unlock()
	return obj("ok", true, "pairPhone", pp), nil
}

func (w *wa) unlink(ctx context.Context) (*jsonx.Object, error) {
	w.mu.Lock()
	w.halted = true
	w.generation++
	if w.reconnectTimer != nil {
		w.o.Timers.ClearTimeout(w.reconnectTimer)
		w.reconnectTimer = nil
	}
	s := w.sock
	if s != nil && w.detach != nil {
		w.detach()
		w.detach = nil
	}
	w.mu.Unlock()
	if s != nil {
		if err := s.Logout(ctx); err != nil {
			w.log.Warn("whatsapp logout failed, deleting the session anyway", map[string]any{"err": err.Error()})
		}
	}
	w.mu.Lock()
	old := w.endSocketLocked()
	w.mu.Unlock()
	endQuietly(old)
	_ = WipeSession(w.storePath)
	w.mu.Lock()
	w.clearPairingLocked()
	w.live.Connection = "closed"
	w.live.Linked = false
	w.live.LinkedNumber = nil
	w.live.Reason = ptr(ReasonUnlinked)
	w.mu.Unlock()
	w.c.Event("wa_unlinked", "WhatsApp device unlinked from the dashboard", "info", nil)
	return obj("ok", true), nil
}

func (w *wa) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.generation++
	if w.reconnectTimer != nil {
		w.o.Timers.ClearTimeout(w.reconnectTimer)
		w.reconnectTimer = nil
	}
	for _, t := range w.thinking {
		w.clearThinkingTimers(t)
	}
	w.thinking = map[string]*thinking{}
	old := w.endSocketLocked()
	w.live.Connection = "closed"
	w.mu.Unlock()
	endQuietly(old)
	close(w.quit)
	w.lock.Release()
}

package whatsapp_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp/watest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	ownerJID = "628123456789@s.whatsapp.net"
	ownerLID = "555000111@lid"
)

func eq(t *testing.T, got, want any) {
	t.Helper()
	if jsonx.Stringify(got) != jsonx.Stringify(want) {
		t.Fatalf("got  %s\nwant %s", jsonx.Stringify(got), jsonx.Stringify(want))
	}
}

func cfg(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

type opts struct {
	config     *jsonx.Object
	registered *bool
	dataDir    string
	mod        func(*whatsapp.Options)
}

type env struct {
	rt      *connectors.Runtime
	ctx     *conntest.ChannelCtx
	clock   *testutil.FakeClock
	dataDir string
	mu      sync.Mutex
	sockets []*watest.FakeSocket
}

func (e *env) sock() *watest.FakeSocket {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sockets[len(e.sockets)-1]
}

func (e *env) socketCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sockets)
}

func (e *env) storePath() string { return whatsapp.StorePathFor(e.dataDir, "whatsapp") }

func setup(t *testing.T, o opts) *env {
	if o.dataDir == "" {
		o.dataDir = t.TempDir()
	}
	if o.config == nil {
		o.config = cfg("ownerPhone", "0812-3456-789")
	}
	e := &env{dataDir: o.dataDir, clock: testutil.NewFakeClock(time.Now().UnixMilli())}
	wo := whatsapp.Options{
		CreateSocket: func(_ context.Context, path string) (whatsapp.Socket, error) {
			registered := false
			if o.registered != nil {
				registered = *o.registered
			} else if _, err := os.Stat(path); err == nil {
				registered = true
			}
			s := watest.New(registered)
			e.mu.Lock()
			e.sockets = append(e.sockets, s)
			e.mu.Unlock()
			return s, nil
		},
		Timers: e.clock,
		Now:    e.clock.Now,
	}
	if o.mod != nil {
		o.mod(&wo)
	}
	e.rt = whatsapp.New(wo)
	e.ctx = conntest.NewChannelCtx(t, "whatsapp", "whatsapp", o.config, nil, o.dataDir)
	return e
}

func (e *env) start(t *testing.T) (*connectors.RunningConnector, error) {
	r, err := e.rt.Start(e.ctx.Context)
	if err == nil {
		t.Cleanup(func() { _ = r.Stop(context.Background()) })
	}
	return r, err
}

func (e *env) mustStart(t *testing.T) *connectors.RunningConnector {
	t.Helper()
	r, err := e.start(t)
	testutil.Must(t, err)
	return r
}

func openRunning(t *testing.T, o opts) (*env, *connectors.RunningConnector) {
	o.registered = testutil.Ptr(true)
	e := setup(t, o)
	r := e.mustStart(t)
	e.sock().OpenConn("")
	return e, r
}

func live(r *connectors.RunningConnector) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(jsonx.Stringify(r.Live())), &m)
	return m
}

func liveStr(r *connectors.RunningConnector, k string) any { return live(r)[k] }

func dm(id string, msg *waE2E.Message, key func(*whatsapp.Key)) *whatsapp.Message {
	if msg == nil {
		msg = &waE2E.Message{Conversation: proto.String("pesan " + id)}
	}
	m := &whatsapp.Message{Key: whatsapp.Key{RemoteJID: ownerJID, ID: id}, Message: msg, Timestamp: 1_700_000_000, PushName: "Zaki"}
	if key != nil {
		key(&m.Key)
	}
	return m
}

func act(t *testing.T, r *connectors.RunningConnector, name string, input *jsonx.Object) map[string]any {
	t.Helper()
	out, err := r.Action(context.Background(), name, input)
	testutil.Must(t, err)
	var m map[string]any
	_ = json.Unmarshal([]byte(jsonx.Stringify(out)), &m)
	return m
}

// writeStore makes a whatsmeow store on disk, with a paired device when linked.
func writeStore(t *testing.T, path string, linked bool) {
	t.Helper()
	testutil.Must(t, os.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755))
	d, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	testutil.Must(t, err)
	defer d.Close()
	c := sqlstore.NewWithDB(d, "sqlite", waLog.Noop)
	testutil.Must(t, c.Upgrade(context.Background()))
	if linked {
		dev := c.NewDevice()
		jid := types.NewADJID("628000001472", 0, 3)
		dev.ID = &jid
		dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignatureKey: make([]byte, 32), AccountSignature: make([]byte, 64), DeviceSignature: make([]byte, 64)}
		testutil.Must(t, c.PutDevice(context.Background(), dev))
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestManifest(t *testing.T) {
	t.Run("has the wizard steps and remove warning", func(t *testing.T) {
		var steps []string
		for _, s := range whatsapp.Manifest.Setup {
			steps = append(steps, s.Action)
		}
		eq(t, steps, []string{"ownerPhone", "pairWhatsApp"})
		eq(t, whatsapp.Manifest.RemoveWarning, "This unlinks the WhatsApp device. You'll need to pair again.")
		_, err := whatsapp.Manifest.ParseConfig(cfg("ownerPhone", "+62 812 3456 789"))
		testutil.Must(t, err)
		if _, err := whatsapp.Manifest.ParseConfig(cfg("ownerPhone", "abc")); err == nil {
			t.Fatal("abc accepted")
		}
	})
	t.Run("formats pairing codes and phone digits", func(t *testing.T) {
		eq(t, whatsapp.FormatPairingCode("abcd1234"), "ABCD-1234")
		eq(t, whatsapp.PairingDigits("0898-0000-1472"), "6289800001472")
		eq(t, whatsapp.PairingDigits("+62 898 0000 1472"), "6289800001472")
	})
}

func TestPairing(t *testing.T) {
	t.Run("requests one pairing code on the first qr and tracks qr rotation", func(t *testing.T) {
		e := setup(t, opts{registered: testutil.Ptr(false), config: cfg("ownerPhone", "0812-3456-789", "pairPhone", "0898-0000-1472")})
		r := e.mustStart(t)
		e.sock().QR("QR-1")
		conntest.Until(t, func() bool { l := live(r); return l["pairingCode"] != nil && l["qrSvg"] != nil })
		eq(t, e.sock().PairingRequests(), []string{"6289800001472"})
		l := live(r)
		eq(t, []any{l["connection"], l["qr"], l["pairingCode"], l["expiresHint"], l["pairPhone"]}, []any{"pairing", "QR-1", "ABCD-1234", whatsapp.ExpiresHint, "6289800001472"})
		if !strings.Contains(l["qrSvg"].(string), "<svg") {
			t.Fatal("no svg")
		}
		last := e.ctx.LastStatusFull()
		eq(t, []any{last.Status, *last.Detail}, []any{"connecting", whatsapp.ReasonWaitingForPairing})

		e.sock().QR("QR-2")
		eq(t, liveStr(r, "qr"), "QR-2")
		conntest.Settle()
		eq(t, len(e.sock().PairingRequests()), 1)

		e.sock().OpenConn("")
		last = e.ctx.LastStatusFull()
		eq(t, []any{last.Status, last.Detail == nil}, []any{"on", true})
		l = live(r)
		eq(t, []any{l["connection"], l["linked"], l["linkedNumber"], l["qr"], l["qrSvg"], l["pairingCode"], l["expiresHint"]}, []any{"open", true, "628000001472", nil, nil, nil, nil})
		found := false
		for _, p := range e.ctx.PatchList() {
			found = found || jsonx.Stringify(p) == `{"linkedNumber":"628000001472"}`
		}
		eq(t, found, true)
		eq(t, r.Channel.Reachable(), true)
	})
	t.Run("shows only the QR when no pairing number is known", func(t *testing.T) {
		e := setup(t, opts{registered: testutil.Ptr(false)})
		r := e.mustStart(t)
		e.sock().QR("QR-ONLY")
		conntest.Settle()
		eq(t, e.sock().PairingRequests(), []string(nil))
		eq(t, []any{liveStr(r, "qr"), liveStr(r, "pairingCode")}, []any{"QR-ONLY", nil})
	})
	t.Run("stops after the pairing window closes, and pair restarts it", func(t *testing.T) {
		e := setup(t, opts{registered: testutil.Ptr(false), config: cfg("pairPhone", "6289800001472")})
		r := e.mustStart(t)
		e.sock().QR("QR-1")
		conntest.Settle()
		e.sock().Close(408)
		last := e.ctx.LastStatusFull()
		eq(t, []any{last.Status, *last.Detail}, []any{"error", whatsapp.ReasonPairingExpired})
		conntest.Settle()
		eq(t, e.socketCount(), 1)

		eq(t, act(t, r, "pair", cfg("phone", "0811-2222-3333")), map[string]any{"ok": true, "pairPhone": "6281122223333"})
		found := false
		for _, p := range e.ctx.PatchList() {
			found = found || jsonx.Stringify(p) == `{"pairPhone":"6281122223333"}`
		}
		eq(t, found, true)
		eq(t, e.socketCount(), 2)
		e.sock().QR("QR-2")
		conntest.Until(t, func() bool { return liveStr(r, "pairingCode") != nil })
		eq(t, e.sock().PairingRequests(), []string{"6281122223333"})
	})
	t.Run("refuses a phone that is too short", func(t *testing.T) {
		e := setup(t, opts{registered: testutil.Ptr(false)})
		r := e.mustStart(t)
		eq(t, act(t, r, "pair", cfg("phone", "0812")), map[string]any{"ok": false, "error": "That phone number looks too short."})
	})
	t.Run("refuses to re-pair while linked and open", func(t *testing.T) {
		_, r := openRunning(t, opts{})
		eq(t, act(t, r, "pair", nil)["ok"], false)
	})
	t.Run("clears an unfinished pairing session before connecting", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.StorePathFor(dir, "whatsapp")
		writeStore(t, path, false)
		eq(t, whatsapp.InspectSession(path), whatsapp.SessionIncomplete)
		e := setup(t, opts{dataDir: dir})
		e.mustStart(t)
		eq(t, exists(path), false)
	})
	t.Run("keeps a linked session on disk", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.StorePathFor(dir, "whatsapp")
		writeStore(t, path, true)
		e := setup(t, opts{dataDir: dir})
		e.mustStart(t)
		eq(t, exists(path), true)
		tr, _ := e.rt.Test(e.ctx.Context)
		eq(t, tr.Data, cfg("linked", true))
	})
}

func TestPairSafety(t *testing.T) {
	t.Run("refuses Pair while a linked session is on disk, even when not open", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.StorePathFor(dir, "whatsapp")
		writeStore(t, path, true)
		e := setup(t, opts{dataDir: dir})
		r := e.mustStart(t)
		// Still connecting (never opened), e.g. during a reconnect.
		res := act(t, r, "pair", nil)
		if res["ok"] != false || !regexp.MustCompile(`already linked`).MatchString(res["error"].(string)) {
			t.Fatal(res)
		}
		eq(t, exists(path), true)
		eq(t, e.socketCount(), 1)
	})
	t.Run("allows Pair over a session that a 401 logged out", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.StorePathFor(dir, "whatsapp")
		writeStore(t, path, true)
		e := setup(t, opts{dataDir: dir, registered: testutil.Ptr(true)})
		r := e.mustStart(t)
		e.sock().OpenConn("")
		e.sock().Close(401)
		eq(t, act(t, r, "pair", nil)["ok"], true)
		eq(t, exists(path), false)
		eq(t, e.socketCount(), 2)
	})
	t.Run("never ends up with two sockets when Pair lands during a reconnect", func(t *testing.T) {
		var mu sync.Mutex
		var sockets []*watest.FakeSocket
		release := make(chan struct{})
		slow := false
		e := setup(t, opts{mod: func(o *whatsapp.Options) {
			o.CreateSocket = func(context.Context, string) (whatsapp.Socket, error) {
				s := watest.New(false)
				mu.Lock()
				sockets = append(sockets, s)
				wait := slow
				slow = false
				mu.Unlock()
				if wait {
					<-release
				}
				return s, nil
			}
		}})
		count := func() int { mu.Lock(); defer mu.Unlock(); return len(sockets) }
		at := func(i int) *watest.FakeSocket { mu.Lock(); defer mu.Unlock(); return sockets[i] }
		r := e.mustStart(t)
		at(0).QR("QR-1")
		// A restart-required close schedules a backoff reconnect; its socket is slow.
		at(0).Close(515)
		mu.Lock()
		slow = true
		mu.Unlock()
		e.clock.Advance(1000, nil)
		conntest.Until(t, func() bool { return count() == 2 })
		// Pair while that reconnect is still waiting on createSocket.
		eq(t, act(t, r, "pair", cfg("phone", "0811-2222-3333"))["ok"], true)
		eq(t, count(), 3)
		close(release)
		conntest.Until(t, func() bool { return at(1).Ended() })
		conntest.Settle()
		// The stale reconnect's socket is ended and never wired up.
		eq(t, at(1).ListenerCount(), 0)
		eq(t, at(2).Ended(), false)
		eq(t, at(2).ListenerCount(), 1)
	})
}

func TestLifecycle(t *testing.T) {
	t.Run("pauses on 440 and does not reconnect", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		e.sock().Close(440)
		last := e.ctx.LastStatusFull()
		eq(t, []any{last.Status, *last.Detail}, []any{"paused", whatsapp.ReasonReplaced})
		eq(t, liveStr(r, "reason"), whatsapp.ReasonReplaced)
		e.clock.Advance(5*60_000, nil)
		conntest.Settle()
		eq(t, e.socketCount(), 1)
		eq(t, r.Channel.Reachable(), false)
	})
	t.Run("reports unlinked on 401 and does not reconnect", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		e.sock().Close(401)
		last := e.ctx.LastStatusFull()
		eq(t, []any{last.Status, *last.Detail}, []any{"error", "unlinked"})
		eq(t, liveStr(r, "linked"), false)
		e.clock.Advance(5*60_000, nil)
		conntest.Settle()
		eq(t, e.socketCount(), 1)
	})
	t.Run("reconnects 515 and other closes with exponential backoff, reset on open", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		detail := func() string { return *e.ctx.LastStatusFull().Detail }
		e.sock().Close(515)
		if !strings.Contains(detail(), "Reconnecting in 1s") {
			t.Fatal(detail())
		}
		e.clock.Advance(999, nil)
		conntest.Settle()
		eq(t, e.socketCount(), 1)
		e.clock.Advance(1, nil)
		conntest.Until(t, func() bool { return e.socketCount() == 2 })

		e.sock().Close(428)
		if !strings.Contains(detail(), "Reconnecting in 2s") {
			t.Fatal(detail())
		}
		e.clock.Advance(2000, nil)
		conntest.Until(t, func() bool { return e.socketCount() == 3 })

		e.sock().Close(500)
		e.clock.Advance(3999, nil)
		conntest.Settle()
		eq(t, e.socketCount(), 3)
		e.clock.Advance(1, nil)
		conntest.Until(t, func() bool { return e.socketCount() == 4 })

		e.sock().OpenConn("")
		e.sock().Close(515)
		if !strings.Contains(detail(), "Reconnecting in 1s") {
			t.Fatal(detail())
		}
		// The replaced sockets no longer have listeners.
		e.mu.Lock()
		first := e.sockets[0]
		e.mu.Unlock()
		eq(t, first.ListenerCount(), 0)
	})
	t.Run("starts one replacement per socket even when close is reported twice", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		first := e.sock()
		first.Close(428)
		first.Close(428)
		e.clock.Advance(10_000, nil)
		conntest.Until(t, func() bool { return e.socketCount() == 2 })
		conntest.Settle()
		eq(t, e.socketCount(), 2)
	})
	t.Run("caps the backoff at 60 s", func(t *testing.T) {
		var got []int64
		for _, a := range []int{0, 1, 2, 5, 6, 10} {
			got = append(got, whatsapp.BackoffDelay(a, 1000, 60_000))
		}
		eq(t, got, []int64{1000, 2000, 4000, 32000, 60000, 60000})
	})
	t.Run("syncs the owner identity from the configured phone", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		o, _, _ := owner.GetOwner(e.ctx.DB, "whatsapp")
		eq(t, o.ExternalID, ownerJID)
	})
	t.Run("a failed socket creation reconnects with backoff", func(t *testing.T) {
		calls := 0
		e := setup(t, opts{mod: func(o *whatsapp.Options) {
			inner := o.CreateSocket
			o.CreateSocket = func(ctx context.Context, p string) (whatsapp.Socket, error) {
				calls++
				if calls == 1 {
					return nil, errors.New("boom")
				}
				return inner(ctx, p)
			}
		}})
		e.mustStart(t)
		eq(t, *e.ctx.LastStatusFull().Detail, "Reconnecting in 1s (close unknown)")
		e.clock.Advance(1000, nil)
		conntest.Until(t, func() bool { return e.socketCount() == 1 })
	})
	t.Run("stops when the connector context is cancelled", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		e.ctx.Abort()
		// The lock file goes after the socket ends, so wait for it too.
		conntest.Until(t, func() bool {
			return !r.Channel.Reachable() && e.sock().Ended() && !exists(whatsapp.LockPathFor(e.dataDir, "whatsapp"))
		})
	})
}

func inboundJSON(v any) string {
	b, _ := jsonx.Marshal(v)
	p, _ := jsonx.Parse(b)
	return jsonx.Canonical(p)
}

func TestInbound(t *testing.T) {
	t.Run("forwards a phone JID DM with its LID from the mapping", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().SetLID(ownerLID, ownerJID)
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		eq(t, inboundJSON(e.ctx.Inbound()[0]), inboundJSON(connectors.InboundMessage{
			Channel: "whatsapp", ConnectorID: "whatsapp", ChatExternalID: ownerJID, ChatKind: "dm", ChatTitle: "Zaki",
			SenderExternalID: ownerJID, SenderAltID: ownerLID, SenderName: "Zaki", ChannelMsgID: "IN1", Text: "pesan IN1", At: 1_700_000_000_000,
		}))
	})
	t.Run("maps a LID DM through remoteJidAlt", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().Message(dm("IN2", nil, func(k *whatsapp.Key) { k.RemoteJID, k.RemoteJIDAlt = ownerLID, ownerJID }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []string{in.ChatExternalID, in.SenderExternalID, in.SenderAltID}, []string{ownerLID, ownerLID, ownerJID})
	})
	t.Run("maps a LID DM through the LID mapping when no alt is given", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().SetLID(ownerLID, "628123456789:5@s.whatsapp.net")
		e.sock().Message(dm("IN3", nil, func(k *whatsapp.Key) { k.RemoteJID = ownerLID }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []string{in.SenderExternalID, in.SenderAltID}, []string{ownerLID, ownerJID})
	})
	t.Run("drops fromMe, status, newsletters and non-notify batches", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		s := e.sock()
		s.Message(dm("A", nil, func(k *whatsapp.Key) { k.FromMe = true }), "")
		s.Message(dm("C", nil, func(k *whatsapp.Key) { k.RemoteJID = "status@broadcast" }), "")
		s.Message(dm("D", nil, func(k *whatsapp.Key) { k.RemoteJID = "1203630@newsletter" }), "")
		s.Message(dm("E", nil, nil), "append")
		s.Message(dm("F", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		conntest.Settle()
		var ids []string
		for _, m := range e.ctx.Inbound() {
			ids = append(ids, m.ChannelMsgID)
		}
		eq(t, ids, []string{"F"})
	})
	t.Run("reads groups with the participant as sender and marks mentions and replies to us as addressed", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		self := e.sock().User().ID
		g := func(k *whatsapp.Key) { k.RemoteJID, k.Participant = "120363@g.us", "62811@s.whatsapp.net" }
		budi := func(m *whatsapp.Message) *whatsapp.Message { m.PushName = "Budi"; return m }
		e.sock().Message(budi(dm("G1", &waE2E.Message{Conversation: proto.String("ngobrol biasa")}, g)), "")
		e.sock().Message(budi(dm("G2", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("@bot halo"), ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{self}}}}, g)), "")
		e.sock().Message(dm("G3", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("lanjut"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("BOT9"), Participant: proto.String(self), QuotedMessage: &waE2E.Message{Conversation: proto.String("tadi")}}}}, g), "")
		e.sock().Message(dm("G4", nil, func(k *whatsapp.Key) { k.RemoteJID = "120363@g.us" }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 3 })
		conntest.Settle()
		in := e.ctx.Inbound()
		var got [][]any
		for _, m := range in {
			got = append(got, []any{m.ChannelMsgID, m.Addressed})
		}
		eq(t, got, [][]any{{"G1", false}, {"G2", true}, {"G3", true}})
		eq(t, []string{in[1].ChatExternalID, in[1].ChatKind, in[1].SenderExternalID, in[1].SenderName}, []string{"120363@g.us", "group", "62811@s.whatsapp.net", "Budi"})
		eq(t, in[2].Quoted.From, "self")
	})
	// A contact card in a group was dropped, so the agent couldn't find the
	// number when someone asked for it later (rpt_0mwmwtby5l0zrpy3sgg).
	t.Run("turns contact cards, locations and captionless videos into text", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		g := func(k *whatsapp.Key) { k.RemoteJID, k.Participant = "120363@g.us", "62811@s.whatsapp.net" }
		vcard := "BEGIN:VCARD\nVERSION:3.0\nFN:Primadona\nTEL;type=CELL;waid=6281200001111:+62 812-0000-1111\nEND:VCARD"
		e.sock().Message(dm("C1", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Primadona ✂️"), Vcard: proto.String(vcard)}}, g), "")
		e.sock().Message(dm("C2", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(-3.5), DegreesLongitude: proto.Float64(114.5)}}, g), "")
		e.sock().Message(dm("C3", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}, g), "")
		e.sock().Message(dm("C4", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("lucu")}}, g), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 4 })
		in := e.ctx.Inbound()
		eq(t, []string{in[0].Text, in[1].Text, in[2].Text, in[3].Text}, []string{
			"[contact: Primadona ✂️, +62 812-0000-1111]",
			"[location: -3.500000,114.500000, https://maps.google.com/?q=-3.500000,114.500000]",
			"[video]",
			"[video]\nlucu",
		})
		eq(t, in[0].Media, []connectors.InboundMedia(nil))
		eq(t, e.sock().Downloads(), []string(nil))
	})
	t.Run("reads extended text with the quoted id", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().Message(dm("IN4", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("yang ini"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("OLD1")}}}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []string{in.Text, in.ReplyToChannelMsgID}, []string{"yang ini", "OLD1"})
	})
	t.Run("passes the quoted text and who wrote it", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		quote := func(text, stanza, participant string, quoted *waE2E.Message) *waE2E.Message {
			ci := &waE2E.ContextInfo{StanzaID: proto.String(stanza), QuotedMessage: quoted}
			if participant != "" {
				ci.Participant = proto.String(participant)
			}
			return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: ci}}
		}
		e.sock().Message(dm("IN5", quote("Ini?", "BOT1", "628000001472@s.whatsapp.net", &waE2E.Message{Conversation: proto.String("Jadwal besok jam 9")}), nil), "")
		e.sock().Message(dm("IN6", quote("maksudnya", "OWN1", ownerLID, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("struk")}}), nil), "")
		e.sock().Message(dm("IN7", quote("hilang?", "GONE1", "", nil), nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 3 })
		in := e.ctx.Inbound()
		eq(t, []any{in[0].Text, in[0].ReplyToChannelMsgID, in[0].Quoted}, []any{"Ini?", "BOT1", connectors.InboundQuote{From: "self", Text: "Jadwal besok jam 9"}})
		eq(t, in[1].Quoted, connectors.InboundQuote{From: "sender", Text: "struk", Media: "[image]"})
		eq(t, in[2].Quoted, connectors.InboundQuote{From: "unknown", Text: ""})
	})
	t.Run("downloads owner media and skips stranger media", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().Message(dm("IMG1", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("ini apa"), Mimetype: proto.String("image/jpeg")}}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.Text, in.Media}, []any{"ini apa", []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString([]byte("media:IMG1"))}}})

		e.sock().Message(dm("VN1", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(4)}}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		m := e.ctx.Inbound()[1].Media[0]
		eq(t, []any{m.Kind, *m.DurationSec}, []any{"audio", 4})

		e.sock().Message(dm("IMG2", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("spam")}}, func(k *whatsapp.Key) { k.RemoteJID = "629999@s.whatsapp.net" }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 3 })
		eq(t, e.ctx.Inbound()[2].Media, []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg"}})
		eq(t, e.sock().Downloads(), []string{"IMG1", "VN1"})
	})
	// A guest's photo in a group set up for guests never reached the model, and
	// one without a caption vanished (rpt_0mwkrtff0z3w0ld5vfp).
	t.Run("downloads a guest's media when the message will be answered", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		var asked []connectors.InboundMessage
		e.ctx.Services.WantsMedia = func(m connectors.InboundMessage) bool {
			asked = append(asked, m)
			return m.Addressed
		}
		group := func(id string, mention bool) *whatsapp.Message {
			img := &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}
			if mention {
				img.Caption = proto.String("@bot lihat")
				img.ContextInfo = &waE2E.ContextInfo{MentionedJID: []string{"628000001472@s.whatsapp.net"}}
			}
			return dm(id, &waE2E.Message{ImageMessage: img}, func(k *whatsapp.Key) {
				k.RemoteJID, k.Participant = "120363@g.us", "62811@s.whatsapp.net"
			})
		}
		e.sock().Message(group("GIMG1", true), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.Text, in.Addressed, in.Media}, []any{"@bot lihat", true, []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString([]byte("media:GIMG1"))}}})
		eq(t, []any{asked[0].ChatKind, asked[0].SenderExternalID, asked[0].Text}, []any{"group", "62811@s.whatsapp.net", "@bot lihat"})

		// Group talk: not downloaded, but the photo still reaches the inbox.
		e.sock().Message(group("GIMG2", false), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		in = e.ctx.Inbound()[1]
		eq(t, []any{in.Text, in.Media}, []any{"", []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg"}}})
		eq(t, e.sock().Downloads(), []string{"GIMG1"})
	})
	t.Run("says so when a wanted photo can't be downloaded", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().Media = func(*whatsapp.Message) ([]byte, error) { return nil, errors.New("410 gone") }
		e.sock().Message(dm("IMG9", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.Text, len(in.Media)}, []any{"[image could not be downloaded]", 0})
	})
	// A sticker never reached the model at all (rpt_0mwkukqo7spxy2l0i2y).
	t.Run("sends stickers to the model as png images", func(t *testing.T) {
		still, err := os.ReadFile("../../sticker/testdata/still-lossless.webp")
		testutil.Must(t, err)
		animated, err := os.ReadFile("../../sticker/testdata/animated-lossless.webp")
		testutil.Must(t, err)
		e, _ := openRunning(t, opts{})
		e.sock().Media = func(m *whatsapp.Message) ([]byte, error) {
			switch m.Key.ID {
			case "ST1":
				return still, nil
			case "ST2":
				return animated, nil
			}
			return []byte("not a webp"), nil
		}
		isPNG := func(m connectors.InboundMedia) bool {
			b, err := base64.StdEncoding.DecodeString(m.Data)
			return err == nil && m.Kind == "image" && m.MimeType == "image/png" && strings.HasPrefix(string(b), "\x89PNG")
		}
		sticker := func(id string, s *waE2E.StickerMessage, edit func(*whatsapp.Key)) *whatsapp.Message {
			if s.Mimetype == nil {
				s.Mimetype = proto.String("image/webp")
			}
			return dm(id, &waE2E.Message{StickerMessage: s}, edit)
		}

		e.sock().Message(sticker("ST1", &waE2E.StickerMessage{}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.Text, len(in.Media), isPNG(in.Media[0])}, []any{"[sticker]", 1, true})

		// Animated, even when WhatsApp leaves out isAnimated.
		e.sock().Message(sticker("ST2", &waE2E.StickerMessage{}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		in = e.ctx.Inbound()[1]
		eq(t, []any{in.Text, len(in.Media), isPNG(in.Media[0])}, []any{"[animated sticker, first frame]", 1, true})

		// Bytes that aren't a WebP: the message still says a sticker came.
		e.sock().Message(sticker("ST3", &waE2E.StickerMessage{}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 3 })
		in = e.ctx.Inbound()[2]
		eq(t, []any{in.Text, len(in.Media)}, []any{"[sticker, could not be read]", 0})

		// Lottie: nothing to draw, nothing downloaded.
		e.sock().Message(sticker("ST4", &waE2E.StickerMessage{IsLottie: proto.Bool(true), IsAnimated: proto.Bool(true), Mimetype: proto.String("application/was")}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 4 })
		in = e.ctx.Inbound()[3]
		eq(t, []any{in.Text, len(in.Media)}, []any{"[animated sticker, can't be shown]", 0})

		// A stranger's sticker isn't downloaded but still reaches the inbox.
		e.sock().Message(sticker("ST5", &waE2E.StickerMessage{}, func(k *whatsapp.Key) { k.RemoteJID = "629999@s.whatsapp.net" }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 5 })
		in = e.ctx.Inbound()[4]
		eq(t, []any{in.Text, in.Media}, []any{"[sticker]", []connectors.InboundMedia{{Kind: "image", MimeType: "image/webp"}}})
		eq(t, e.sock().Downloads(), []string{"ST1", "ST2", "ST3"})
	})
	// A guest's sticker in a group is context, so it isn't downloaded. When
	// the owner replies to it and asks the bot, the bot only saw "[sticker]"
	// (rpt_0mwkvfallcmflwkzcev). The reply now fetches the quoted sticker.
	t.Run("fetches a quoted sticker or photo when the reply is for the bot", func(t *testing.T) {
		still, err := os.ReadFile("../../sticker/testdata/still-lossless.webp")
		testutil.Must(t, err)
		e, _ := openRunning(t, opts{})
		e.ctx.Services.WantsMedia = func(m connectors.InboundMessage) bool { return m.Addressed }
		e.sock().Media = func(m *whatsapp.Message) ([]byte, error) {
			if m.Message.GetStickerMessage() != nil {
				return still, nil
			}
			return []byte("media:" + m.Key.ID), nil
		}
		inGroup := func(k *whatsapp.Key) { k.RemoteJID, k.Participant = "120363@g.us", "62811@s.whatsapp.net" }
		quoting := func(text string, mention bool, quoted *waE2E.Message) *waE2E.Message {
			ci := &waE2E.ContextInfo{StanzaID: proto.String("GST1"), Participant: proto.String("62822@s.whatsapp.net"), QuotedMessage: quoted}
			if mention {
				ci.MentionedJID = []string{"628000001472@s.whatsapp.net"}
			}
			return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: ci}}
		}
		stickerMsg := &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}}

		// Group talk: the sticker isn't downloaded.
		e.sock().Message(dm("GST1", stickerMsg, func(k *whatsapp.Key) { k.RemoteJID, k.Participant = "120363@g.us", "62822@s.whatsapp.net" }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		eq(t, e.sock().Downloads(), []string(nil))

		// A reply to it that isn't for the bot doesn't fetch it either.
		e.sock().Message(dm("GR1", quoting("wkwk", false, stickerMsg), inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		in := e.ctx.Inbound()[1]
		eq(t, []any{in.Text, len(in.Media)}, []any{"wkwk", 0})
		eq(t, e.sock().Downloads(), []string(nil))

		// "@bot can you read this sticker?" as a reply: the sticker comes along.
		e.sock().Message(dm("GR2", quoting("@bot dah bisa baca stiker blom", true, stickerMsg), inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 3 })
		in = e.ctx.Inbound()[2]
		eq(t, []any{in.Text, in.Quoted.Media, len(in.Media)}, []any{"@bot dah bisa baca stiker blom\n[the quoted sticker is attached]", "[sticker]", 1})
		b, err := base64.StdEncoding.DecodeString(in.Media[0].Data)
		testutil.Must(t, err)
		eq(t, []any{in.Media[0].Kind, in.Media[0].MimeType, strings.HasPrefix(string(b), "\x89PNG")}, []any{"image", "image/png", true})
		eq(t, e.sock().Downloads(), []string{"GST1"})

		// A quoted photo works the same way.
		photo := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}
		e.sock().Message(dm("GR3", quoting("@bot ini apa", true, photo), inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 4 })
		in = e.ctx.Inbound()[3]
		eq(t, []any{in.Text, in.Media}, []any{"@bot ini apa\n[the quoted photo is attached]", []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString([]byte("media:GST1"))}}})

		// A quoted voice note or Lottie sticker isn't fetched.
		e.sock().Message(dm("GR4", quoting("@bot dengar", true, &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}), inGroup), "")
		e.sock().Message(dm("GR5", quoting("@bot ini", true, &waE2E.Message{StickerMessage: &waE2E.StickerMessage{IsLottie: proto.Bool(true)}}), inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 6 })
		eq(t, []any{e.ctx.Inbound()[4].Text, e.ctx.Inbound()[5].Text}, []any{"@bot dengar", "@bot ini"})
		eq(t, e.sock().Downloads(), []string{"GST1", "GST1"})
	})
	t.Run("does not download documents over 1 MB", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.sock().Message(dm("DOC1", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("big.pdf"), FileLength: proto.Uint64(5 * 1024 * 1024), Mimetype: proto.String("application/pdf")}}, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		if !strings.Contains(e.ctx.Inbound()[0].Text, "over the 1 MB limit") {
			t.Fatal(e.ctx.Inbound()[0].Text)
		}
		eq(t, e.sock().Downloads(), []string(nil))
	})
	t.Run("fetches photos and documents for a chat that auto-saves, never stickers or voice notes", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.ctx.Services.WantsMedia = func(m connectors.InboundMessage) bool { return m.Addressed }
		e.ctx.Services.SavesMedia = func(m connectors.InboundMessage) bool { return true }
		inGroup := func(k *whatsapp.Key) { k.RemoteJID, k.Participant = "120363@g.us", "62811@s.whatsapp.net" }
		// Group talk: fetched for the auto-save only, the text says nothing more.
		e.sock().Message(dm("AS1", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("struk")}}, inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.Text, len(in.Media), in.Media[0].SaveOnly, in.Media[0].Caption, in.Media[0].Data != ""}, []any{"struk", 1, true, "struk", true})
		// A 5 MB document someone asked about: saved, but too big for the model.
		big := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("big.pdf"), FileLength: proto.Uint64(5 * 1024 * 1024), Mimetype: proto.String("application/pdf"), Caption: proto.String("@bot simpan"),
			ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{"628000001472@s.whatsapp.net"}}}}
		e.sock().Message(dm("AS2", big, inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		in = e.ctx.Inbound()[1]
		if !strings.Contains(in.Text, "over the 1 MB limit") || len(in.Media) != 1 || !in.Media[0].SaveOnly {
			t.Fatalf("%q %+v", in.Text, in.Media)
		}
		// Over the store's 10 MB cap: not fetched at all.
		huge := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("huge.pdf"), FileLength: proto.Uint64(11 * 1024 * 1024), Mimetype: proto.String("application/pdf")}}
		e.sock().Message(dm("AS3", huge, inGroup), "")
		e.sock().Message(dm("AS4", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}, inGroup), "")
		e.sock().Message(dm("AS5", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}}, inGroup), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 5 })
		eq(t, e.sock().Downloads(), []string{"AS1", "AS2"})
	})
}

func typer(r *connectors.RunningConnector) connectors.Typer { return r.Channel.(connectors.Typer) }

func reactionOf(s watest.Sent) (string, string) {
	if s.Content.React == nil {
		return "", ""
	}
	return s.Content.React.Text, s.Content.React.Key.ID
}

func lastSent(t *testing.T, s *watest.FakeSocket) watest.Sent {
	t.Helper()
	l := s.SentList()
	if len(l) == 0 {
		t.Fatal("nothing sent")
	}
	return l[len(l)-1]
}

func TestTypingAndSend(t *testing.T) {
	lead := func(o *whatsapp.Options) { o.ReactionLeadMs = 30 }
	bg := context.Background()
	t.Run("reacts while thinking, types only after the reaction, and clears both on off", func(t *testing.T) {
		e, r := openRunning(t, opts{mod: lead})
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{}))
		s := lastSent(t, e.sock())
		text, id := reactionOf(s)
		eq(t, []string{s.JID, text, id}, []string{ownerJID, whatsapp.ThinkingReaction, "IN1"})
		// Typing waits for the reaction to land, so it never flashes before the reaction.
		conntest.Settle()
		eq(t, e.sock().HasPresence("composing", ownerJID), false)
		e.clock.Advance(50, nil)
		conntest.Until(t, func() bool { return e.sock().HasPresence("composing", ownerJID) })
		testutil.Must(t, typer(r).Typing(bg, ownerJID, false, connectors.TypingOptions{}))
		text, id = reactionOf(lastSent(t, e.sock()))
		eq(t, []string{text, id}, []string{"", "IN1"})
		p := e.sock().PresenceList()
		eq(t, p[len(p)-1], watest.Presence{Type: "paused", JID: ownerJID})
	})
	t.Run("reacts with the chat's own emoji", func(t *testing.T) {
		e, r := openRunning(t, opts{mod: lead})
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{Reaction: testutil.Ptr("👀")}))
		text, id := reactionOf(lastSent(t, e.sock()))
		eq(t, []string{text, id}, []string{"👀", "IN1"})
		testutil.Must(t, typer(r).Typing(bg, ownerJID, false, connectors.TypingOptions{}))
		text, id = reactionOf(lastSent(t, e.sock()))
		eq(t, []string{text, id}, []string{"", "IN1"})
	})
	t.Run("types right away without a reaction when the chat has ack off", func(t *testing.T) {
		e, r := openRunning(t, opts{mod: lead})
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		before := len(e.sock().SentList())
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{Reaction: testutil.Ptr("")}))
		conntest.Until(t, func() bool { return e.sock().HasPresence("composing", ownerJID) })
		eq(t, len(e.sock().SentList()), before)
		testutil.Must(t, typer(r).Typing(bg, ownerJID, false, connectors.TypingOptions{}))
		eq(t, len(e.sock().SentList()), before)
		p := e.sock().PresenceList()
		eq(t, p[len(p)-1], watest.Presence{Type: "paused", JID: ownerJID})
	})
	t.Run("never starts typing when the reply comes before the reaction lead", func(t *testing.T) {
		e, r := openRunning(t, opts{mod: lead})
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{}))
		_, err := r.Channel.Send(bg, ownerJID, db.OutboxPayload{Text: "oke", Format: "plain"})
		testutil.Must(t, err)
		e.clock.Advance(50, nil)
		conntest.Settle()
		eq(t, e.sock().HasPresence("composing", ownerJID), false)
	})
	t.Run("types right away when there is no message to react to", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{}))
		conntest.Until(t, func() bool { return e.sock().HasPresence("composing", ownerJID) })
		eq(t, len(e.sock().SentList()), 0)
	})
	t.Run("refreshes typing every 8 s and stops after 10 minutes", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{}))
		composing := func() int {
			n := 0
			for _, p := range e.sock().PresenceList() {
				if p.Type == "composing" {
					n++
				}
			}
			return n
		}
		conntest.Until(t, func() bool { return composing() == 1 })
		e.clock.Advance(16_000, nil)
		conntest.Until(t, func() bool { return composing() == 3 })
		e.clock.Advance(10*60_000, nil)
		conntest.Until(t, func() bool { return e.sock().HasPresence("paused", ownerJID) })
		eq(t, e.clock.Pending(), 1) // only the lock heartbeat
	})
	t.Run("sends WhatsApp formatting with an actions hint, quoting the original", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{}))
		res, err := r.Channel.Send(bg, ownerJID, db.OutboxPayload{
			Text: "**Minum obat** sekarang", ReplyToChannelMsgID: "IN1",
			Actions: []db.OutboxAction{{ID: "rem:done:r1", Label: "Done"}, {ID: "rem:snooze:r1:1h", Label: "1 jam"}, {ID: "rem:snooze:r1:tomorrow", Label: "Besok"}},
		})
		testutil.Must(t, err)
		var texts []watest.Sent
		for _, s := range e.sock().SentList() {
			if s.Content.React == nil {
				texts = append(texts, s)
			}
		}
		eq(t, len(texts), 1)
		eq(t, texts[0].Content.Text, "*Minum obat* sekarang\n\nBalas: done / tunda 1j / tunda besok")
		eq(t, texts[0].Quoted.Key.ID, "IN1")
		if !regexp.MustCompile(`^OUT\d+$`).MatchString(res.ChannelMsgID) {
			t.Fatal(res.ChannelMsgID)
		}
		// The thinking reaction is cleared on reply.
		text, _ := reactionOf(lastSent(t, e.sock()))
		eq(t, lastSent(t, e.sock()).Content.React != nil && text == "", true)
	})
	t.Run("sends without a quote when the message is unknown and refuses when offline", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		_, err := r.Channel.Send(bg, ownerJID, db.OutboxPayload{Text: "halo", ReplyToChannelMsgID: "NOPE", Format: "plain"})
		testutil.Must(t, err)
		s := lastSent(t, e.sock())
		eq(t, []any{s.JID, s.Content, s.Quoted == nil}, []any{ownerJID, whatsapp.Content{Text: "halo"}, true})
		e.sock().Close(440)
		_, err = r.Channel.Send(bg, ownerJID, db.OutboxPayload{Text: "halo"})
		if err == nil || err.Error() != whatsapp.ReasonNotConnected {
			t.Fatal(err)
		}
	})
	t.Run("sends media as one message with its caption, keeping the thinking reaction", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		e.sock().Message(dm("IN1", nil, nil), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		testutil.Must(t, typer(r).Typing(bg, ownerJID, true, connectors.TypingOptions{}))
		res, err := r.Channel.Send(bg, ownerJID, db.OutboxPayload{
			Text: "**Inhaler** dengan spacer", ReplyToChannelMsgID: "IN1",
			Media: &db.OutboxMedia{ID: "med_1", Kind: db.MediaImage, MimeType: "image/jpeg", Filename: "inhaler.jpg", Size: 3, Bytes: []byte{1, 2, 3}},
		})
		testutil.Must(t, err)
		var media []watest.Sent
		for _, s := range e.sock().SentList() {
			if s.Content.Media != nil {
				media = append(media, s)
			}
		}
		eq(t, len(media), 1)
		m := media[0]
		eq(t, []any{m.Content.Text, m.Content.Media.Kind, m.Content.Media.MimeType, m.Content.Media.Filename, m.Content.Media.Bytes, m.Quoted.Key.ID},
			[]any{"*Inhaler* dengan spacer", db.MediaImage, "image/jpeg", "inhaler.jpg", []byte{1, 2, 3}, "IN1"})
		eq(t, res.ChannelMsgID != "", true)
		eq(t, lastSent(t, e.sock()).Content.Media != nil, true)
	})
	t.Run("sends a voice note without a caption and refuses media without bytes", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		_, err := r.Channel.Send(bg, ownerJID, db.OutboxPayload{Text: "ignored", Media: &db.OutboxMedia{ID: "med_2", Kind: db.MediaVoice, MimeType: "audio/ogg", Bytes: []byte("OggS")}})
		testutil.Must(t, err)
		s := lastSent(t, e.sock())
		eq(t, []any{s.Content.Text, s.Content.Media.Kind}, []any{"", db.MediaVoice})
		_, err = r.Channel.Send(bg, ownerJID, db.OutboxPayload{Media: &db.OutboxMedia{ID: "med_3", Kind: db.MediaImage}})
		if err == nil {
			t.Fatal("sent media without bytes")
		}
	})
}

func writeLock(t *testing.T, dir, name, info string, ageMs int64) string {
	t.Helper()
	path := whatsapp.LockPathFor(dir, name)
	testutil.Must(t, os.MkdirAll(dir+"/wa", 0o755))
	testutil.Must(t, os.WriteFile(path, []byte(info), 0o644))
	if ageMs > 0 {
		at := time.Now().Add(-time.Duration(ageMs) * time.Millisecond)
		testutil.Must(t, os.Chtimes(path, at, at))
	}
	return path
}

func TestAuthLock(t *testing.T) {
	t.Run("refuses a second runtime on the same store until the first stops", func(t *testing.T) {
		dir := t.TempDir()
		a := setup(t, opts{dataDir: dir})
		ra := a.mustStart(t)
		b := setup(t, opts{dataDir: dir})
		_, err := b.start(t)
		var locked *whatsapp.AuthLockedError
		if !errors.As(err, &locked) {
			t.Fatal(err)
		}
		eq(t, b.ctx.LastStatus(), "error")
		eq(t, b.socketCount(), 0)
		testutil.Must(t, ra.Stop(context.Background()))
		b.mustStart(t)
		eq(t, b.socketCount(), 1)
	})
	t.Run("recovers a lock whose heartbeat stopped over 2 minutes ago", func(t *testing.T) {
		dir := t.TempDir()
		writeLock(t, dir, "whatsapp", `{"pid":424242,"nonce":"old","startedAt":1}`, whatsapp.StaleAfterMs+1000)
		e := setup(t, opts{dataDir: dir})
		e.mustStart(t)
		eq(t, e.socketCount(), 1)
	})
	t.Run("respects a fresh lock with our own pid (another container is pid 1 too)", func(t *testing.T) {
		dir := t.TempDir()
		path := writeLock(t, dir, "x", `{"pid":`+jsonx.NumberString(float64(os.Getpid()))+`,"nonce":"other-container","startedAt":1}`, 0)
		_, err := whatsapp.AcquireLock(path, whatsapp.LockOptions{})
		var locked *whatsapp.AuthLockedError
		eq(t, errors.As(err, &locked), true)
		eq(t, exists(path), true)
	})
	t.Run("respects a fresh lock even when its pid isn't visible here", func(t *testing.T) {
		dir := t.TempDir()
		path := writeLock(t, dir, "y", `{"pid":4242,"nonce":"n","startedAt":1}`, 0)
		_, err := whatsapp.AcquireLock(path, whatsapp.LockOptions{})
		if err == nil || !strings.Contains(err.Error(), "pid 4242") {
			t.Fatal(err)
		}
	})
	t.Run("keeps a held lock fresh with the heartbeat and frees it on release", func(t *testing.T) {
		dir := t.TempDir()
		path := writeLock(t, dir, "z", `{"pid":1,"nonce":"crashed","startedAt":1}`, whatsapp.StaleAfterMs+1000)
		clock := time.Now().UnixMilli()
		fake := testutil.NewFakeClock(clock)
		lock, err := whatsapp.AcquireLock(path, whatsapp.LockOptions{Now: func() int64 { return clock }, Timers: fake})
		testutil.Must(t, err)
		// Three minutes later, with the heartbeat running, nobody else can take it.
		clock += 3 * 60_000
		lock.Touch()
		_, err = whatsapp.AcquireLock(path, whatsapp.LockOptions{Now: func() int64 { return clock }})
		eq(t, err != nil, true)
		_, err = whatsapp.AcquireLock(path, whatsapp.LockOptions{Now: func() int64 { return clock + 60_000 }})
		eq(t, err != nil, true)
		lock.Release()
		eq(t, exists(path), false)
		eq(t, fake.Pending(), 0)
	})
	t.Run("the heartbeat runs every 30 s", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.LockPathFor(dir, "h")
		clock := time.Now().UnixMilli() + 10*60_000
		fake := testutil.NewFakeClock(0)
		lock, err := whatsapp.AcquireLock(path, whatsapp.LockOptions{Now: func() int64 { return clock }, Timers: fake})
		testutil.Must(t, err)
		defer lock.Release()
		eq(t, fake.Armed(), []int64(nil))
		fake.Advance(whatsapp.HeartbeatMs, nil)
		st, _ := os.Stat(path)
		eq(t, st.ModTime().UnixMilli(), clock)
	})
	t.Run("lets a new process take over once the holder's heartbeat stops", func(t *testing.T) {
		dir := t.TempDir()
		path := writeLock(t, dir, "w", `{"pid":1,"nonce":"old","startedAt":1}`, whatsapp.StaleAfterMs-10_000)
		_, err := whatsapp.AcquireLock(path, whatsapp.LockOptions{})
		eq(t, err != nil, true)
		later := time.Now().UnixMilli() + 20_000
		lock, err := whatsapp.AcquireLock(path, whatsapp.LockOptions{Now: func() int64 { return later }})
		testutil.Must(t, err)
		eq(t, exists(path), true)
		lock.Release()
	})
	t.Run("a session wipe never removes the lock", func(t *testing.T) {
		dir := t.TempDir()
		e, r := openRunning(t, opts{dataDir: dir})
		_ = e
		writeStore(t, whatsapp.StorePathFor(dir, "whatsapp"), true)
		act(t, r, "unlink", nil)
		eq(t, exists(whatsapp.StorePathFor(dir, "whatsapp")), false)
		eq(t, exists(whatsapp.LockPathFor(dir, "whatsapp")), true)
	})
}

func TestUnlink(t *testing.T) {
	t.Run("logs out and deletes the store", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.StorePathFor(dir, "whatsapp")
		writeStore(t, path, true)
		e, r := openRunning(t, opts{dataDir: dir})
		before := len(e.ctx.StatusList())
		eq(t, act(t, r, "unlink", nil), map[string]any{"ok": true})
		eq(t, e.sock().LoggedOut(), true)
		eq(t, exists(path), false)
		l := live(r)
		eq(t, []any{l["connection"], l["linked"], l["linkedNumber"]}, []any{"closed", false, nil})
		eq(t, len(e.ctx.StatusList()), before)
		conntest.Settle()
		eq(t, e.socketCount(), 1)
	})
	t.Run("refuses unknown actions", func(t *testing.T) {
		_, r := openRunning(t, opts{})
		if _, err := r.Action(context.Background(), "nope", nil); err == nil || err.Error() != "Unknown WhatsApp action: nope" {
			t.Fatal(err)
		}
	})
	t.Run("onRemoved deletes the store of a removed WhatsApp connector only", func(t *testing.T) {
		dir := t.TempDir()
		path := whatsapp.StorePathFor(dir, "whatsapp")
		writeStore(t, path, true)
		other := whatsapp.StorePathFor(dir, "telegram")
		writeStore(t, other, true)
		rm := whatsapp.RemoveConnectorFiles(dir)
		testutil.Must(t, rm(db.Connector{ID: "telegram", Type: "telegram"}, false))
		eq(t, exists(other), true)
		testutil.Must(t, rm(db.Connector{ID: "whatsapp", Type: "whatsapp"}, true))
		eq(t, exists(path), false)
		eq(t, exists(path+"-wal"), false)
	})
}

func TestTestAction(t *testing.T) {
	e := setup(t, opts{config: cfg()})
	tr, _ := e.rt.Test(e.ctx.Context)
	eq(t, tr, connectors.TestResult{OK: false, Detail: "Enter the owner's phone number first."})
	e = setup(t, opts{config: cfg("ownerPhone", "0812-3456-789", "linkedNumber", "628000001472")})
	tr, _ = e.rt.Test(e.ctx.Context)
	eq(t, tr, connectors.TestResult{OK: true, Detail: "Not paired yet. Start pairing to link the number.", Data: cfg("linked", false)})
	writeStore(t, e.storePath(), true)
	tr, _ = e.rt.Test(e.ctx.Context)
	eq(t, tr, connectors.TestResult{OK: true, Detail: "Linked as +628000001472", Data: cfg("linked", true)})
}

func TestQuotes(t *testing.T) {
	t.Run("reads captions, media labels and ignores a quote inside the quote", func(t *testing.T) {
		eq(t, whatsapp.QuoteOf(&waE2E.Message{Conversation: proto.String("plain")}) == nil, true)
		eq(t, whatsapp.QuoteOf(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("lihat"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q1"), QuotedMessage: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf")}}}}}),
			&whatsapp.QuoteInfo{ID: "Q1", Text: "", Media: "[document: a.pdf]"})
		eq(t, whatsapp.QuoteOf(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("x"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q2"), QuotedMessage: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}}}}).Media, "[voice note]")
		nested := whatsapp.QuoteOf(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("x"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q3"), QuotedMessage: &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("lapis dua"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q0"), QuotedMessage: &waE2E.Message{Conversation: proto.String("lapis satu")}}},
		}}}})
		eq(t, nested, &whatsapp.QuoteInfo{ID: "Q3", Text: "lapis dua"})
		wrapped := whatsapp.QuoteOf(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("x"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q4"), QuotedMessage: &waE2E.Message{
			EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("sementara")}},
		}}}})
		eq(t, wrapped.Text, "sementara")
	})
	t.Run("tells self, sender and others apart across device suffixes and LIDs", func(t *testing.T) {
		self := []string{"628000001472:3@s.whatsapp.net", "111222333:3@lid"}
		sender := []string{ownerJID, ownerLID}
		eq(t, whatsapp.QuoteAuthor("628000001472@s.whatsapp.net", self, sender, false), "self")
		eq(t, whatsapp.QuoteAuthor("111222333@lid", self, sender, false), "self")
		eq(t, whatsapp.QuoteAuthor("628123456789:7@s.whatsapp.net", self, sender, false), "sender")
		eq(t, whatsapp.QuoteAuthor("620000000@s.whatsapp.net", self, sender, false), "other")
		eq(t, whatsapp.QuoteAuthor("999@lid", self, sender, true), "sender")
		eq(t, whatsapp.QuoteAuthor("", self, sender, false), "unknown")
	})
}

func TestSessionStates(t *testing.T) {
	dir := t.TempDir()
	path := whatsapp.StorePathFor(dir, "whatsapp")
	eq(t, whatsapp.InspectSession(path), whatsapp.SessionNone)
	testutil.Must(t, os.MkdirAll(dir+"/wa", 0o755))
	testutil.Must(t, os.WriteFile(path, nil, 0o644))
	eq(t, whatsapp.InspectSession(path), whatsapp.SessionIncomplete)
	testutil.Must(t, os.Remove(path))
	writeStore(t, path, false)
	eq(t, whatsapp.ClearIncompleteSession(path), whatsapp.SessionIncomplete)
	eq(t, exists(path), false)
	writeStore(t, path, true)
	eq(t, whatsapp.ClearIncompleteSession(path), whatsapp.SessionLinked)
	eq(t, exists(path), true)
	testutil.Must(t, whatsapp.WipeSession(path))
	eq(t, whatsapp.InspectSession(path), whatsapp.SessionNone)
}

func TestGroupParticipants(t *testing.T) {
	const (
		grp     = "120363000000000001@g.us"
		other   = "120363000000000002@g.us"
		selfPN  = "628000001472@s.whatsapp.net"
		selfLID = "111222333@lid"
		aniPN   = "6281200000001@s.whatsapp.net"
		aniLID  = "100000000000001@lid"
		budiPN  = "6281200000002@s.whatsapp.net"
		budiLID = "100000000000002@lid"
	)
	lister := func(r *connectors.RunningConnector) connectors.GroupLister {
		l, ok := r.Channel.(connectors.GroupLister)
		if !ok {
			t.Fatal("whatsapp channel is not a GroupLister")
		}
		return l
	}
	members := func(s *watest.FakeSocket) {
		s.GroupMembers[grp] = []whatsapp.GroupMember{
			{ID: selfLID, Alt: selfPN},
			{ID: aniPN, Alt: aniLID},
			{ID: budiLID}, // LID only: the phone comes from the local store
		}
		s.GroupMembers[other] = []whatsapp.GroupMember{{ID: selfPN}, {ID: aniPN}}
		s.SetLID(budiLID, budiPN)
	}
	ctx := context.Background()

	t.Run("lists without this account and pairs phone and LID", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		members(e.sock())
		got, err := lister(r).GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, got, []connectors.Participant{{ExternalID: aniPN, AltID: aniLID}, {ExternalID: budiLID, AltID: budiPN}})
	})

	t.Run("caches for 10 minutes", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		e.clock.Set(e.clock.Now() + 9*60_000)
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 1)
		e.clock.Set(e.clock.Now() + 2*60_000)
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
	})

	t.Run("a group change drops only that group", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		for _, g := range []string{grp, other} {
			_, err := l.GroupParticipants(ctx, g)
			testutil.Must(t, err)
		}
		s.EmitGroupChange(other)
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
		s.EmitGroupChange(grp)
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 3)
	})

	t.Run("a change during the fetch keeps the stale list out", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		s.DuringGroupParticipants = func(jid string) {
			s.DuringGroupParticipants = nil
			s.EmitGroupChange(jid)
		}
		if _, err := l.GroupParticipants(ctx, grp); err == nil {
			t.Fatal("a list fetched across a group change passed")
		}
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
	})

	t.Run("a member writing during the fetch keeps it, a stranger doesn't", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		write := func(id, participant string, n int) {
			s.Message(dm(id, nil, func(k *whatsapp.Key) { k.RemoteJID, k.Participant = grp, participant }), "")
			conntest.Until(t, func() bool { return e.ctx.InboundCount() == n })
		}
		s.DuringGroupParticipants = func(string) {
			s.DuringGroupParticipants = nil
			write("M1", aniPN, 1)
		}
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 1)

		s.EmitGroupChange(grp)
		s.DuringGroupParticipants = func(string) {
			s.DuringGroupParticipants = nil
			write("M2", "6281200000003@s.whatsapp.net", 2)
		}
		if _, err := l.GroupParticipants(ctx, grp); err == nil {
			t.Fatal("a list that misses a sender seen during the fetch passed")
		}
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 3)
	})

	t.Run("a canceled fetch is not cached", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		cctx, cancel := context.WithCancel(ctx)
		s.DuringGroupParticipants = func(string) {
			s.DuringGroupParticipants = nil
			cancel()
		}
		if _, err := l.GroupParticipants(cctx, grp); err == nil {
			t.Fatal("a canceled fetch passed")
		}
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
	})

	t.Run("reconnecting clears the cache", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		s.OpenConn("")
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
	})

	t.Run("a sender outside the list drops the group", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		// A known participant writing keeps the entry.
		s.Message(dm("K1", nil, func(k *whatsapp.Key) { k.RemoteJID, k.Participant = grp, aniLID }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 1)
		s.Message(dm("K2", nil, func(k *whatsapp.Key) { k.RemoteJID, k.Participant = grp, "6281200000003@s.whatsapp.net" }), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		_, err = l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)
	})

	t.Run("errors are never cached", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		s := e.sock()
		members(s)
		l := lister(r)
		s.GroupMembersErr[grp] = errors.New("rate limited")
		if _, err := l.GroupParticipants(ctx, grp); err == nil {
			t.Fatal("socket error passed")
		}
		delete(s.GroupMembersErr, grp)
		_, err := l.GroupParticipants(ctx, grp)
		testutil.Must(t, err)
		eq(t, s.GroupMemberCalls(), 2)

		// A list without this account is unknown.
		s.GroupMembers[other] = []whatsapp.GroupMember{{ID: aniPN}}
		if _, err := l.GroupParticipants(ctx, other); err == nil {
			t.Fatal("list without self passed")
		}
		if _, err := l.GroupParticipants(ctx, other); err == nil {
			t.Fatal("list without self passed")
		}
		eq(t, s.GroupMemberCalls(), 4)

		// A DM jid.
		if _, err := l.GroupParticipants(ctx, aniPN); err == nil {
			t.Fatal("dm passed")
		}
		eq(t, s.GroupMemberCalls(), 4)

		// Socket down: no cache, even a fresh one.
		s.Close(500)
		if _, err := l.GroupParticipants(ctx, grp); err == nil || err.Error() != whatsapp.ReasonNotConnected {
			t.Fatal(err)
		}
		eq(t, s.GroupMemberCalls(), 4)
	})
}

// Outgoing @mentions (ADR 0002). rpt_0mwrwosczfzc3p58slq: in a LID group the
// agent couldn't tag anyone; now an "@<id>" of someone in the group goes out
// as a real mention.
func TestSendMentions(t *testing.T) {
	const (
		grp     = "120363000000000001@g.us"
		aniLID  = "100000000000001@lid"
		budiLID = "100000000000002@lid"
		citaPN  = "6281200000003@s.whatsapp.net"
	)
	members := func(s *watest.FakeSocket) {
		s.GroupMembers[grp] = []whatsapp.GroupMember{
			{ID: "111222333@lid", Alt: "628000001472@s.whatsapp.net"},
			{ID: aniLID},
			{ID: budiLID},
			{ID: citaPN},
		}
	}
	ctx := context.Background()

	t.Run("the reported case: tokens of people in a LID group become mentions", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		members(e.sock())
		_, err := r.Channel.Send(ctx, grp, db.OutboxPayload{Text: "Siap, @100000000000001 sama @100000000000002 dicolek ya", Format: "markdown"})
		testutil.Must(t, err)
		s := lastSent(t, e.sock())
		eq(t, s.Content.Text, "Siap, @100000000000001 sama @100000000000002 dicolek ya")
		eq(t, s.Content.Mentions, []string{aniLID, budiLID})
	})

	t.Run("a name or an unknown id stays plain text", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		members(e.sock())
		_, err := r.Channel.Send(ctx, grp, db.OutboxPayload{Text: "@Ani dan @100000000000009, sama @6281200000003", Format: "plain"})
		testutil.Must(t, err)
		eq(t, lastSent(t, e.sock()).Content.Mentions, []string{citaPN})
	})

	t.Run("each chunk of a long message carries only its own mentions", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		members(e.sock())
		long := "@100000000000001 " + strings.Repeat("kata ", whatsapp.TextLimit/5) + "\n\n@100000000000002 selesai"
		_, err := r.Channel.Send(ctx, grp, db.OutboxPayload{Text: long, Format: "plain"})
		testutil.Must(t, err)
		l := e.sock().SentList()
		eq(t, len(l) >= 2, true)
		eq(t, l[0].Content.Mentions, []string{aniLID})
		eq(t, l[len(l)-1].Content.Mentions, []string{budiLID})
	})

	t.Run("a photo caption mentions too", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		members(e.sock())
		_, err := r.Channel.Send(ctx, grp, db.OutboxPayload{Text: "buat @100000000000002", Format: "plain", Media: &db.OutboxMedia{Kind: db.MediaImage, MimeType: "image/png", Bytes: []byte{1}}})
		testutil.Must(t, err)
		eq(t, lastSent(t, e.sock()).Content.Mentions, []string{budiLID})
	})

	t.Run("no list, no mentions: the text still goes out", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		e.sock().GroupMembersErr[grp] = errors.New("boom")
		_, err := r.Channel.Send(ctx, grp, db.OutboxPayload{Text: "@100000000000001 halo", Format: "plain"})
		testutil.Must(t, err)
		s := lastSent(t, e.sock())
		eq(t, s.Content.Text, "@100000000000001 halo")
		eq(t, len(s.Content.Mentions), 0)
	})

	t.Run("a DM never looks the group up", func(t *testing.T) {
		e, r := openRunning(t, opts{})
		_, err := r.Channel.Send(ctx, ownerJID, db.OutboxPayload{Text: "@628123456789 halo", Format: "plain"})
		testutil.Must(t, err)
		eq(t, len(lastSent(t, e.sock()).Content.Mentions), 0)
		eq(t, e.sock().GroupMemberCalls(), 0)
	})
}

// rpt_0mwsqf7y5vjh0reoc1a: a reply to a photo album quoted the album message,
// which carries no text or media, and the album's photos were group talk
// whose bytes were never fetched, so the agent saw nothing.
func TestAlbumsAndKeptGroupPhotos(t *testing.T) {
	const grp = "120363000000000009@g.us"
	albumPhoto := func(id, parent string, caption string) *whatsapp.Message {
		img := &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(2048)}
		if caption != "" {
			img.Caption = proto.String(caption)
		}
		msg := &waE2E.Message{ImageMessage: img}
		if parent != "" {
			msg.MessageContextInfo = &waE2E.MessageContextInfo{MessageAssociation: &waE2E.MessageAssociation{
				AssociationType:  waE2E.MessageAssociation_MEDIA_ALBUM.Enum(),
				ParentMessageKey: &waCommon.MessageKey{ID: proto.String(parent)},
			}}
		}
		return dm(id, msg, func(k *whatsapp.Key) { k.RemoteJID, k.Participant = grp, "777000@lid" })
	}

	t.Run("AlbumOf reads the album id, also through a wrapper", func(t *testing.T) {
		eq(t, whatsapp.AlbumOf(albumPhoto("A1", "ALB", "").Message), "ALB")
		eq(t, whatsapp.AlbumOf(albumPhoto("A1", "", "").Message), "")
		wrapped := &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: albumPhoto("A1", "ALB2", "").Message}}
		eq(t, whatsapp.AlbumOf(wrapped), "ALB2")
	})

	t.Run("a quoted album says it's an album", func(t *testing.T) {
		q := whatsapp.QuoteOf(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("in apa"), ContextInfo: &waE2E.ContextInfo{
			StanzaID: proto.String("ALB"), Participant: proto.String("62811000777@s.whatsapp.net"),
			QuotedMessage: &waE2E.Message{AlbumMessage: &waE2E.AlbumMessage{ExpectedImageCount: proto.Uint32(3)}},
		}}})
		eq(t, []string{q.ID, q.Text, q.Media}, []string{"ALB", "", "[album of 3]"})
	})

	t.Run("fetches a group talk photo the engine keeps and passes its album", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.ctx.Services.WantsMedia = func(connectors.InboundMessage) bool { return false }
		e.ctx.Services.KeepsMedia = func(m connectors.InboundMessage) bool { return m.ChatKind == "group" }
		e.sock().Message(albumPhoto("A1", "ALB", ""), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.AlbumID, in.Text, in.Media}, []any{"ALB", "", []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString([]byte("media:A1"))}}})
		eq(t, e.sock().Downloads(), []string{"A1"})
	})

	t.Run("doesn't fetch group talk stickers, oversized photos, or anything the engine won't keep", func(t *testing.T) {
		e, _ := openRunning(t, opts{})
		e.ctx.Services.WantsMedia = func(connectors.InboundMessage) bool { return false }
		keep := true
		e.ctx.Services.KeepsMedia = func(connectors.InboundMessage) bool { return keep }
		e.sock().Message(dm("ST1", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}}, func(k *whatsapp.Key) { k.RemoteJID, k.Participant = grp, "777000@lid" }), "")
		big := albumPhoto("BIG", "", "")
		big.Message.ImageMessage.FileLength = proto.Uint64(connectors.ContextPhotoMaxBytes + 1)
		e.sock().Message(big, "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 2 })
		keep = false
		e.sock().Message(albumPhoto("OFF", "", ""), "")
		conntest.Until(t, func() bool { return e.ctx.InboundCount() == 3 })
		eq(t, e.sock().Downloads(), []string(nil))
		eq(t, e.ctx.Inbound()[2].Media, []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg"}})
	})
}

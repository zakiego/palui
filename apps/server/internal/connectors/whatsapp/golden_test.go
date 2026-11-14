package whatsapp_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type baileysMessage struct {
	Key struct {
		RemoteJid      string `json:"remoteJid"`
		RemoteJidAlt   string `json:"remoteJidAlt"`
		Participant    string `json:"participant"`
		ParticipantAlt string `json:"participantAlt"`
		FromMe         bool   `json:"fromMe"`
		ID             string `json:"id"`
	} `json:"key"`
	Message          json.RawMessage `json:"message"`
	MessageTimestamp int64           `json:"messageTimestamp"`
	PushName         string          `json:"pushName"`
}

type waGolden struct {
	WaInbound []struct {
		Lids      map[string]string `json:"lids"`
		Name      string            `json:"name"`
		Msg       baileysMessage    `json:"msg"`
		Type      string            `json:"type"`
		Inbound   []map[string]any  `json:"inbound"`
		Downloads []string          `json:"downloads"`
	} `json:"waInbound"`
	WaSends []struct {
		Name    string           `json:"name"`
		Payload db.OutboxPayload `json:"payload"`
		Sent    []struct {
			JID     string          `json:"jid"`
			Content json.RawMessage `json:"content"`
			Quoted  *string         `json:"quoted"`
		} `json:"sent"`
		Result struct {
			ChannelMsgID *string `json:"channelMsgId"`
		} `json:"result"`
	} `json:"waSends"`
}

// Baileys' WAProto and whatsmeow's waE2E share field numbers, but a few JSON
// names differ in case.
var protoNames = map[string]string{"stanzaId": "stanzaID", "mentionedJid": "mentionedJID", "remoteJid": "remoteJID", "ptt": "PTT"}

func renameKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if n, ok := protoNames[k]; ok {
				k = n
			}
			out[k] = renameKeys(val)
		}
		return out
	case []any:
		for i := range x {
			x[i] = renameKeys(x[i])
		}
	}
	return v
}

// ToWhatsmeow turns a Baileys message (as JSON) into the runtime's message
// with a whatsmeow protobuf.
func toWhatsmeow(t *testing.T, b baileysMessage) *whatsapp.Message {
	t.Helper()
	var raw any
	testutil.Must(t, json.Unmarshal(b.Message, &raw))
	renamed, _ := json.Marshal(renameKeys(raw))
	msg := &waE2E.Message{}
	testutil.Must(t, protojson.Unmarshal(renamed, msg))
	return &whatsapp.Message{
		Key:       whatsapp.Key{RemoteJID: b.Key.RemoteJid, RemoteJIDAlt: b.Key.RemoteJidAlt, Participant: b.Key.Participant, ParticipantAlt: b.Key.ParticipantAlt, FromMe: b.Key.FromMe, ID: b.Key.ID},
		Message:   msg,
		PushName:  b.PushName,
		Timestamp: b.MessageTimestamp,
	}
}

func loadWAGolden(t *testing.T) waGolden {
	raw, err := os.ReadFile("../../../testdata/channels.json")
	testutil.Must(t, err)
	var g waGolden
	testutil.Must(t, json.Unmarshal(raw, &g))
	return g
}

// canonInbound compares by content: Go omits addressed:false where TS wrote it.
func canonInbound(list []map[string]any, noTime bool) string {
	for _, o := range list {
		if a, ok := o["addressed"]; ok && a == false {
			delete(o, "addressed")
		}
		if noTime {
			o["at"] = 0
		}
	}
	if list == nil {
		list = []map[string]any{}
	}
	b, _ := json.Marshal(list)
	v, _ := jsonx.Parse(b)
	return jsonx.Canonical(v)
}

func TestGoldenInbound(t *testing.T) {
	g := loadWAGolden(t)
	if len(g.WaInbound) < 50 {
		t.Fatal("too few steps")
	}
	e, _ := openRunning(t, opts{})
	s := e.sock()
	s.Media = func(m *whatsapp.Message) ([]byte, error) {
		switch m.Key.ID {
		case "BIGDOC":
			return make([]byte, 1024*1024+1), nil
		case "BIGIMG":
			return make([]byte, 16*1024*1024+1), nil
		}
		return []byte("media:" + m.Key.ID), nil
	}
	withInbound := 0
	defer func() {
		if withInbound < 40 {
			t.Errorf("only %d steps produced inbound messages", withInbound)
		}
	}()
	for _, step := range g.WaInbound {
		withInbound += min(len(step.Inbound), 1)
		if step.Lids != nil {
			for k, v := range step.Lids {
				s.SetLID(k, v)
			}
			continue
		}
		before, dl := e.ctx.InboundCount(), len(s.Downloads())
		s.Message(toWhatsmeow(t, step.Msg), step.Type)
		if len(step.Inbound) > 0 {
			conntest.Until(t, func() bool { return e.ctx.InboundCount() >= before+len(step.Inbound) })
		}
		conntest.Settle()
		var got []map[string]any
		for _, m := range e.ctx.Inbound()[before:] {
			b, _ := json.Marshal(m)
			var o map[string]any
			_ = json.Unmarshal(b, &o)
			got = append(got, o)
		}
		noTime := step.Msg.MessageTimestamp == 0
		if gs, ws := canonInbound(got, noTime), canonInbound(step.Inbound, noTime); gs != ws {
			t.Errorf("%s:\n got  %s\n want %s", step.Name, gs, ws)
		}
		if gd, wd := jsonx.Stringify(s.Downloads()[dl:]), jsonx.Stringify(step.Downloads); gd != wd && !(len(step.Downloads) == 0 && len(s.Downloads()) == dl) {
			t.Errorf("%s: downloads %s, want %s", step.Name, gd, wd)
		}
	}
}

func TestGoldenSends(t *testing.T) {
	g := loadWAGolden(t)
	e, r := openRunning(t, opts{})
	s := e.sock()
	in := dm("IN1", nil, nil)
	s.Message(in, "")
	conntest.Until(t, func() bool { return e.ctx.InboundCount() == 1 })
	for _, c := range g.WaSends {
		before := len(s.SentList())
		res, err := r.Channel.Send(context.Background(), ownerJID, c.Payload)
		testutil.Must(t, err)
		sent := s.SentList()[before:]
		if len(sent) != len(c.Sent) {
			t.Errorf("%s: %d sent, want %d", c.Name, len(sent), len(c.Sent))
			continue
		}
		for i, x := range sent {
			want := c.Sent[i]
			b, _ := json.Marshal(x.Content)
			gc, _ := jsonx.Parse(b)
			wc, _ := jsonx.Parse(want.Content)
			var q *string
			if x.Quoted != nil {
				q = &x.Quoted.Key.ID
			}
			if x.JID != want.JID || jsonx.Canonical(gc) != jsonx.Canonical(wc) || jsonx.Stringify(q) != jsonx.Stringify(want.Quoted) {
				t.Errorf("%s chunk %d:\n got  %s %s %v\n want %s %s %v", c.Name, i, x.JID, jsonx.Canonical(gc), jsonx.Stringify(q), want.JID, jsonx.Canonical(wc), jsonx.Stringify(want.Quoted))
			}
		}
		if c.Result.ChannelMsgID != nil && res.ChannelMsgID != *c.Result.ChannelMsgID {
			t.Errorf("%s: channelMsgId %q, want %q", c.Name, res.ChannelMsgID, *c.Result.ChannelMsgID)
		}
	}
}

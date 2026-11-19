package chats_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	chatsconn "github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// A contact card shared in a group hours earlier was out of the model's
// window and there was no way to look it up (rpt_0mwmwtby5l0zrpy3sgg).
func TestSearchThisChatFindsAnEarlierContactCardOnlyInThisChat(t *testing.T) {
	d := testutil.DB(t)
	group := ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000009@g.us", Title: "Teman", Kind: "group"}, NOW-1000*MIN)
	other := ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000010@g.us", Title: "Lain", Kind: "group"}, NOW-1000*MIN)
	addFrom(t, d, group, "user", "[contact: Primadona ✂️, +62 812-0000-1111]", NOW-900*MIN, "Owner Name", true, nil)
	addFrom(t, d, group, "user", "kd kepanjangan lah jalurnya", NOW-899*MIN, "Guest A", false, nil)
	quote := jsonx.NewMap[any]()
	quote.Set("from", "other")
	quote.Set("text", "")
	quote.Set("media", "[contact: Primadona ✂️, +62 812-0000-1111]")
	extra := jsonx.NewMap[any]()
	extra.Set("quoted", quote)
	addFrom(t, d, group, "user", "wait", NOW-898*MIN, "Guest A", false, extra)
	addFrom(t, d, group, "user", "ngobrol lain", NOW-10*MIN, "Guest A", false, nil)
	addFrom(t, d, other, "user", "[contact: Primadona ✂️, +62 899-0000-9999]", NOW-5*MIN, "Owner Name", true, nil)

	turn := connectors.TurnInfo{RunID: "run_1", ChatID: group.ID, Channel: "whatsapp", Trigger: "message", Mode: "outside", TZ: WITA, Now: NOW}
	set := runtimeTools(t, d, turn)
	pt, ok := set.Get(chatsconn.ThisChatTool)
	if !ok {
		t.Fatal(set.Keys())
	}
	if pt.Class != "read" || !pt.Untrusted || !pt.GuestSafe || pt.Data != connectors.DataChat {
		t.Fatalf("%+v", pt)
	}
	got := conntest.Call(t, set, chatsconn.ThisChatTool, `{"query":"PRIMADONA"}`)
	b, _ := json.Marshal(got)
	s := string(b)
	for _, want := range []string{`"count":2`, `+62 812-0000-1111`, `"from":"owner (Owner Name)"`, `"from":"Guest A"`, `"replyTo":"[contact: Primadona`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "899-0000-9999") {
		t.Fatalf("leaked another chat: %s", s)
	}

	none := conntest.Call(t, set, chatsconn.ThisChatTool, `{"query":"bengkel"}`)
	if b, _ := json.Marshal(none); !strings.Contains(string(b), `"count":0`) {
		t.Fatal(string(b))
	}
}

func TestSearchThisChatIsNotOfferedToAHookTurnOrWithoutAChat(t *testing.T) {
	d := testutil.DB(t)
	group := ensure(t, d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120000000000000009@g.us", Title: "Teman", Kind: "group"}, NOW)
	for _, turn := range []connectors.TurnInfo{
		{ChatID: group.ID, Trigger: "hook"},
		{ChatID: "", Trigger: "message"},
	} {
		if _, ok := runtimeTools(t, d, turn).Get(chatsconn.ThisChatTool); ok {
			t.Fatalf("%+v", turn)
		}
	}
}

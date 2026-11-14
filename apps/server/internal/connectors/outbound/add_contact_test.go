package outbound_test

// add_contact through a real turn: the engine, the approval prompt and the
// owner's answer, with the real outbound runtime (add-contact.test.ts).

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	outboundconn "github.com/zakiego/palui/apps/server/internal/connectors/outbound"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const ask = "tambah kontak Ana +62 822-0000-1002"

var addArgs = map[string]any{"name": "Ana", "phone": "+62 822-0000-1002"}

func turnHarness(t *testing.T, steps []models.Step, prep func(d *db.DB)) *chattest.Harness {
	d := testutil.DB(t)
	chattest.OwnTelegram(t, d)
	chattest.ModelChain(t, d, "cliproxy")
	if prep != nil {
		prep(d)
	}
	m := models.NewScripted(steps...)
	return chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{
		"cliproxy": chattest.Model("cliproxy", m),
		"telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram")),
		"outbound": outboundconn.Runtime,
	}, Rows: []chattest.Row{{"cliproxy", "model"}, {"telegram", "channel"}, {"outbound", "skill"}}})
}

func send(t *testing.T, h *chattest.Harness, msg connectors.InboundMessage) {
	t.Helper()
	_, err := h.Engine.Inbound(msg)
	testutil.Must(t, err)
	h.Engine.Idle()
}

func callback(data string) connectors.InboundMessage {
	m := conntest.TgMessage("")
	m.Callback = &connectors.InboundCallback{Data: data}
	return m
}

func approval(t *testing.T, d *db.DB) (id, tool, status, preview string) {
	t.Helper()
	testutil.Must(t, d.QueryRow("SELECT id, tool, status, args_preview FROM pending_approvals").Scan(&id, &tool, &status, &preview))
	return
}

func TestAddContactInATurnAsksWithTheNameAndNormalizedNumberAndSavesOnlyAfterYes(t *testing.T) {
	h := turnHarness(t, []models.Step{models.CallTool("outbound__add_contact", addArgs, "c1"), models.Say("udah disimpan")}, nil)
	send(t, h, conntest.TgMessage(ask))
	id, tool, status, preview := approval(t, h.DB)
	if tool != "outbound__add_contact" || status != "pending" || preview != "Tambah kontak Ana (+62 822-0000-1002)\nPersetujuan kirim: selalu tanya" {
		t.Fatal(tool, status, preview)
	}
	var payload string
	testutil.Must(t, h.DB.QueryRow("SELECT payload FROM outbox WHERE source = ?", "approval:"+id).Scan(&payload))
	p, _ := jsonx.ParseObject([]byte(payload))
	text, _ := p.Get("text")
	if !strings.Contains(text.(string), "Tambah kontak Ana (+62 822-0000-1002)") {
		t.Fatal(text)
	}
	if list, _ := outbound.ListContacts(h.DB, nil); len(list) != 0 {
		t.Fatal("saved before yes")
	}
	send(t, h, callback("approve:"+id))
	def, err := agents.GetDefault(h.DB, 1)
	testutil.Must(t, err)
	list, _ := outbound.ListContacts(h.DB, nil)
	if len(list) != 1 || list[0].Name != "Ana" || list[0].Address != "6282200001002" || list[0].Policy != "ask" || list[0].AgentID == nil || *list[0].AgentID != def.ID {
		t.Fatalf("%+v", list)
	}
}

func TestAddContactInATurnARejectionLeavesNothingSaved(t *testing.T) {
	h := turnHarness(t, []models.Step{models.CallTool("outbound__add_contact", addArgs, "c1"), models.Say("oke, gak jadi")}, nil)
	send(t, h, conntest.TgMessage(ask))
	id, _, _, _ := approval(t, h.DB)
	send(t, h, callback("deny:"+id))
	_, _, status, _ := approval(t, h.DB)
	if status != "denied" {
		t.Fatal(status)
	}
	if list, _ := outbound.ListContacts(h.DB, nil); len(list) != 0 {
		t.Fatal("saved")
	}
}

func TestAddContactInATurnStillAsksWhenTheAgentMayRunEveryToolWithoutAsking(t *testing.T) {
	h := turnHarness(t, []models.Step{models.CallTool("outbound__add_contact", addArgs, "c1"), models.Say("x")}, func(d *db.DB) {
		def, err := agents.GetDefault(d, 1)
		testutil.Must(t, err)
		perms := jsonx.NewMap[*string]()
		perms.Set("outbound__add_contact", testutil.Ptr("allow"))
		_, err = agents.SetToolPermissions(d, def.ID, agents.PermissionsPatch{AllowAll: testutil.Ptr(true), Tools: perms}, 1)
		testutil.Must(t, err)
	})
	send(t, h, conntest.TgMessage(ask))
	_, tool, status, _ := approval(t, h.DB)
	if tool != "outbound__add_contact" || status != "pending" {
		t.Fatal(tool, status)
	}
	if list, _ := outbound.ListContacts(h.DB, nil); len(list) != 0 {
		t.Fatal("saved")
	}
}

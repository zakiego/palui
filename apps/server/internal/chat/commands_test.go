package chat_test

// Ports commands.test.ts.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type offer struct {
	id     string
	models []string
	err    error
}

type cmdWorld struct {
	*world
}

// cmdSetup: one model connector per offer (default: cliproxy offering "m"),
// each answering listModels.
func cmdSetup(t *testing.T, steps []models.Step, offers []offer, x extra) *cmdWorld {
	d := testutil.DB(t)
	chattest.OwnTelegram(t, d)
	if offers == nil {
		offers = []offer{{id: "cliproxy", models: []string{"m"}}}
	}
	links := make([]settings.ModelChainLink, len(offers))
	for i, o := range offers {
		links[i] = settings.ModelChainLink{ConnectorID: o.id, Model: "m"}
	}
	testutil.Must(t, settings.Set(d, "modelRoles", settings.ModelRoles{Default: links, Fast: links, Vision: links}, 1))
	m := models.NewScripted(steps...)
	reg := map[string]*connectors.Runtime{"telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}
	rows := []chattest.Row{{"telegram", "channel"}}
	for k, v := range x.registry {
		reg[k] = v
	}
	rows = append(rows, x.rows...)
	for _, o := range offers {
		o := o
		reg[o.id] = conntest.ModelRuntime(o.id, func() models.LanguageModel { return m }, func(h *connectors.RunningConnector) {
			h.Action = func(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error) {
				if o.err != nil {
					return nil, o.err
				}
				list := make([]any, len(o.models))
				for i, s := range o.models {
					list[i] = s
				}
				return objOf("models", list), nil
			}
		})
		rows = append(rows, chattest.Row{o.id, "model"})
	}
	h := chattest.New(t, chattest.Options{DB: d, Registry: reg, Rows: rows})
	return &cmdWorld{&world{Harness: h, t: t, m: m}}
}

func (w *cmdWorld) say(text string, mod ...func(m *connectors.InboundMessage)) *chat.InboundResult {
	w.Clock.Add(1000)
	return w.sendIdle(text, mod...)
}

func TestParseCommand(t *testing.T) {
	c, ok := chat.ParseCommand("/help")
	eq(t, ok, true)
	jsonEq(t, c.Args, `[]`)
	eq(t, c.Name, "help")
	c, ok = chat.ParseCommand("/Tools@palui_bot alpha  beta")
	eq(t, ok, true)
	eq(t, c.Name, "tools")
	jsonEq(t, c.Args, `["alpha","beta"]`)
	if _, ok := chat.ParseCommand("/beres"); ok {
		t.Fatal("beres")
	}
	if _, ok := chat.ParseCommand("help"); ok {
		t.Fatal("help")
	}
}

func TestHelp(t *testing.T) {
	h := cmdSetup(t, nil, []offer{}, extra{})
	h.say("/help")
	text := lastText(t, h.DB)
	for _, c := range []string{"/help", "/status", "/usage", "/new", "/compact", "/stop", "/btw", "/model", "/models", "/tools", "/connectors", "/agent", "/id", "/report"} {
		contains(t, text, "• "+c)
	}
	eq(t, len(allRuns(t, h.DB)), 0)
}

func TestID(t *testing.T) {
	t.Run("shows the chat, session and message ids, and the ids of a quoted message", func(t *testing.T) {
		h := cmdSetup(t, []models.Step{models.Say("halo juga")}, nil, extra{})
		first := h.say("halo palui", func(m *connectors.InboundMessage) { m.ChannelMsgID = "wa-1" })
		c := firstChat(t, h.DB)
		mine := h.say("/id")
		contains(t, lastText(t, h.DB), fmt.Sprintf("Chat: `%s` (telegram, 42)", c.ID))
		contains(t, lastText(t, h.DB), fmt.Sprintf("Sesi: `%s`", *c.SessionID))
		contains(t, lastText(t, h.DB), fmt.Sprintf("Pesan ini: `%s`", mine.MessageID))
		contains(t, lastText(t, h.DB), "Quote sebuah pesan")
		h.say("/id", func(m *connectors.InboundMessage) { m.ReplyToChannelMsgID = "wa-1" })
		run := allRuns(t, h.DB)[0]
		contains(t, lastText(t, h.DB), fmt.Sprintf("• Pesan: `%s` (user)", first.MessageID))
		contains(t, lastText(t, h.DB), fmt.Sprintf("• Run: `%s`", run.ID))
		contains(t, lastText(t, h.DB), fmt.Sprintf("• Sesi: `%s`", *c.SessionID))
	})

	t.Run("finds a quoted reply or command reply through the outbox", func(t *testing.T) {
		h := cmdSetup(t, []models.Step{models.Say("halo juga")}, nil, extra{})
		h.say("halo palui")
		h.say("/status")
		rows := outboxRows(t, h.DB)
		reply, status := rows[0], rows[1]
		_, err := h.DB.Exec("UPDATE outbox SET channel_msg_id = 'out-1' WHERE id = ?", reply.ID)
		testutil.Must(t, err)
		_, err = h.DB.Exec("UPDATE outbox SET channel_msg_id = 'out-2' WHERE id = ?", status.ID)
		testutil.Must(t, err)
		run := allRuns(t, h.DB)[0]
		answer := need(db.ScanMessage(h.DB.QueryRow("SELECT " + db.MessageCols + " FROM messages WHERE role = 'assistant'")))
		h.say("/id", func(m *connectors.InboundMessage) { m.ReplyToChannelMsgID = "out-1" })
		contains(t, lastText(t, h.DB), fmt.Sprintf("• Outbox: `%s` (reply:%s)", reply.ID, run.ID))
		contains(t, lastText(t, h.DB), fmt.Sprintf("• Pesan: `%s` (assistant)", answer.ID))
		h.say("/id", func(m *connectors.InboundMessage) { m.ReplyToChannelMsgID = "out-2" })
		contains(t, lastText(t, h.DB), fmt.Sprintf("• Outbox: `%s` (system)", status.ID))
		h.say("/id", func(m *connectors.InboundMessage) { m.ReplyToChannelMsgID = "nope" })
		contains(t, lastText(t, h.DB), "Gak ketemu di database")
	})
}

func TestUsage(t *testing.T) {
	h := cmdSetup(t, []models.Step{models.Say("halo juga")}, nil, extra{})
	h.say("halo palui")
	h.say("/usage")
	text := lastText(t, h.DB)
	contains(t, text, "Run: 1 (jam ini 1 / 60)")
	if !regexp.MustCompile(`• cliproxy/m: 1 run, input \d+, output \d+`).MatchString(text) || !regexp.MustCompile(`• Palui: 1 run, \d+ / tanpa batas`).MatchString(text) {
		t.Fatal(text)
	}
	contains(t, text, "Chat ini: 1 run")
	contains(t, text, "7 hari terakhir: 1 run")
}

func TestToolsCommand(t *testing.T) {
	alpha := toolsRT("alpha", tools(
		"baca", connectors.PaluiTool{Class: "read", Title: "Baca data", Tool: conntest.FnTool("read it", ret("ok"))},
		"hapus", connectors.PaluiTool{Class: "destructive", Tool: conntest.FnTool("delete it", ret("ok"))},
	))
	h := cmdSetup(t, nil, nil, extra{registry: map[string]*connectors.Runtime{"alpha": alpha}, rows: []chattest.Row{{"alpha", "tools"}}})
	h.say("/tools")
	contains(t, lastText(t, h.DB), "Tool buat Palui di chat ini: 2")
	contains(t, lastText(t, h.DB), "• alpha (2): read 1, destructive 1")
	h.say("/tools alpha")
	eq(t, lastText(t, h.DB), "alpha (2 tool):\n• baca (read): Baca data\n• hapus (destructive)")
	h.say("/tools beta")
	contains(t, lastText(t, h.DB), "Gak ada tool dari `beta`")
}

func TestModelsAndModel(t *testing.T) {
	offers := func() []offer {
		return []offer{{id: "cliproxy", models: []string{"claude-opus-4-7", "claude-sonnet-4-6", "m"}}, {id: "gateway", models: []string{"claude-sonnet-4-6", "gpt-5"}}}
	}
	t.Run("/models lists what each model connector offers, filtered by a word or a connector", func(t *testing.T) {
		h := cmdSetup(t, nil, offers(), extra{})
		h.say("/models")
		contains(t, lastText(t, h.DB), "cliproxy (3 model):\n• claude-opus-4-7\n• claude-sonnet-4-6\n• m (dipakai: default, fast, vision)")
		contains(t, lastText(t, h.DB), "gateway (2 model):")
		h.say("/models gpt")
		eq(t, lastText(t, h.DB), "gateway (1 model):\n• gpt-5\n\nGanti model: /model <nama>.")
		h.say("/models gateway")
		notContains(t, lastText(t, h.DB), "cliproxy (")
		h.say("/models llama")
		eq(t, lastText(t, h.DB), "Gak ada model yang cocok sama `llama`.")
	})

	t.Run("/model shows the chains and puts a model first on every connector that offers it", func(t *testing.T) {
		h := cmdSetup(t, nil, offers(), extra{})
		h.say("/model")
		contains(t, lastText(t, h.DB), "• default: cliproxy/m → gateway/m")
		h.say("/model sonnet")
		roles := need(settings.Get[settings.ModelRoles](h.DB, "modelRoles"))
		jsonEq(t, roles.Default, `[{"connectorId":"cliproxy","model":"claude-sonnet-4-6"},{"connectorId":"gateway","model":"claude-sonnet-4-6"},{"connectorId":"cliproxy","model":"m"},{"connectorId":"gateway","model":"m"}]`)
		contains(t, lastText(t, h.DB), "Oke, model default sekarang cliproxy/claude-sonnet-4-6.")
		contains(t, lastText(t, h.DB), "Pesan bergambar tetap pakai cliproxy/m.")
		hit := false
		for _, e := range listEvents(t, h.DB) {
			if e.Kind == "settings.update" && strings.Contains(e.Message, "default model set to cliproxy/claude-sonnet-4-6") {
				hit = true
			}
		}
		eq(t, hit, true)
		jsonEq(t, roles.Fast, `[{"connectorId":"cliproxy","model":"m"},{"connectorId":"gateway","model":"m"}]`)
		h.say("/model claude-sonnet-4-6")
		eq(t, lastText(t, h.DB), "Model default udah cliproxy/claude-sonnet-4-6.")
	})

	t.Run("/model takes a role and a connector, and refuses a name that is unknown or matches several", func(t *testing.T) {
		h := cmdSetup(t, nil, offers(), extra{})
		h.say("/model fast gateway/gpt-5")
		roles := need(settings.Get[settings.ModelRoles](h.DB, "modelRoles"))
		jsonEq(t, roles.Fast[0], `{"connectorId":"gateway","model":"gpt-5"}`)
		jsonEq(t, roles.Default[0], `{"connectorId":"cliproxy","model":"m"}`)
		h.say("/model fast")
		contains(t, lastText(t, h.DB), "Model sekarang:")
		h.say("/model claude")
		contains(t, lastText(t, h.DB), "Ada 2 model yang cocok sama `claude`")
		h.say("/model llama")
		contains(t, lastText(t, h.DB), "Gak ada model yang cocok sama `llama`.")
		jsonEq(t, need(settings.Get[settings.ModelRoles](h.DB, "modelRoles")).Default[0], `{"connectorId":"cliproxy","model":"m"}`)
	})

	t.Run("says so when a connector's model list can't be fetched", func(t *testing.T) {
		h := cmdSetup(t, nil, []offer{{id: "cliproxy", err: errors.New("503")}}, extra{})
		h.say("/models")
		contains(t, lastText(t, h.DB), "cliproxy: daftar modelnya gagal diambil.")
		h.say("/model opus")
		contains(t, lastText(t, h.DB), "Daftar dari cliproxy gagal diambil.")
	})
}

func TestBtw(t *testing.T) {
	t.Run("answers with the session as context and leaves the session untouched", func(t *testing.T) {
		h := cmdSetup(t, []models.Step{models.Say("halo juga"), models.Say("Makassar, WITA.")}, nil, extra{})
		h.say("halo palui, aku lagi di Banjarmasin")
		before := count(t, h.DB, "messages")
		cmd := h.say("/btw zona waktu sini apa?")
		eq(t, lastText(t, h.DB), "Makassar, WITA.")
		rows := outboxRows(t, h.DB)
		eq(t, rows[len(rows)-1].Payload.Format, "markdown")
		p := prompt(h.m, -1)
		contains(t, p, "Owner: halo palui, aku lagi di Banjarmasin")
		contains(t, p, "Side question: zona waktu sini apa?")
		eq(t, count(t, h.DB, "messages"), before+1)
		row := messageByID(t, h.DB, cmd.MessageID)
		eq(t, row.Status, "handled")
		if row.RunID != nil {
			t.Fatal(row.RunID)
		}
		eq(t, len(allRuns(t, h.DB)), 1)
	})

	t.Run("needs a question and a model", func(t *testing.T) {
		h := cmdSetup(t, nil, []offer{}, extra{})
		h.say("/btw")
		contains(t, lastText(t, h.DB), "Formatnya: /btw <pertanyaan>")
		h.say("/btw jam berapa?")
		eq(t, lastText(t, h.DB), chat.ModelOffText)
	})

	t.Run("tells the owner when the model call fails", func(t *testing.T) {
		h := cmdSetup(t, []models.Step{models.Fail(errors.New("boom")), models.Fail(errors.New("boom"))}, nil, extra{})
		h.say("/btw halo?")
		eq(t, lastText(t, h.DB), "/btw gagal jalan. Coba lagi ya.")
	})
}

func TestCompact(t *testing.T) {
	h := cmdSetup(t, []models.Step{models.Say("satu"), models.Say("dua"), models.Say("tiga"), models.Say("empat"), models.Say("RINGKASAN: empat sapaan")}, nil, extra{})
	for _, x := range []string{"a", "b", "c", "d"} {
		h.say("halo " + x)
	}
	h.say("/compact")
	eq(t, lastText(t, h.DB), "Oke, 2 pesan lama udah aku ringkas. 6 pesan terakhir tetap utuh.")
	ses := need(db.ScanSession(h.DB.QueryRow("SELECT " + db.SessionCols + " FROM sessions")))
	eq(t, *ses.Summary, "RINGKASAN: empat sapaan")
	if ses.SummarizedUntil == nil {
		t.Fatal("no watermark")
	}
	h.say("/compact")
	eq(t, lastText(t, h.DB), "Obrolannya masih pendek, belum ada yang perlu diringkas.")
}

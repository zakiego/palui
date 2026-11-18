package chat_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/customcmd"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/models"
)

func addCommand(t *testing.T, q db.Q, in customcmd.Input) db.CustomCommand {
	t.Helper()
	in.Enabled = true
	in.Reserved = chat.BuiltinCommands
	return need(customcmd.Create(q, in, 1))
}

func TestCustomCommands(t *testing.T) {
	t.Run("a command with a report runs a turn on the expanded prompt and the typed text stays", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("halo juga"), models.Say("thread dimulai"), models.Say("oke")}, extra{})
		addCommand(t, h.DB, customcmd.Input{Name: "report-fix", Description: "report lalu perbaiki", Prompt: "Start a T3 Code thread in palui to fix this. Owner note: {{args}}", AttachReport: true})
		h.sendIdle("halo palui")
		r := h.sendIdle("/report-fix@palui_bot reminder\ngak kekirim")

		saved := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports")))
		eq(t, saved.Note, "reminder\ngak kekirim")
		contains(t, saved.Text, ": halo palui")
		notContains(t, saved.Text, "/report-fix")

		p := prompt(h.m, -1)
		contains(t, p, `Start a T3 Code thread in palui to fix this. Owner note: reminder\ngak kekirim\n\n<debug_report>\nPalui debug report `+saved.ID+".")
		notContains(t, p, "/report-fix")

		row := messageByID(t, h.DB, r.MessageID)
		eq(t, row.Text, "/report-fix@palui_bot reminder\ngak kekirim")
		c := chat.ParseContent(row.Content)
		eq(t, c.Command.Name, "report-fix")
		eq(t, c.Command.ReportID, saved.ID)
		eq(t, lastText(t, h.DB), "thread dimulai")

		// Later turns still see the prompt, and a later report skips the command.
		h.sendIdle("makasih")
		contains(t, prompt(h.m, -1), "Owner note: reminder")
		h.sendIdle("/report")
		latest := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports ORDER BY created_at DESC, rowid DESC LIMIT 1")))
		notContains(t, latest.Text, "/report-fix")
	})

	t.Run("a report command sent as a reply keeps the quoted message in the report", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("thread dimulai")}, extra{})
		addCommand(t, h.DB, customcmd.Input{Name: "report-fix", Prompt: "Fix this. {{args}}", AttachReport: true})
		h.sendIdle("/report-fix", func(m *connectors.InboundMessage) {
			m.Quoted = &connectors.InboundQuote{From: "self", Text: "pencariannya kelamaan"}
		})
		saved := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports")))
		contains(t, saved.Text, "Owner replied to the bot's reply with this report, so that's most likely what went wrong: pencariannya kelamaan")
		contains(t, prompt(h.m, -1), "most likely what went wrong: pencariannya kelamaan")
	})

	t.Run("an agent's own command beats a global one, an off one is a normal message", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("a"), models.Say("b")}, extra{})
		def := firstAgent(t, h.DB)
		addCommand(t, h.DB, customcmd.Input{Name: "cek", Prompt: "global prompt"})
		own := addCommand(t, h.DB, customcmd.Input{AgentID: def.ID, Name: "cek", Prompt: "own prompt"})
		h.sendIdle("/cek")
		p := prompt(h.m, -1)
		contains(t, p, "own prompt")
		notContains(t, p, "global prompt")

		_ = need(customcmd.SetEnabled(h.DB, own.ID, false, 2))
		_ = need(customcmd.SetEnabled(h.DB, need(customcmd.List(h.DB))[0].ID, false, 2))
		h.sendIdle("/cek lagi")
		contains(t, prompt(h.m, -1), "/cek lagi")
	})

	t.Run("/help lists the chat's custom commands", func(t *testing.T) {
		h := basic(t, nil, extra{})
		addCommand(t, h.DB, customcmd.Input{Name: "report-fix", Description: "report lalu perbaiki", Prompt: "x"})
		addCommand(t, h.DB, customcmd.Input{Name: "pagi", Prompt: "x"})
		h.sendIdle("/help")
		help := lastText(t, h.DB)
		contains(t, help, "Perintah buatanmu:\n• /pagi: perintah dari dashboard\n• /report-fix: report lalu perbaiki")
	})
}

func firstAgent(t *testing.T, q db.Q) db.Agent {
	t.Helper()
	return need(agents.GetDefault(q, 1))
}

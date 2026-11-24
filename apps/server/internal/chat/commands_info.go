package chat

// Read-only chat commands (commands-info.ts): /help, /id, /usage and /tools.
// None of them calls a model.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/customcmd"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/tools"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

var helpLines = [][2]string{
	{"/help", "daftar perintah ini"},
	{"/status", "model, token hari ini dan status konektor"},
	{"/usage", "pemakaian token hari ini per model dan agent"},
	{"/new", "simpan sesi ini, mulai sesi baru"},
	{"/compact", "ringkas obrolan lama biar hemat token, sesinya lanjut"},
	{"/stop", "hentikan turn yang lagi jalan"},
	{"/btw <pertanyaan>", "tanya sampingan, gak masuk ke sesi"},
	{"/model [fast|vision] <nama>", "lihat atau ganti model"},
	{"/models [kata]", "daftar model dari tiap konektor model"},
	{"/tools [id]", "tool yang bisa dipakai agent di chat ini"},
	{"/connectors [on|off <id>]", "lihat, nyalakan atau matikan konektor"},
	{"/agent [slug|off]", "lihat atau ganti agent di chat ini"},
	{"/reply [all|mention]", "di grup: jawab semua pesan atau cuma mention"},
	{"/ack [on|off|emoji]", "reaksi pas lagi mikir di chat ini: nyalakan, matikan, atau ganti emoji"},
	{"/id", "id chat, sesi dan pesan. Quote sebuah pesan buat lihat id-nya"},
	{"/report [catatan]", "snapshot debug chat ini buat ditempel ke coding agent"},
}

// HelpText is helpText. The commands of this chat's flows come from the
// Flows boundary (M5); without it there are none.
func HelpText(c *EngineCtx, chat db.Chat) (string, error) {
	lines := []string{"Perintah:"}
	for _, h := range helpLines {
		lines = append(lines, fmt.Sprintf("• %s: %s", h[0], h[1]))
	}
	if c.Deps.Flows != nil {
		own, err := c.Deps.Flows.ChatCommands(c.DB, chat.ID)
		if err != nil {
			return "", err
		}
		if len(own) > 0 {
			lines = append(lines, "", "Dari flow di chat ini:")
			for _, f := range own {
				lines = append(lines, fmt.Sprintf("• %s: flow \"%s\"", f.Command, f.Flow))
			}
		}
	}
	agent, err := agents.ForChat(c.DB, chat.AgentID, c.Clock())
	if err != nil {
		return "", err
	}
	custom, err := customcmd.ForAgent(c.DB, agent.ID)
	if err != nil {
		return "", err
	}
	if len(custom) > 0 {
		lines = append(lines, "", "Perintah buatanmu:")
		for _, cmd := range custom {
			about := cmd.Description
			if about == "" {
				about = "perintah dari dashboard"
			}
			lines = append(lines, fmt.Sprintf("• /%s: %s", cmd.Name, about))
		}
	}
	lines = append(lines, "", "Persetujuan: balas `ya` atau `batal`. Quote pesannya kalau yang nunggu lebih dari satu.")
	return strings.Join(lines, "\n"), nil
}

// quotedLines: the stored row behind a quoted channel message.
func quotedLines(q db.Q, chatID, channelMsgID string) ([]string, error) {
	m, ok, err := db.One(q.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE chat_id = ? AND channel_msg_id = ? ORDER BY created_at DESC LIMIT 1", chatID, channelMsgID), db.ScanMessage)
	if err != nil {
		return nil, err
	}
	if ok {
		lines := []string{fmt.Sprintf("• Pesan: `%s` (%s)", m.ID, m.Role)}
		if m.RunID != nil && *m.RunID != "" {
			lines = append(lines, fmt.Sprintf("• Run: `%s`", *m.RunID))
		}
		if m.SessionID != nil && *m.SessionID != "" {
			lines = append(lines, fmt.Sprintf("• Sesi: `%s`", *m.SessionID))
		}
		return append(lines, fmt.Sprintf("• Id di channel: `%s`", channelMsgID)), nil
	}
	o, ok, err := db.One(q.QueryRow("SELECT "+db.OutboxCols+" FROM outbox WHERE chat_id = ? AND channel_msg_id = ? ORDER BY created_at DESC LIMIT 1", chatID, channelMsgID), db.ScanOutbox)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []string{fmt.Sprintf("• Gak ketemu di database (id di channel: `%s`).", channelMsgID)}, nil
	}
	lines := []string{fmt.Sprintf("• Outbox: `%s` (%s)", o.ID, o.Source)}
	// A reply sent in several parts: only the first part is linked to the message row.
	if strings.HasPrefix(o.Source, "reply:") {
		runID := strings.TrimPrefix(o.Source, "reply:")
		reply, found, err := db.One(q.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE chat_id = ? AND run_id = ? AND role = 'assistant' LIMIT 1", chatID, runID), db.ScanMessage)
		if err != nil {
			return nil, err
		}
		if found {
			lines = append(lines, fmt.Sprintf("• Pesan: `%s` (assistant)", reply.ID))
		}
		lines = append(lines, fmt.Sprintf("• Run: `%s`", runID))
		if found && reply.SessionID != nil && *reply.SessionID != "" {
			lines = append(lines, fmt.Sprintf("• Sesi: `%s`", *reply.SessionID))
		}
	}
	return append(lines, fmt.Sprintf("• Id di channel: `%s`", channelMsgID)), nil
}

// IDText is idText.
func IDText(c *EngineCtx, chat db.Chat, msg *CommandMessage) (string, error) {
	agent, err := agents.ForChat(c.DB, chat.AgentID, c.Clock())
	if err != nil {
		return "", err
	}
	session := "belum ada"
	if chat.SessionID != nil && *chat.SessionID != "" {
		session = fmt.Sprintf("`%s`", *chat.SessionID)
	}
	lines := []string{fmt.Sprintf("Chat: `%s` (%s, %s)", chat.ID, chat.Channel, chat.ExternalID), "Sesi: " + session, fmt.Sprintf("Agent: %s (`%s`)", agent.Slug, agent.ID)}
	if msg != nil {
		lines = append(lines, fmt.Sprintf("Pesan ini: `%s`", msg.ID))
	}
	if msg != nil && msg.ReplyToChannelMsgID != "" {
		quoted, err := quotedLines(c.DB, chat.ID, msg.ReplyToChannelMsgID)
		if err != nil {
			return "", err
		}
		lines = append(append(lines, "", "Yang di-quote:"), quoted...)
	} else {
		lines = append(lines, "", "Quote sebuah pesan lalu kirim /id buat lihat id pesan itu.")
	}
	return strings.Join(lines, "\n"), nil
}

type tally struct {
	runs, tokensIn, tokensOut, cached, cacheWrite int64
}

func (t *tally) add(r db.Run) {
	t.runs++
	t.tokensIn += r.TokensIn
	t.tokensOut += r.TokensOut
	t.cached += r.TokensCached
	t.cacheWrite += r.TokensCacheWrite
}

// UsageText is usageText: today in the settings zone, from the budget
// counters and the `runs` rows.
func UsageText(c *EngineCtx, chat db.Chat) (string, error) {
	q := c.DB
	now := c.Clock()
	all, err := settings.All(q)
	if err != nil {
		return "", err
	}
	tz := all.Timezone
	loc := zone.MustLoad(tz)
	dayStart := zone.StartOfDay(now, loc)
	weekStart := zone.PlusDays(dayStart, loc, -6)
	rows, err := q.Query("SELECT "+db.RunCols+" FROM runs WHERE started_at >= ?", weekStart)
	runs, err := db.All(rows, err, db.ScanRun)
	if err != nil {
		return "", err
	}
	var today, week, here tally
	byModel := map[string]*tally{}
	var modelOrder []string
	byAgent := map[string]*tally{}
	for _, r := range runs {
		week.add(r)
		if r.StartedAt < dayStart {
			continue
		}
		today.add(r)
		if r.ChatID != nil && *r.ChatID == chat.ID {
			here.add(r)
		}
		if r.Model != nil && *r.Model != "" {
			cid := "?"
			if r.ModelConnectorID != nil {
				cid = *r.ModelConnectorID
			}
			key := cid + "/" + *r.Model
			if byModel[key] == nil {
				byModel[key] = &tally{}
				modelOrder = append(modelOrder, key)
			}
			byModel[key].add(r)
		}
		if r.AgentID != nil && *r.AgentID != "" {
			if byAgent[*r.AgentID] == nil {
				byAgent[*r.AgentID] = &tally{}
			}
			byAgent[*r.AgentID].add(r)
		}
	}
	cap := "tanpa batas"
	if all.Budgets.DailyTokenCap > 0 {
		cap = Num(all.Budgets.DailyTokenCap)
	}
	tokens, err := budget.TokensToday(q, now)
	if err != nil {
		return "", err
	}
	runsNow, err := budget.GetUsage(q, budget.RunsKey(now, tz))
	if err != nil {
		return "", err
	}
	lines := []string{
		"Pemakaian hari ini:",
		fmt.Sprintf("Token ke budget: %s / %s", Num(tokens), cap),
		fmt.Sprintf("Run: %s (jam ini %d / %d)", Num(today.runs), runsNow, all.Budgets.MaxRunsPerHour),
		fmt.Sprintf("Input %s (cache read %s, cache write %s), output %s", Num(today.tokensIn), Num(today.cached), Num(today.cacheWrite), Num(today.tokensOut)),
	}
	if len(modelOrder) > 0 {
		sort.SliceStable(modelOrder, func(i, j int) bool {
			a, b := byModel[modelOrder[i]], byModel[modelOrder[j]]
			return b.tokensIn+b.tokensOut < a.tokensIn+a.tokensOut
		})
		lines = append(lines, "", "Per model:")
		for _, key := range modelOrder {
			t := byModel[key]
			lines = append(lines, fmt.Sprintf("• %s: %s run, input %s, output %s", key, Num(t.runs), Num(t.tokensIn), Num(t.tokensOut)))
		}
	}
	list, err := agents.List(q, now)
	if err != nil {
		return "", err
	}
	// A shared chat never learns the owner's other agents (PLAN 19.4).
	shared, err := agents.IsSharedChat(q, chat)
	if err != nil {
		return "", err
	}
	chatAgent, err := agents.ForChat(q, chat.AgentID, now)
	if err != nil {
		return "", err
	}
	var shown []db.Agent
	for _, a := range list {
		if shared && a.ID != chatAgent.ID {
			continue
		}
		if byAgent[a.ID] != nil {
			shown = append(shown, a)
		}
	}
	if len(shown) > 0 {
		lines = append(lines, "", "Per agent (token ke budget):")
		for _, a := range shown {
			used, err := budget.AgentTokensToday(q, a.ID, now)
			if err != nil {
				return "", err
			}
			acap := "tanpa batas"
			if a.DailyTokenCap > 0 {
				acap = Num(a.DailyTokenCap)
			}
			lines = append(lines, fmt.Sprintf("• %s: %s run, %s / %s", a.Name, Num(byAgent[a.ID].runs), Num(used), acap))
		}
	}
	lines = append(lines, "", fmt.Sprintf("Chat ini: %s run, input %s, output %s", Num(here.runs), Num(here.tokensIn), Num(here.tokensOut)))
	lines = append(lines, fmt.Sprintf("7 hari terakhir: %s run, input %s, output %s", Num(week.runs), Num(week.tokensIn), Num(week.tokensOut)))
	lines = append(lines, "", "Cache read dihitung 10% ke budget. Ringkasan sesi dan /btw masuk budget, tapi bukan run.")
	return strings.Join(lines, "\n"), nil
}

var toolClasses = []string{"read", "write", "destructive"}

// ToolsText is toolsText: what a turn in this chat would be offered right now.
func ToolsText(c *EngineCtx, chat db.Chat, args []string) (string, error) {
	q := c.DB
	now := c.Clock()
	agent, err := agents.ForChat(q, chat.AgentID, now)
	if err != nil {
		return "", err
	}
	all, err := settings.All(q)
	if err != nil {
		return "", err
	}
	var sticky []string
	var session *db.Session
	if chat.SessionID != nil {
		if s, ok, err := chats.GetSession(q, *chat.SessionID); err != nil {
			return "", err
		} else if ok {
			sticky = s.LoadedTools.V
			session = &s
		}
	}
	// A shared chat gets what a fresh turn there gets (same rules as ExecuteTurn).
	sessionID := ""
	var history []db.Message
	if session != nil {
		sessionID = session.ID
		if history, err = LoadHistory(q, session.ID, session.SummarizedUntil); err != nil {
			return "", err
		}
	}
	audience, err := TurnAudience(c, chat, agent, sessionID, history)
	if err != nil {
		return "", err
	}
	if audience == tools.AudienceOutside && agent.GuestTools == "none" {
		return "Gak ada tool buat " + agent.Name + " di chat ini: tool buat orang lain dimatiin, dan di sini ada yang bukan owner atau member " + agent.Name + ". Nyalain di dashboard: agent " + agent.Name + " > Settings > \"Tools for other people\", atau jadiin semua orang di sini member.", nil
	}
	perms := agent.ToolPermissions.V
	turn := connectors.TurnInfo{ChatID: chat.ID, Channel: chat.Channel, Trigger: "message", TZ: all.Timezone, Now: now, AgentID: agent.ID}
	open := tools.NoApprovals(audience)
	if open {
		turn.Mode = audience
		audience = tools.ToolAudience(audience, agent)
	} else {
		audience = ""
	}
	collected := tools.Collect(context.Background(), c.Host.Running(), tools.CollectOptions{
		Turn:              turn,
		MaxTools:          int(all.Budgets.MaxTools),
		Sticky:            sticky,
		Permissions:       &perms,
		HideAsk:           open,
		Audience:          audience,
		Log:               c.Log,
		OnUntrustedResult: func(*tools.ToolMeta) {},
	})
	var metas []*tools.ToolMeta
	for _, n := range collected.Names() {
		if m := collected.Meta[n]; m != nil && m.Name != tools.SearchTool {
			metas = append(metas, m)
		}
	}
	if len(metas) == 0 {
		return "Gak ada tool yang nyala sekarang. Cek /connectors.", nil
	}
	byConnector := map[string][]*tools.ToolMeta{}
	var order []string
	for _, m := range metas {
		if _, ok := byConnector[m.ConnectorID]; !ok {
			order = append(order, m.ConnectorID)
		}
		byConnector[m.ConnectorID] = append(byConnector[m.ConnectorID], m)
	}
	if len(args) > 0 && args[0] != "" {
		id := strings.ToLower(args[0])
		own, ok := byConnector[id]
		if !ok {
			return fmt.Sprintf("Gak ada tool dari `%s`. Yang ada: %s.", id, strings.Join(order, ", ")), nil
		}
		lines := []string{fmt.Sprintf("%s (%d tool):", id, len(own))}
		for _, m := range own {
			title := ""
			if m.Title != "" {
				title = ": " + m.Title
			}
			lines = append(lines, fmt.Sprintf("• %s (%s)%s", m.Tool, m.Class, title))
		}
		return strings.Join(lines, "\n"), nil
	}
	deferred := len(collected.Deferred)
	head := fmt.Sprintf("Tool buat %s di chat ini: %d", agent.Name, len(metas))
	if deferred > 0 {
		head += fmt.Sprintf(" (%d langsung, %d lewat pencarian tool)", len(metas)-deferred, deferred)
	}
	lines := []string{head}
	for _, id := range order {
		own := byConnector[id]
		var counts []string
		for _, cls := range toolClasses {
			n := 0
			for _, m := range own {
				if m.Class == cls {
					n++
				}
			}
			if n > 0 {
				counts = append(counts, fmt.Sprintf("%s %d", cls, n))
			}
		}
		lines = append(lines, fmt.Sprintf("• %s (%d): %s", id, len(own), strings.Join(counts, ", ")))
	}
	if audience != "" {
		lines = append(lines, "", "Detail: /tools <id>. Di chat ini tool jalan tanpa minta izin, jadi yang perlu izin gak ditawarin.")
	} else if perms.AllowAll {
		lines = append(lines, "", "Detail: /tools <id>. Semua tool jalan tanpa minta izin di chat kamu, kecuali yang diatur minta izin.")
	} else {
		lines = append(lines, "", "Detail: /tools <id>. Destructive selalu minta izin, write minta izin kalau turn-nya kena data dari luar.")
	}
	return strings.Join(lines, "\n"), nil
}

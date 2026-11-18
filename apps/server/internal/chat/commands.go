package chat

// Chat commands (commands.ts, PLAN 8.2, 4.6). They never reach the model and
// keep working when every model connector is off.

import (
	"context"
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/ack"
	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/memory"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Later is CommandReply.later: slow work whose text is sent when it's done.
// Queued runs it in the chat's order.
type Later struct {
	Run    func() (string, error)
	Queued bool
	Format string
}

// CommandReply is CommandReply.
type CommandReply struct {
	// Empty when the whole answer comes from Later.
	Text    string
	Actions []db.OutboxAction
	// "plain" skips the channel's markdown conversion.
	Format string
	// Runs after the reply was sent (turning off the channel it goes through).
	After func() error
	Later *Later
}

// CommandMessage is the message that carried the command.
type CommandMessage struct {
	ID                  string
	Text                string
	ReplyToChannelMsgID string
	// The message it replied to, if any.
	Quoted *StoredQuote
}

var statusLabels = map[string]string{"on": "nyala", "paused": "dijeda", "error": "error", "connecting": "nyambung...", "not_set_up": "belum disetup"}

func statusLabel(s string) string {
	if l, ok := statusLabels[s]; ok {
		return l
	}
	return s
}

// RunCommand is runCommand.
func RunCommand(c *EngineCtx, chat db.Chat, name string, args []string, msg *CommandMessage) (CommandReply, error) {
	switch name {
	case "help":
		t, err := HelpText(c, chat)
		return CommandReply{Text: t}, err
	case "id":
		t, err := IDText(c, chat, msg)
		return CommandReply{Text: t}, err
	case "usage":
		t, err := UsageText(c, chat)
		return CommandReply{Text: t}, err
	case "tools":
		return CommandReply{Later: &Later{Run: func() (string, error) { return ToolsText(c, chat, args) }}}, nil
	case "models":
		return CommandReply{Later: &Later{Run: func() (string, error) { return ModelsText(c, args) }}}, nil
	case "model":
		return CmdModel(c, chat, args)
	case "btw":
		return CmdBtw(c, chat, msg)
	case "compact":
		return CmdCompact(c, chat), nil
	case "new":
		return cmdNew(c, chat)
	case "stop":
		return cmdStop(c, chat), nil
	case "status":
		t, err := StatusText(c, chat)
		return CommandReply{Text: t}, err
	case "connectors":
		return cmdConnectors(c, chat, args)
	case "agent":
		return cmdAgent(c, chat, args)
	case "reply":
		return cmdReply(c, chat, args)
	case "ack":
		return cmdAck(c, chat, args)
	case "report":
		return cmdReport(c, chat, args, msg)
	}
	return CommandReply{}, fmt.Errorf("unknown command %s", name)
}

// SwitchResult is switchAgent's result.
type SwitchResult struct {
	Chat    db.Chat
	Agent   db.Agent
	Changed bool
}

// SwitchAgent is switchAgent (PLAN 19.2): save the session to the old agent's
// memory, assign, then start a fresh session. agentID "" is the default agent.
func SwitchAgent(c *EngineCtx, chatID, agentID string) (SwitchResult, error) {
	q := c.DB
	before, ok, err := chats.Get(q, chatID)
	if err != nil {
		return SwitchResult{}, err
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("No chat %s", chatID)
	}
	now := c.Clock()
	var target db.Agent
	if agentID != "" {
		var found bool
		if target, found, err = agents.Get(q, agentID); err != nil {
			return SwitchResult{}, err
		}
		if !found {
			return SwitchResult{}, fmt.Errorf("No agent %s", agentID)
		}
	} else if target, err = agents.GetDefault(q, now); err != nil {
		return SwitchResult{}, err
	}
	current, err := agents.ForChat(q, before.AgentID, now)
	if err != nil {
		return SwitchResult{}, err
	}
	same := current.ID == target.ID
	if !same && before.SessionID != nil {
		if _, err := memory.AutoSave(context.Background(), q, memory.Find(c.Host.Running()), *before.SessionID, c.Log, nil, now); err != nil {
			c.Log.Warn("auto-save before an agent switch failed", logx.Fields{"chat_id": chatID, "error": err})
		}
	}
	var assign *string
	if !target.IsDefault {
		assign = &target.ID
	}
	chat, agent, err := agents.Assign(q, chatID, assign, now)
	if err != nil {
		return SwitchResult{}, err
	}
	if !same {
		if _, err := chats.RotateSession(q, chatID, c.Clock()); err != nil {
			return SwitchResult{}, err
		}
	}
	return SwitchResult{Chat: chat, Agent: agent, Changed: !same}, nil
}

var replyModeText = map[string]string{"mention": "kalau di-mention atau di-reply", "all": "semua pesan"}

func cmdAgent(c *EngineCtx, chat db.Chat, args []string) (CommandReply, error) {
	q := c.DB
	now := c.Clock()
	current, err := agents.ForChat(q, chat.AgentID, now)
	if err != nil {
		return CommandReply{}, err
	}
	arg := ""
	if len(args) > 0 {
		arg = strings.ToLower(args[0])
	}
	where := "chat ini"
	if chat.Kind == "group" {
		where = "grup ini"
	}
	// A chat on an agent's own bot keeps that agent.
	pickable := []db.Agent{current}
	if chat.ConnectorID == "" {
		if pickable, err = agents.List(q, now); err != nil {
			return CommandReply{}, err
		}
	}
	shared, err := agents.IsSharedChat(q, chat)
	if err != nil {
		return CommandReply{}, err
	}
	choices := func() []string {
		out := make([]string, len(pickable))
		for i, a := range pickable {
			extra := ""
			if a.IsDefault && shared {
				extra = ", cuma jawab kamu"
			}
			out[i] = fmt.Sprintf("• /agent %s: %s%s", a.Slug, a.Name, extra)
		}
		return out
	}
	// Everyone in a shared chat reads the reply, so the owner's agents never
	// show up there: the list goes to the owner's DM (PLAN 19.4). A chat on an
	// agent's own bot only ever offers that agent, which the chat already knows.
	private := shared && chat.ConnectorID == ""
	toOwner := func(lines []string) (string, bool) {
		dm, ok := ownerChatFor(c, chat.Channel)
		if !ok {
			return "", false
		}
		name := chat.Title
		if name == "" {
			name = chat.ExternalID
		}
		label := "Chat"
		if chat.Kind == "group" {
			label = "Grup"
		}
		c.send(dm, strings.Join(append([]string{fmt.Sprintf("%s \"%s\" (%s):", label, name, chat.Channel)}, lines...), "\n"), "system", SendExtra{})
		return "Pilihan agent-nya udah aku kirim ke chat pribadi kamu.", true
	}
	if arg == "" && !chat.Allowed {
		// The setup prompt: the first thing the owner sees in a new group.
		head := fmt.Sprintf("Orang lain di %s belum dijawab.", where)
		if chat.Channel == "whatsapp" {
			head = fmt.Sprintf("Aku belum aktif di %s.", where)
		}
		if private {
			head = fmt.Sprintf("Aku belum di-setup di %s, belum ada agent yang dipilih.", where)
			pick := append([]string{"Belum ada agent. Ketik salah satu di sana:"}, choices()...)
			pick = append(pick, "", "Agent baru bisa dibikin di dashboard, menu Agents.")
			if sent, ok := toOwner(pick); ok {
				return CommandReply{Text: head + "\n" + sent}, nil
			}
			return CommandReply{Text: head + "\nPilih pakai /agent <nama>."}, nil
		}
		lines := append([]string{head + " Pilih siapa yang jawab:"}, choices()...)
		if chat.ConnectorID == "" {
			lines = append(lines, "", "Agent baru bisa dibikin di dashboard, menu Agents.")
		}
		return CommandReply{Text: strings.Join(lines, "\n")}, nil
	}
	if arg == "" {
		var mode []string
		if chat.Kind == "group" {
			mode = []string{fmt.Sprintf("Jawab %s. Ganti pakai /reply.", replyModeText[chat.ReplyMode])}
		}
		if chat.ConnectorID != "" {
			return CommandReply{Text: strings.Join(append([]string{fmt.Sprintf("Agent di %s: %s (lewat bot-nya sendiri)", where, current.Name)}, mode...), "\n")}, nil
		}
		lines := append([]string{fmt.Sprintf("Agent di %s: %s", where, current.Name)}, mode...)
		if private {
			pick := append([]string{fmt.Sprintf("Agent-nya sekarang %s. Buat ganti, ketik salah satu di sana:", current.Name)}, choices()...)
			pick = append(pick, "", "Balik ke Palui: /agent off.")
			if sent, ok := toOwner(pick); ok {
				lines = append(lines, sent)
			} else {
				lines = append(lines, "Ganti pakai /agent <nama>.")
			}
			return CommandReply{Text: strings.Join(lines, "\n")}, nil
		}
		lines = append(lines, "", "Agent yang ada:")
		lines = append(lines, choices()...)
		lines = append(lines, "", "Balik ke Palui: /agent off.")
		return CommandReply{Text: strings.Join(lines, "\n")}, nil
	}
	var target *db.Agent
	if arg == "off" {
		d, err := agents.GetDefault(q, now)
		if err != nil {
			return CommandReply{}, err
		}
		target = &d
	} else {
		a, ok, err := agents.GetBySlug(q, arg)
		if err != nil {
			return CommandReply{}, err
		}
		if ok {
			target = &a
		}
	}
	if chat.ConnectorID != "" && (target == nil || target.ID != current.ID) {
		return CommandReply{Text: fmt.Sprintf("Chat ini masuk lewat bot milik %s, jadi agent-nya tetap.", current.Name)}, nil
	}
	if target == nil {
		return CommandReply{Text: fmt.Sprintf("Gak ada agent `%s`. Cek daftarnya pakai /agent.", arg)}, nil
	}
	if target.ID == current.ID && chat.Allowed {
		return CommandReply{Text: fmt.Sprintf("Udah pakai %s.", target.Name)}, nil
	}
	id := ""
	if !target.IsDefault {
		id = target.ID
	}
	r, err := SwitchAgent(c, chat.ID, id)
	if err != nil {
		return CommandReply{}, err
	}
	fresh := ""
	if r.Changed {
		fresh = " Mulai sesi baru."
	}
	if shared, err = agents.IsSharedChat(q, chat); err != nil {
		return CommandReply{}, err
	}
	if target.IsDefault {
		if shared {
			return CommandReply{Text: fmt.Sprintf("Oke, %s yang jawab di sini, cuma buat kamu.%s", target.Name, fresh)}, nil
		}
		return CommandReply{Text: fmt.Sprintf("Oke, %s yang jawab di sini.%s", target.Name, fresh)}, nil
	}
	if chat.Kind == "group" {
		mode := "Dia jawab kalau di-mention atau di-reply. Biar jawab semua pesan: /reply all"
		if chat.ReplyMode == "all" {
			mode = "Dia jawab semua pesan di grup ini."
		}
		return CommandReply{Text: fmt.Sprintf("Oke, sekarang %s yang jawab di sini.%s\n%s", target.Name, fresh, mode)}, nil
	}
	who := ""
	if shared {
		who = " Orang di chat ini sekarang dijawab sama dia."
	}
	return CommandReply{Text: fmt.Sprintf("Oke, sekarang %s yang jawab di sini.%s%s", target.Name, fresh, who)}, nil
}

// cmdReply: groups only (PLAN 19.2): answer @mentions and replies, or every message.
func cmdReply(c *EngineCtx, chat db.Chat, args []string) (CommandReply, error) {
	if chat.Kind != "group" {
		return CommandReply{Text: "Di chat pribadi semua pesan selalu dijawab. /reply cuma buat grup."}, nil
	}
	agent, err := agents.ForChat(c.DB, chat.AgentID, c.Clock())
	if err != nil {
		return CommandReply{}, err
	}
	arg := ""
	if len(args) > 0 {
		arg = strings.ToLower(args[0])
	}
	if arg == "" {
		other := "all"
		if chat.ReplyMode == "all" {
			other = "mention"
		}
		return CommandReply{Text: fmt.Sprintf("%s jawab %s. Ganti: /reply %s", agent.Name, replyModeText[chat.ReplyMode], other)}, nil
	}
	if arg != "all" && arg != "mention" {
		return CommandReply{Text: "Pilih /reply all atau /reply mention."}, nil
	}
	if _, _, err := chats.SetReplyMode(c.DB, chat.ID, arg); err != nil {
		return CommandReply{}, err
	}
	off := ""
	if !chat.Allowed {
		off = "\nGrup ini belum nyala. Pilih agent dulu: /agent"
	}
	if arg == "mention" {
		return CommandReply{Text: fmt.Sprintf("Oke, %s sekarang cuma jawab %s.%s", agent.Name, replyModeText["mention"], off)}, nil
	}
	// A Telegram bot with privacy mode on never sees messages that don't mention it.
	tg := ""
	if chat.Channel == "telegram" {
		tg = "\nDi Telegram, matiin dulu privacy mode bot-nya di @BotFather, lalu keluarin dan masukin lagi bot-nya ke grup."
	}
	return CommandReply{Text: fmt.Sprintf("Oke, %s sekarang jawab semua pesan di grup ini.%s%s", agent.Name, off, tg)}, nil
}

// cmdAck: the reaction while a reply is being written (PLAN 5).
func cmdAck(c *EngineCtx, chat db.Chat, args []string) (CommandReply, error) {
	raw := strx.Trim(strings.Join(args, " "))
	arg := strings.ToLower(raw)
	only := "\nReaksi cuma ada di WhatsApp, di sini yang muncul cuma typing."
	if chat.Channel == "whatsapp" {
		only = ""
	}
	if arg == "" {
		state := fmt.Sprintf("mati (emoji-nya %s)", chat.AckEmoji)
		toggle := "on"
		if chat.Ack {
			state = "nyala pakai " + chat.AckEmoji
			toggle = "off"
		}
		return CommandReply{Text: fmt.Sprintf("Reaksi pas mikir di chat ini %s. Ganti: /ack %s, atau /ack <emoji> buat ganti emoji.%s", state, toggle, only)}, nil
	}
	if arg == "on" || arg == "off" {
		on := arg == "on"
		if _, _, err := chats.SetAck(c.DB, chat.ID, chats.AckPatch{On: &on}); err != nil {
			return CommandReply{}, err
		}
		if on {
			return CommandReply{Text: fmt.Sprintf("Oke, reaksi %s nyala lagi di chat ini.%s", chat.AckEmoji, only)}, nil
		}
		return CommandReply{Text: "Oke, gak pakai reaksi lagi di chat ini. Typing tetap muncul." + only}, nil
	}
	emoji, ok := ack.ParseEmoji(raw)
	if !ok {
		return CommandReply{Text: "Pilih /ack on, /ack off, atau satu emoji, misal /ack 👀"}, nil
	}
	on := true
	if _, _, err := chats.SetAck(c.DB, chat.ID, chats.AckPatch{On: &on, Emoji: &emoji}); err != nil {
		return CommandReply{}, err
	}
	return CommandReply{Text: fmt.Sprintf("Oke, reaksinya sekarang %s.%s", emoji, only)}, nil
}

// ReportNoteTip is REPORT_NOTE_TIP.
const ReportNoteTip = "Lain kali tulis juga apa yang salah, biar lebih gampang dilacak: /report <apa yang salah>"

// cmdReport: plain, so the channel sends exactly what `reports` holds. From a
// shared chat the snapshot goes to the owner's DM (PLAN 19.4).
func cmdReport(c *EngineCtx, chat db.Chat, args []string, msg *CommandMessage) (CommandReply, error) {
	note := strx.Trim(strings.Join(args, " "))
	var quote *StoredQuote
	if msg != nil {
		quote = msg.Quoted
	}
	report, err := createReport(c, chat, note, quote, "", c.Clock())
	if err != nil {
		return CommandReply{}, err
	}
	tip := ""
	if note == "" {
		tip = "\n\n" + ReportNoteTip
	}
	shared, err := agents.IsSharedChat(c.DB, chat)
	if err != nil {
		return CommandReply{}, err
	}
	if !shared {
		r := CommandReply{Text: report.Text, Format: "plain"}
		if note == "" {
			r.Later = &Later{Run: func() (string, error) { return ReportNoteTip, nil }}
		}
		return r, nil
	}
	dm, ok := ownerChatFor(c, chat.Channel)
	if !ok {
		return CommandReply{Text: fmt.Sprintf("Report %s udah aku simpan.%s", report.ID, tip)}, nil
	}
	c.send(dm, report.Text, "system", SendExtra{Format: "plain"})
	return CommandReply{Text: fmt.Sprintf("Report %s udah aku kirim ke chat pribadi kamu.%s", report.ID, tip)}, nil
}

func cmdNew(c *EngineCtx, chat db.Chat) (CommandReply, error) {
	if chat.SessionID != nil {
		if _, err := memory.AutoSave(context.Background(), c.DB, memory.Find(c.Host.Running()), *chat.SessionID, c.Log, nil, c.Clock()); err != nil {
			c.Log.Warn("auto-save on /new failed", logx.Fields{"chat_id": chat.ID, "error": err})
		}
	}
	if _, err := chats.RotateSession(c.DB, chat.ID, c.Clock()); err != nil {
		return CommandReply{}, err
	}
	return CommandReply{Text: "Oke, mulai sesi baru. Obrolan sebelumnya udah aku simpan."}, nil
}

func cmdStop(c *EngineCtx, chat db.Chat) CommandReply {
	a := c.Active(chat.ID)
	if a == nil {
		return CommandReply{Text: "Gak ada yang lagi jalan."}
	}
	a.Stop("stopped by the owner")
	return CommandReply{Text: "Oke, aku stop."}
}

// StatusText is statusText.
func StatusText(c *EngineCtx, chat db.Chat) (string, error) {
	q := c.DB
	now := c.Clock()
	all, err := settings.All(q)
	if err != nil {
		return "", err
	}
	// The chain this chat's agent runs, its own or the global one.
	agent, err := agents.ForChat(q, chat.AgentID, now)
	if err != nil {
		return "", err
	}
	tz := all.Timezone
	running := map[string]bool{}
	for _, e := range c.Host.Running() {
		running[e.ID] = true
	}
	var links []string
	for _, l := range models.EffectiveChain(all.ModelRoles, agent.ModelRoles.V, "default") {
		off := ""
		if !running[l.ConnectorID] {
			off = " (off)"
		}
		links = append(links, fmt.Sprintf("%s/%s%s", l.ConnectorID, l.Model, off))
	}
	chain := strings.Join(links, " → ")
	if chain == "" {
		chain = "belum diatur"
	}
	today, err := budget.TokensToday(q, now)
	if err != nil {
		return "", err
	}
	cap := "tanpa batas"
	if all.Budgets.DailyTokenCap > 0 {
		cap = Num(all.Budgets.DailyTokenCap)
	}
	runsNow, err := budget.GetUsage(q, budget.RunsKey(now, tz))
	if err != nil {
		return "", err
	}
	lines := []string{
		"Model: " + chain,
		fmt.Sprintf("Token hari ini: %s / %s", Num(today), cap),
		fmt.Sprintf("Run jam ini: %d / %d", runsNow, all.Budgets.MaxRunsPerHour),
	}
	paused, err := budget.ActivePause(q, now)
	if err != nil {
		return "", err
	}
	if paused != nil {
		lines = append(lines, fmt.Sprintf("Turn dijeda sampai %s: %s", budget.FormatClock(paused.Until, tz), paused.Reason))
	}
	lines = append(lines, "", "Konektor:")
	for _, s := range c.Host.Status() {
		detail := ""
		if s.Detail != nil && *s.Detail != "" {
			detail = fmt.Sprintf(" (%s)", *s.Detail)
		}
		lines = append(lines, fmt.Sprintf("• %s: %s%s", s.ID, statusLabel(s.Status), detail))
	}
	return strings.Join(lines, "\n"), nil
}

func cmdConnectors(c *EngineCtx, chat db.Chat, args []string) (CommandReply, error) {
	q := c.DB
	var verb, id string
	if len(args) > 0 {
		verb = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		id = strings.ToLower(args[1])
	}
	if verb == "" {
		rows, err := connectors.List(q)
		if err != nil {
			return CommandReply{}, err
		}
		lines := []string{"Konektor:"}
		var actions []db.OutboxAction
		for _, r := range rows {
			if r.Status == "not_set_up" {
				continue
			}
			lines = append(lines, fmt.Sprintf("• %s: %s", r.ID, statusLabel(r.Status)))
			if r.ID == "web" {
				continue
			}
			if r.Enabled {
				actions = append(actions, db.OutboxAction{ID: "connector:off:" + r.ID, Label: "Matikan " + r.Label})
			} else {
				actions = append(actions, db.OutboxAction{ID: "connector:on:" + r.ID, Label: "Nyalakan " + r.Label})
			}
		}
		lines = append(lines, "", "Pakai /connectors on <id> atau /connectors off <id>.")
		reply := CommandReply{Text: strings.Join(lines, "\n")}
		if chat.Channel == "telegram" {
			if actions == nil {
				actions = []db.OutboxAction{}
			}
			reply.Actions = actions
		}
		return reply, nil
	}
	if (verb != "on" && verb != "off") || id == "" {
		return CommandReply{Text: "Formatnya: /connectors, /connectors on <id>, atau /connectors off <id>."}, nil
	}
	row, ok, err := connectors.Get(q, id)
	if err != nil {
		return CommandReply{}, err
	}
	if !ok {
		return CommandReply{Text: fmt.Sprintf("Gak ada konektor `%s`. Cek daftarnya pakai /connectors.", id)}, nil
	}
	if row.Status == "not_set_up" {
		return CommandReply{Text: fmt.Sprintf("%s belum disetup. Sambungkan dulu dari dashboard.", row.Label)}, nil
	}
	by := fmt.Sprintf("chat (%s)", chat.Channel)
	if verb == "on" {
		if row.Enabled {
			return CommandReply{Text: fmt.Sprintf("%s udah nyala.", row.Label)}, nil
		}
		if err := connectors.Resume(q, id, by, c.Clock()); err != nil {
			return CommandReply{}, err
		}
		c.Host.Reconcile()
		return CommandReply{Text: fmt.Sprintf("%s dinyalakan.", row.Label)}, nil
	}
	if id == "web" {
		return CommandReply{Text: "Web chat selalu nyala, gak bisa dimatikan."}, nil
	}
	if !row.Enabled {
		return CommandReply{Text: fmt.Sprintf("%s udah mati.", row.Label)}, nil
	}
	apply := func() error {
		if err := connectors.Pause(q, id, by, c.Clock()); err != nil {
			return err
		}
		c.Host.Reconcile()
		return nil
	}
	if row.Kind == "channel" && (row.Type == chat.Channel || id == chat.Channel) {
		var entries []connectors.ChannelEntry
		for _, e := range c.Host.Running() {
			var ch connectors.ChannelHandle
			if e.Handle != nil {
				ch = e.Handle.Channel
			}
			entries = append(entries, connectors.ChannelEntry{ID: e.ID, Kind: e.Row.Kind, Channel: ch})
		}
		way, ok, err := connectors.SelfChannelOffUndo(q, chat.Channel, entries, id)
		if err != nil {
			return CommandReply{}, err
		}
		if !ok {
			return CommandReply{Text: fmt.Sprintf("Gak bisa matiin %s dari sini: gak ada channel lain atau dashboard buat nyalain lagi.", connectors.ChannelName(chat.Channel))}, nil
		}
		// The reply goes out through this channel, so pause only after it's sent.
		return CommandReply{Text: fmt.Sprintf("%s dimatikan. Buat nyalain lagi: %s.", row.Label, way), After: apply}, nil
	}
	if err := apply(); err != nil {
		return CommandReply{}, err
	}
	return CommandReply{Text: fmt.Sprintf("%s dimatikan. Buat nyalain lagi: kirim `/connectors on %s`.", row.Label, id)}, nil
}

// Num is toLocaleString("id-ID") for an integer: dots between thousands.
func Num(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(ch)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

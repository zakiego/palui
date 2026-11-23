package fixtures

import (
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/db"
)

// huge.ts: the realistic upper bound for list length. A year of daily use
// leaves about this much behind: a thousand chats and reminders, thousands of
// runs and events. None of the lists paginate, so this is a render and query
// cost test as much as a visual one.

var (
	hugeFirst = []string{"Aleksandra", "Budi", "Christopher", "Dewi", "Eka", "Fitri", "Gusti", "Hendra", "Indah", "Joko", "Kartika", "Lukman", "Maria", "Nur", "Oktaviani", "Putu", "Rahmat", "Siti", "Taufik", "Wulan"}
	hugeLast  = []string{"Wiśniewska-Kowalczyk", "Santoso", "Montgomery", "Lestari", "Pratama", "Handayani", "Ngurah", "Gunawan", "Permatasari", "Widodo", "Sari", "Hakim", "Fernández", "Hidayah", "Putri", "Wijaya", "Hidayat", "Aminah", "Rahman", "Ningsih"}
	hugeTasks = []string{"Follow up deal", "Telepon pak RT soal iuran keamanan", "Bayar listrik", "Kirim laporan mingguan ke tim Northwind", "Standup", "Cek pipeline", "Perpanjang domain", "Transfer arisan"}
)

// Huge holds the huge fixture's sizes (HUGE in huge.ts).
var Huge = struct {
	Flows, FlowRuns, FlowChats, Chats, Transcript, Upcoming, Recurring, Done, Jobs, Runs, Events, MCP, Hooks, Passkeys int64
}{Flows: 120, FlowRuns: 1000, FlowChats: 2, Chats: 1284, Transcript: 2000, Upcoming: 1284, Recurring: 300, Done: 1284, Jobs: 120, Runs: 5000, Events: 12_000, MCP: 50, Hooks: 40, Passkeys: 25}

func at[T any](list []T, i int64) T { return list[i%int64(len(list))] }

func huge(k *Kit) {
	now := k.Now
	name := func(i int64) string {
		return at(hugeFirst, i) + " " + hugeLast[(i/int64(len(hugeFirst)))%int64(len(hugeLast))]
	}

	for _, t := range []string{"telegram", "whatsapp", "web", "cliproxy", "recall", "reminders", "jobs", "webfetch", "hooks"} {
		k.Connector(t, t, ConnectorOpts{Status: "on"})
	}
	k.Connector("gateway-litellm", "gateway", ConnectorOpts{Status: "on", Config: obj("baseUrl", "https://llm.example.com")})
	// Every MCP server goes down together when the network does.
	for i := int64(0); i < Huge.MCP; i++ {
		status := "on"
		if i%4 == 0 {
			status = "error"
		} else if i%7 == 0 {
			status = "paused"
		}
		var detail *string
		if i%4 == 0 {
			detail = ptr("ECONNRESET")
		}
		trust := "own"
		if i%3 != 0 {
			trust = "external"
		}
		k.Connector(fmt.Sprintf("mcp-server-%02d", i), "mcp", ConnectorOpts{Label: ptr(fmt.Sprintf("%s MCP %d", at(hugeLast, i), i+1)), Status: status, Detail: detail, Trust: &trust, Config: obj("url", fmt.Sprintf("https://mcp-%d.example.com/mcp", i))})
	}

	web := k.WebChat()
	for i := int64(0); i < Huge.Transcript; i++ {
		if i%2 != 0 {
			k.Message(web, "assistant", fmt.Sprintf("Siap, sudah dicatat (%d).", i), now-(Huge.Transcript-i)*MIN)
		} else {
			k.Message(web, "user", fmt.Sprintf("%s #%d", at(hugeTasks, i), 1000+i), now-(Huge.Transcript-i)*MIN)
		}
	}

	var chatIDs []string
	for i := int64(0); i < Huge.Chats; i++ {
		channel := "whatsapp"
		external := fmt.Sprintf("62812%d@s.whatsapp.net", 10_000_000+i)
		if i%2 != 0 {
			channel = "telegram"
			external = fmt.Sprint(1_000_000_000 + i)
		}
		kind := "dm"
		if i%9 == 0 {
			kind = "group"
		}
		c := k.Chat(channel, external, name(i), ChatOpts{Kind: kind})
		chatIDs = append(chatIDs, c.ID)
		role := "assistant"
		if i%3 != 0 {
			role = "user"
		}
		k.Message(c, role, fmt.Sprintf("%s, %s", at(hugeTasks, i), name(i+7)), now-i*37*MIN)
	}
	chatAt := func(i int64) string { return at(chatIDs, i) }

	var jobIDs []string
	for i := int64(0); i < Huge.Jobs; i++ {
		kind := "job"
		if i%10 == 0 {
			kind = "briefing"
		} else if i%10 == 1 {
			kind = "heartbeat"
		}
		lastStatus := "ok"
		if i%8 == 0 {
			lastStatus = "error"
		}
		jobIDs = append(jobIDs, k.Job(Job{Name: fmt.Sprintf("%s %d", at(hugeTasks, i), i+1), Kind: kind, RRule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", TZ: "Asia/Makassar", Prompt: "x", ChatID: chatAt(i),
			Enabled: ptr(i%6 != 0), NextRunAt: ptr(now + (i+1)*10*MIN), LastRunAt: ptr(now - (i+1)*10*MIN), LastStatus: &lastStatus, LastResult: ptr("Selesai."), DailyTokenCap: ptr[int64](50_000), CreatedAt: ptr(now - D + i)}))
	}

	for i := int64(0); i < Huge.Runs; i++ {
		var jobID *string
		trigger := "message"
		if i%5 == 0 {
			jobID, trigger = ptr(at(jobIDs, i)), "schedule"
		}
		status := "ok"
		var errText *string
		if i%31 == 0 {
			status, errText = "error", ptr("429 from CLIProxyAPI")
		}
		chat := chatAt(i)
		k.Run(Run{ChatID: &chat, JobID: jobID, Trigger: trigger, Model: ptr(fmt.Sprintf("model-%02d", i%26)), TokensIn: ptr(4000 + (i%90)*150), TokensOut: ptr(300 + (i%40)*10), Status: status, Error: errText, StartedAt: now - i*9*MIN, Ms: ptr(2000 + (i%50)*300)})
	}

	for i := int64(0); i < Huge.Upcoming; i++ {
		k.Reminder(Reminder{ChatID: chatAt(i), Text: fmt.Sprintf("%s #%d", at(hugeTasks, i), 1000+i), TZ: "Asia/Makassar", FireAt: now + (i+1)*23*MIN, Status: "pending"})
	}
	for i := int64(0); i < Huge.Recurring; i++ {
		rrule := "FREQ=MONTHLY;BYMONTHDAY=1"
		if i%2 != 0 {
			rrule = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
		}
		k.Reminder(Reminder{ChatID: chatAt(i), Text: fmt.Sprintf("%s (rutin %d)", at(hugeTasks, i), i+1), RRule: &rrule, TZ: "Asia/Makassar", FireAt: now + (i+1)*47*MIN, Status: "pending"})
	}
	for i := int64(0); i < Huge.Done; i++ {
		status := "cancelled"
		if i%4 != 0 {
			status = "done"
		}
		k.Reminder(Reminder{ChatID: chatAt(i), Text: fmt.Sprintf("%s #%d", at(hugeTasks, i), i), TZ: "Asia/Makassar", FireAt: now - (i+1)*H, Status: status, UpdatedAt: ptr(now - (i+1)*H)})
	}

	for i := Huge.Events; i > 0; i-- {
		level := "info"
		if i%53 == 0 {
			level = "error"
		} else if i%17 == 0 {
			level = "warn"
		}
		connector := "cliproxy"
		if i%3 != 0 {
			connector = fmt.Sprintf("mcp-server-%02d", i%Huge.MCP)
		}
		kind, message := "status", "status change"
		if i%4 != 0 {
			kind, message = "tool", fmt.Sprintf("list_deals ok in %d ms", 100+(i%70)*13)
		}
		k.Event(Event{At: now - i*3*MIN, Level: level, ConnectorID: &connector, Kind: kind, Message: message})
	}

	for i := int64(0); i < Huge.Hooks; i++ {
		mode := "forward"
		if i%2 != 0 {
			mode = "summarize"
		}
		var lastAt *int64
		if i%5 != 0 {
			lastAt = ptr(now - i*H)
		}
		k.Hook(Hook{Name: fmt.Sprintf("%s alerts %d", at(hugeLast, i), i+1), ChatID: chatAt(i), Mode: mode, LastAt: lastAt})
	}
	for i := int64(0); i < Huge.Passkeys; i++ {
		deviceType := "multiDevice"
		if i%2 != 0 {
			deviceType = "singleDevice"
		}
		k.Passkey(Passkey{Name: ptr(fmt.Sprintf("%s's device %d", name(i), i+1)), DeviceType: deviceType, CreatedAt: ptr(now - i*D)})
	}

	// MCP access: the 50-token cap.
	for i := int64(0); i < 50; i++ {
		k.MCPGrant(MCPGrant{Name: fmt.Sprintf("%s's agent %d", name(i), i+1), Hint: ptr(fmt.Sprintf("%04d", i)), CreatedAt: now - i*D})
	}

	// Flows: the 50-per-chat cap in a few chats, and a run log a year of use leaves.
	flowChats := []db.Chat{k.WebChat()}
	for i := int64(0); i < Huge.FlowChats; i++ {
		flowChats = append(flowChats, k.Chat("telegram", fmt.Sprintf("flowchat%d", i), fmt.Sprintf("Flows %d", i+1), ChatOpts{}))
	}
	var flowIDs []string
	for i := int64(0); i < Huge.Flows; i++ {
		task := at(hugeTasks, i)
		def := obj(
			"name", fmt.Sprintf("%s %d", task, i+1),
			"triggers", []any{obj("id", "main", "kind", "schedule", "start", "say", "source", obj("kind", "times", "times", []any{obj("time", fmt.Sprintf("%02d:%02d", 6+i%14, (i*7)%60))}))},
			"steps", []any{obj("id", "say", "kind", "message", "text", task)},
		)
		var failures int64
		if i%13 == 0 {
			failures = 2
		}
		flowIDs = append(flowIDs, k.Flow(at(flowChats, i), def, FlowOpts{Enabled: ptr(i%9 != 0), Failures: failures, CreatedAt: ptr(now - (Huge.Flows-i)*H)}))
	}
	for i := int64(0); i < Huge.FlowRuns; i++ {
		status, stepStatus := "done", "ok"
		var skip, errText *string
		if i%11 == 0 {
			status, stepStatus, errText = "failed", "error", ptr("Step say: More than 5 messages in one run.")
		} else if i%17 == 0 {
			status, skip = "skipped", ptr("late")
		}
		k.FlowRun(flowIDs[i%7], FlowRun{Status: status, SkipReason: skip, TriggerID: ptr("main"), StartedAt: now - i*37*MIN, Error: errText, Steps: []FlowStep{{StepID: "say", Kind: "message", Status: stepStatus}}})
	}
}

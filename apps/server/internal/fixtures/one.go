package fixtures

// one.ts: exactly one of everything. This is the pluralization test ("1
// runs", "1 need attention") and the "list that looks broken with one row" test.
func one(k *Kit) {
	now := k.Now

	// One connector, and it's the one that needs attention.
	k.Connector("telegram", "telegram", ConnectorOpts{Status: "error", Detail: ptr("401 Unauthorized")})

	web := k.WebChat()
	k.Message(web, "user", "hi", now-MIN)

	k.Setting("modelRoles", js(`{"default":[{"connectorId":"cliproxy","model":"claude-sonnet-4-6"}],"fast":[{"connectorId":"cliproxy","model":"claude-haiku-4-5-20251001"}],"vision":[{"connectorId":"cliproxy","model":"claude-sonnet-4-6"}]}`))

	jobID := k.Job(Job{Name: "Daily briefing", Kind: "briefing", RRule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", TZ: "Asia/Makassar", Prompt: "Brief me.", ChatID: web.ID, NextRunAt: ptr(now + H), LastRunAt: ptr(now - MIN), LastStatus: ptr("ok"), LastResult: ptr("1 reminder today."), DailyTokenCap: ptr[int64](1000)})
	// One run today, one token in, one millisecond.
	k.Run(Run{ChatID: &web.ID, JobID: &jobID, Trigger: "schedule", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](1), TokensOut: ptr[int64](0), Steps: ptr[int64](1), Status: "ok", StartedAt: now - MIN, Ms: ptr[int64](1)})

	k.Reminder(Reminder{ChatID: web.ID, Text: "Bayar listrik", TZ: "Asia/Makassar", FireAt: now + H, Status: "pending"})
	k.Reminder(Reminder{ChatID: web.ID, Text: "Standup", RRule: ptr("FREQ=DAILY"), TZ: "Asia/Makassar", FireAt: now + 2*H, Status: "pending"})
	k.Reminder(Reminder{ChatID: web.ID, Text: "Old one", TZ: "Asia/Makassar", FireAt: now - H, Status: "done", LastFiredAt: ptr(now - H)})

	flow := k.Flow(web, js(`{"name":"Air","triggers":[{"id":"main","kind":"schedule","start":"say","source":{"kind":"times","times":[{"time":"11:00"}]}}],"steps":[{"id":"say","kind":"message","text":"Minum air"}]}`), FlowOpts{})
	k.FlowRun(flow, FlowRun{Status: "done", TriggerID: ptr("main"), StartedAt: now - MIN, Steps: []FlowStep{{StepID: "say", Kind: "message", Status: "ok", Output: obj("text", "Minum air")}}})

	k.Passkey(Passkey{Name: ptr("MacBook Air"), CreatedAt: ptr(now - 24*H)})
	k.MCPGrant(MCPGrant{Name: "Claude Code", Hint: ptr("x7Qe"), CreatedAt: now - 2*H, LastUsedAt: ptr(now - 10*MIN)})
	k.Hook(Hook{Name: "Uptime", ChatID: web.ID, LastAt: ptr(now - H)})
}

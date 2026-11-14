package chat_test

// Ports caching.test.ts: prompt caching across real turns (PLAN 5.2).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	cacheLong  = `{"anthropic":{"cacheControl":{"type":"ephemeral","ttl":"1h"}}}`
	cacheShort = `{"anthropic":{"cacheControl":{"type":"ephemeral"}}}`
)

func cachingChat(t *testing.T, steps []models.Step, provider string) *world {
	d := testutil.DB(t)
	chattest.OwnTelegram(t, d)
	chattest.ModelChain(t, d, "cliproxy")
	m := models.NewScripted(steps...)
	if provider != "" {
		m.WithProvider(provider)
	}
	var mu sync.Mutex
	recalls := 0
	mem := &connectors.MemoryHandle{AutoRecall: true, Recall: func(ctx context.Context, q string, o connectors.RecallOptions) (string, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		recalls++
		return fmt.Sprintf("recalled fact %d", recalls), true, nil
	}, Save: func(context.Context, connectors.SaveInput) error { return nil }}
	h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{
		"cliproxy": chattest.Model("cliproxy", m),
		"telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram")),
		"clock":    toolsRT("clock", tools("now", connectors.PaluiTool{Class: "read", Tool: conntest.FnTool("What time it is", ret("14.05"))})),
		"recall":   conntest.MemoryRuntime(mem),
	}, Rows: []chattest.Row{{"cliproxy", "model"}, {"telegram", "channel"}, {"clock", "tools"}, {"recall", "memory"}}})
	return &world{Harness: h, t: t, m: m}
}

// promptBytes is the prompt as the provider hashes it: cache markers left out.
func promptBytes(msgs []models.Message) string {
	var strip func(v any) any
	strip = func(v any) any {
		switch x := v.(type) {
		case *jsonx.Object:
			o := jsonx.NewMap[any]()
			for _, k := range x.Keys() {
				if k == "providerOptions" {
					continue
				}
				val, _ := x.Get(k)
				o.Set(k, strip(val))
			}
			return o
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = strip(e)
			}
			return out
		}
		return v
	}
	raw := need(models.MarshalMessages(msgs))
	return jsonx.Stringify(strip(need(jsonx.Parse(raw))))
}

func byRole(msgs []models.Message, system bool) []models.Message {
	var out []models.Message
	for _, m := range msgs {
		if (m.Role() == "system") == system {
			out = append(out, m)
		}
	}
	return out
}

func withoutTurnContext(m models.Message) models.Message {
	if !m.HasParts() {
		return m
	}
	var keep []models.Part
	for _, p := range m.Parts() {
		if p.Type() == "text" && strings.HasPrefix(p.Text(), chat.TurnContextTag) {
			continue
		}
		keep = append(keep, p)
	}
	c := m.Clone()
	c.SetParts(keep)
	return c
}

func lastPart(m models.Message) models.Part {
	parts := m.Parts()
	return parts[len(parts)-1]
}

func poJSON(p interface{ ProviderOptions() *jsonx.Object }) string {
	po := p.ProviderOptions()
	if po == nil {
		return "undefined"
	}
	return jsonx.Stringify(po)
}

func TestPromptCachingAcrossTurns(t *testing.T) {
	t.Run("sends a byte-identical prefix on two turns a minute apart", func(t *testing.T) {
		h := cachingChat(t, []models.Step{models.Say("kopi tubruk"), models.Say("soto banjar")}, "anthropic.messages")
		h.sendIdle("minuman apa yang aku suka")
		h.Clock.Add(60_000)
		h.sendIdle("terus makanan apa yang aku suka")
		first, second := h.m.Prompts[0], h.m.Prompts[1]
		tail := func(p []models.Message) string { return lastPart(p[len(p)-1]).Text() }
		contains(t, tail(first), "14.00 WITA")
		contains(t, tail(second), "14.01 WITA")
		contains(t, tail(first), "recalled fact 1")
		contains(t, tail(second), "recalled fact 2")
		eq(t, promptBytes(byRole(second, true)), promptBytes(byRole(first, true)))
		notContains(t, promptBytes(byRole(first, true)), "Now:")
		notContains(t, promptBytes(byRole(first, true)), "recalled fact")
		var sent []models.Message
		for _, m := range byRole(first, false) {
			sent = append(sent, withoutTurnContext(m))
		}
		again := byRole(second, false)[:len(sent)]
		eq(t, promptBytes(again), promptBytes(sent))
		eq(t, jsonx.Stringify(h.m.Offered[1]), jsonx.Stringify(h.m.Offered[0]))
	})

	t.Run("sets the breakpoints for the anthropic protocol", func(t *testing.T) {
		h := cachingChat(t, []models.Step{models.CallTool("clock__now", objOf(), ""), models.Say("jam dua lewat lima")}, "anthropic.messages")
		h.sendIdle("jam berapa sekarang?")
		step1, step2 := h.m.Prompts[0], h.m.Prompts[1]
		eq(t, poJSON(byRole(step1, true)[0]), cacheLong)
		user := step1[len(step1)-1].Parts()
		eq(t, poJSON(user[0])+" "+poJSON(user[1]), cacheLong+" undefined")
		if !strings.HasPrefix(user[1].Text(), chat.TurnContextTag) {
			t.Fatal(user[1].Text())
		}
		eq(t, poJSON(byRole(step2, true)[0]), cacheLong)
		eq(t, poJSON(lastPart(step2[len(step2)-1])), cacheShort)
		eq(t, strings.Count(models.PromptText(step2), `"cacheControl"`), 3)
		for _, p := range h.m.Prompts {
			notContains(t, models.PromptText(p), `"palui"`)
		}
	})

	t.Run("sets none for the openai protocol, and the turn still answers", func(t *testing.T) {
		h := cachingChat(t, []models.Step{models.CallTool("clock__now", objOf(), ""), models.Say("jam dua lewat lima")}, "cliproxy.chat")
		res := h.sendIdle("jam berapa sekarang?")
		if res == nil {
			t.Fatal("dropped")
		}
		eq(t, len(h.m.Prompts), 2)
		var all []string
		for _, p := range h.m.Prompts {
			all = append(all, models.PromptText(p))
		}
		text := strings.Join(all, "\n")
		notContains(t, text, "cacheControl")
		notContains(t, text, "providerOptions")
		notContains(t, text, `"palui"`)
		contains(t, text, chat.TurnContextTag)
	})
}

func usageOf(total, cacheRead, cacheWrite, output int64) models.Usage {
	return models.Usage{InputTokens: models.InputTokens{Total: i64p(total), NoCache: i64p(total - cacheRead - cacheWrite), CacheRead: i64p(cacheRead), CacheWrite: i64p(cacheWrite)}, OutputTokens: models.OutputTokens{Total: i64p(output), Text: i64p(output)}}
}

func withUsage(s models.Step, u models.Usage) models.Step {
	evs := append([]models.Event{}, s.Events...)
	evs[len(evs)-1].Usage = u
	return models.Step{Events: evs}
}

func TestCacheTokensOnARun(t *testing.T) {
	t.Run("records cache reads and writes, and counts the reads at a tenth against the global and agent caps", func(t *testing.T) {
		h := cachingChat(t, []models.Step{withUsage(models.Say("halo"), usageOf(52_000, 40_000, 2_000, 800))}, "anthropic.messages")
		h.sendIdle("halo")
		run := allRuns(t, h.DB)[0]
		eq(t, fmt.Sprintf("%s %d %d %d %d", run.Status, run.TokensIn, run.TokensOut, run.TokensCached, run.TokensCacheWrite), "ok 52000 800 40000 2000")
		eq(t, need(budget.TokensToday(h.DB, h.Clock.Now())), int64(16_800))
		eq(t, need(budget.AgentTokensToday(h.DB, need(agents.GetDefault(h.DB, 1)).ID, h.Clock.Now())), int64(16_800))
	})

	t.Run("adds the cache tokens of every step of a turn", func(t *testing.T) {
		call := withUsage(models.CallTool("clock__now", objOf(), ""), models.Usage{InputTokens: models.InputTokens{Total: i64p(10_000), NoCache: i64p(1_000), CacheRead: i64p(0), CacheWrite: i64p(9_000)}, OutputTokens: models.OutputTokens{Total: i64p(20), Text: i64p(20)}})
		h := cachingChat(t, []models.Step{call, withUsage(models.Say("jam dua"), usageOf(10_500, 10_000, 400, 30))}, "anthropic.messages")
		h.sendIdle("jam berapa sekarang?")
		run := allRuns(t, h.DB)[0]
		eq(t, fmt.Sprintf("%d %d %d %d %d", run.Steps, run.TokensIn, run.TokensOut, run.TokensCached, run.TokensCacheWrite), "2 20500 50 10000 9400")
		eq(t, need(budget.TokensToday(h.DB, h.Clock.Now())), int64(10_500+1_000+50))
	})
}

var _ = json.Marshal

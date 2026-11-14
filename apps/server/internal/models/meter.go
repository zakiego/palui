package models

import (
	"context"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/db"
)

// Count the tokens of model calls made outside a turn (compaction, reminder
// parsing), so they go toward the daily token cap too (PLAN 5.2).

// UsageTokens is usageTokens: tokens as they count against the daily cap,
// cache reads at a tenth.
func UsageTokens(u *Usage) int64 {
	if u == nil {
		return 0
	}
	var cached *int64
	if u.InputTokens.CacheRead != nil {
		cached = u.InputTokens.CacheRead
	}
	return budget.Tokens(budget.Usage{TokensIn: val(u.InputTokens.Total), TokensOut: val(u.OutputTokens.Total), TokensCached: cached})
}

// MeteredModel reports the tokens of every call it serves.
type MeteredModel struct {
	Model    LanguageModel
	OnTokens func(tokens int64)
}

func (m *MeteredModel) Provider() string { return m.Model.Provider() }
func (m *MeteredModel) ModelID() string  { return m.Model.ModelID() }

func (m *MeteredModel) Stream(ctx context.Context, opts CallOptions) (Stream, error) {
	s, err := m.Model.Stream(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &meteredStream{Stream: s, report: m.OnTokens}, nil
}

type meteredStream struct {
	Stream
	report func(int64)
}

func (s *meteredStream) Recv() (Event, error) {
	ev, err := s.Stream.Recv()
	if err == nil && ev.Type == EventFinish {
		if n := UsageTokens(&ev.Usage); n > 0 && s.report != nil {
			s.report(n)
		}
	}
	return ev, err
}

// MeterToBudget is meterToBudget: the global daily token counter as the sink.
func MeterToBudget(q db.Q, model LanguageModel, clock func() int64) LanguageModel {
	return &MeteredModel{Model: model, OnTokens: func(n int64) { _ = budget.RecordRunTokens(q, n, clock()) }}
}

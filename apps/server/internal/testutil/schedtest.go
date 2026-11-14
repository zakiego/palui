package testutil

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// FakeClock is reminders/test-helpers.ts fakeClock: a clock plus timers that
// record what was armed, for driving the scheduler without real sleeps. It
// implements reminders.Timers.
type FakeClock struct {
	mu      sync.Mutex
	now     int64
	seq     int
	pending map[int]*fakeTimer
	armed   []int64
}

type fakeTimer struct {
	id    int
	fn    func()
	at    int64
	every int64
}

func NewFakeClock(start int64) *FakeClock {
	return &FakeClock{now: start, pending: map[int]*fakeTimer{}}
}

func (c *FakeClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) Set(t int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Armed is every timeout delay armed so far.
func (c *FakeClock) Armed() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.armed)
}

// LastArmed is armed.at(-1) (-1 when nothing was armed).
func (c *FakeClock) LastArmed() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.armed) == 0 {
		return -1
	}
	return c.armed[len(c.armed)-1]
}

// Pending is how many timers are set.
func (c *FakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func (c *FakeClock) SetTimeout(fn func(), ms int64) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = append(c.armed, ms)
	c.seq++
	c.pending[c.seq] = &fakeTimer{id: c.seq, fn: fn, at: c.now + ms}
	return c.seq
}

func (c *FakeClock) ClearTimeout(h any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := h.(int); ok {
		delete(c.pending, id)
	}
}

func (c *FakeClock) SetInterval(fn func(), ms int64) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.pending[c.seq] = &fakeTimer{id: c.seq, fn: fn, at: c.now + ms, every: ms}
	return c.seq
}

func (c *FakeClock) ClearInterval(h any) { c.ClearTimeout(h) }

// Advance moves time forward, running due timers in order (earliest first,
// then the one set first) and flush after each. It returns how many
// callbacks ran.
func (c *FakeClock) Advance(ms int64, flush func()) int {
	c.mu.Lock()
	end := c.now + ms
	c.mu.Unlock()
	ran := 0
	for {
		c.mu.Lock()
		var due *fakeTimer
		for _, t := range c.pending {
			if t.at <= end && (due == nil || t.at < due.at || (t.at == due.at && t.id < due.id)) {
				due = t
			}
		}
		if due == nil {
			c.mu.Unlock()
			break
		}
		c.now = max(c.now, due.at)
		if due.every > 0 {
			due.at += due.every
		} else {
			delete(c.pending, due.id)
		}
		fn := due.fn
		c.mu.Unlock()
		fn()
		ran++
		if flush != nil {
			flush()
		}
		if ran > 10_000 {
			panic("timer loop")
		}
	}
	c.mu.Lock()
	c.now = end
	c.mu.Unlock()
	return ran
}

// TableRows is SELECT * ... ORDER BY rowid as ordered column -> value maps,
// integers as float64 (JSON numbers like the TS rows).
func TableRows(t testing.TB, q db.Q, table, where string) []any {
	t.Helper()
	rows, err := q.Query(fmt.Sprintf("SELECT * FROM %s %s ORDER BY rowid", table, where))
	Must(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	Must(t, err)
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		Must(t, rows.Scan(ptrs...))
		o := jsonx.NewMap[any]()
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if f, ok := v.(int64); ok {
				v = float64(f)
			}
			o.Set(c, v)
		}
		out = append(out, o)
	}
	Must(t, rows.Err())
	return out
}

// Without returns the rows with a column dropped (events without id).
func Without(rows []any, col string) []any {
	for _, r := range rows {
		r.(*jsonx.Object).Delete(col)
	}
	return rows
}

var GoldenID = regexp.MustCompile(`\b([a-z]{2,6})_[0-9a-z]{19}\b`)

// NormalizeGolden is the generators' normalize: sorted keys, and every newId()
// id becomes <prefix>#N in order of first appearance.
func NormalizeGolden(v any) string {
	s := jsonx.Canonical(v)
	seen := map[string]string{}
	return GoldenID.ReplaceAllStringFunc(s, func(m string) string {
		if r, ok := seen[m]; ok {
			return r
		}
		r := fmt.Sprintf("%s#%d", GoldenID.FindStringSubmatch(m)[1], len(seen)+1)
		seen[m] = r
		return r
	})
}

// ParseJSONColumns parses string values under the given keys that hold a JSON
// object (outbox payload, settings value, events data), anywhere in v, so
// stored JSON compares by content, not key order.
func ParseJSONColumns(v any, keys ...string) any {
	switch x := v.(type) {
	case []any:
		for i := range x {
			x[i] = ParseJSONColumns(x[i], keys...)
		}
	case *jsonx.Object:
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			if s, ok := val.(string); ok && slices.Contains(keys, k) && strings.HasPrefix(s, "{") {
				if p, err := jsonx.Parse([]byte(s)); err == nil {
					x.Set(k, p)
					continue
				}
			}
			x.Set(k, ParseJSONColumns(val, keys...))
		}
	}
	return v
}

// DiffAt shows where two long strings first differ.
func DiffAt(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-200)
	return fmt.Sprintf("first difference at %d:\n go: %s\n ts: %s", i, a[lo:min(len(a), i+200)], b[lo:min(len(b), i+200)])
}

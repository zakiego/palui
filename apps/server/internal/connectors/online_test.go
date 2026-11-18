package connectors_test

import (
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestSupervisorOnOnlineFiresOnlyOnTheWayBackOn(t *testing.T) {
	var mu sync.Mutex
	var got []string
	wa := live("whatsapp", "channel", extra{}, nil)
	h := setup(t, map[string]*fake{"whatsapp": wa}, func(o *connectors.SupervisorOptions) {
		o.BackoffMs = []int64{20, 40, 80}
		o.OnOnline = func(id string) {
			mu.Lock()
			got = append(got, id)
			mu.Unlock()
		}
	})
	seed(t, h.db, "whatsapp", "channel", testutil.SeedOptions{})
	h.sup.Reconcile()
	calls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(got)
	}
	// The start itself turns it on once.
	waitUntil(t, func() bool { return calls() == 1 })
	// Already on: nothing new.
	wa.ctx(0).SetStatus("on", nil)
	// A socket reconnect: connecting, then on again.
	wa.ctx(0).SetStatus("connecting", detail("Reconnecting in 2s (close 428)"))
	wa.ctx(0).SetStatus("on", nil)
	waitUntil(t, func() bool { return calls() == 2 })
	mu.Lock()
	defer mu.Unlock()
	if got[0] != "whatsapp" || got[1] != "whatsapp" {
		t.Fatal(got)
	}
}

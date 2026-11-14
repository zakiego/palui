package connectors

// Connector edge-case rules (PLAN 4.3) that need more than one table, from
// connectors/rules.ts.

import (
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
)

// OwnerChat is ownerChat (rules.ts); it lives in chats so the outbox worker
// can use it without importing this package.
func OwnerChat(q db.Q, channel string, now int64) (db.Chat, bool, error) {
	return chats.OwnerChat(q, channel, now)
}

// DashboardReachable: a passkey exists, so the owner can sign in.
func DashboardReachable(q db.Q) (bool, error) {
	var n int
	err := q.QueryRow("SELECT count(*) FROM (SELECT id FROM passkey LIMIT 1)").Scan(&n)
	return n > 0, err
}

// ChannelEntry is what SelfChannelOffUndo needs of a running connector.
type ChannelEntry struct {
	ID      string
	Kind    string
	Channel ChannelHandle
}

func safeReachable(ch ChannelHandle) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return ch.Reachable()
}

// SelfChannelOffUndo: `/connectors off <channel>` sent from that same channel
// is allowed only when another channel is reachable or the dashboard is.
// Returns the way back for the reply, or "" (ok false) when turning it off
// would lock the owner out.
func SelfChannelOffUndo(q db.Q, from string, running []ChannelEntry, targetID string) (string, bool, error) {
	var others []string
	for _, e := range running {
		ch := e.Channel
		if ch == nil || e.ID == targetID || ch.Channel() == from || ch.Channel() == db.ChannelWeb {
			continue
		}
		if safeReachable(ch) {
			others = append(others, ch.Channel())
		}
	}
	var ways []string
	if len(others) > 0 {
		names := make([]string, len(others))
		for i, o := range others {
			names[i] = ChannelName(o)
		}
		ways = append(ways, "kirim `/connectors on "+targetID+"` dari "+strings.Join(names, " atau "))
	}
	reachable, err := DashboardReachable(q)
	if err != nil {
		return "", false, err
	}
	if reachable {
		ways = append(ways, "nyalakan lagi dari dashboard")
	}
	if len(ways) == 0 {
		return "", false, nil
	}
	return strings.Join(ways, ", atau "), true, nil
}

func ChannelName(ch string) string {
	switch ch {
	case "telegram":
		return "Telegram"
	case "whatsapp":
		return "WhatsApp"
	case "web":
		return "web chat"
	}
	return ch
}

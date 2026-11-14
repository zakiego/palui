package outbox

import (
	"context"
	"database/sql"

	"github.com/zakiego/palui/apps/server/internal/db"
)

// SendResult is what a channel answers for a sent message.
type SendResult struct {
	ChannelMsgID string
}

// Channel is a running channel as the worker uses it (ChannelHandle in
// connectors/define.ts; connectors.ChannelHandle is this type).
type Channel interface {
	Channel() string
	Send(ctx context.Context, chatExternalID string, payload db.OutboxPayload) (SendResult, error)
	// Whether the socket is up right now.
	Reachable() bool
}

var errNoRows = sql.ErrNoRows

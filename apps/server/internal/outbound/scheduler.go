package outbound

// Scheduled and repeating sends to contacts (outbound/scheduler.ts), swept on
// the reminder scheduler's timer as a reminders.Extension, like flows. A row is
// claimed scheduled -> firing in one UPDATE ... RETURNING, so two sweeps never
// send the same occurrence twice. Every fire runs the same guards as a send
// from chat: kill switch, contact still saved, rate limits. No model runs
// here; the text is the one approved at creation.

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
)

// SchedulerExt implements reminders.Extension and reminders.Recoverer.
type SchedulerExt struct {
	DB  db.Q
	Log *logx.Logger
}

func NewScheduler(q db.Q, log *logx.Logger) *SchedulerExt {
	if log == nil {
		log = logx.Silent
	}
	return &SchedulerExt{DB: q, Log: log}
}

// advance: one-off -> done; series -> next occurrence after now (missed runs collapse).
func (s *SchedulerExt) advance(q db.Q, r db.OutboundMessage, now int64, outboxID string) error {
	cols := "updated_at = ?"
	args := []any{now}
	if outboxID != "" {
		cols += ", last_outbox_id = ?"
		args = append(args, outboxID)
	}
	var next int64
	ok := false
	if r.RRule != nil && *r.RRule != "" {
		var err error
		next, ok, err = reminders.NextOccurrence(reminders.Schedule{RRule: r.RRule, TZ: r.TZ, LocalStart: r.LocalStart}, now)
		if err != nil {
			s.Log.Error("outbound rule broke, stopping the series", logx.Fields{"outbound_id": r.ID, "err": err})
			ok = false
		}
	}
	if !ok {
		cols += ", status = 'done'"
	} else {
		cols += ", status = 'scheduled', fire_at = ?"
		args = append(args, next)
	}
	_, err := q.Exec("UPDATE outbound_messages SET "+cols+" WHERE id = ? AND status = 'firing'", append(args, r.ID)...)
	return err
}

func (s *SchedulerExt) cancel(r db.OutboundMessage, now int64) error {
	_, err := s.DB.Exec("UPDATE outbound_messages SET status = 'cancelled', updated_at = ? WHERE id = ?", now, r.ID)
	return err
}

func (s *SchedulerExt) fire(r db.OutboundMessage, now int64) (bool, error) {
	q := s.DB
	contact, found, err := GetContact(q, r.ContactID)
	if err != nil {
		return false, err
	}
	who := "kontak"
	if found {
		who = contact.Name
	}
	if !found || contact.RemovedAt != nil {
		if err := s.cancel(r, now); err != nil {
			return false, err
		}
		_, err := events.Log(q, events.Input{Kind: "outbound.orphan", Level: "warn", Message: fmt.Sprintf("Scheduled message %s cancelled: its contact is gone", r.ID), Data: obj("outboundId", r.ID)}, now)
		return false, err
	}
	if now-r.FireAt > OutboundLateSkipMs {
		if err := s.advance(q, r, now, ""); err != nil {
			return false, err
		}
		return false, NoticeToOwner(q, r.OwnerChatID, fmt.Sprintf("Pesan terjadwal ke %s kelewat (Palui mati waktu jadwalnya), jadi nggak aku kirim:\n\n%s", who, r.Text), "system", nil, now)
	}
	if err := AssertCanSend(q, contact, now); err != nil {
		var oe *OutboundError
		if !errors.As(err, &oe) {
			return false, err
		}
		if err := s.advance(q, r, now, ""); err != nil {
			return false, err
		}
		if _, err := events.Log(q, events.Input{Kind: "outbound.skipped", Level: "warn", Message: fmt.Sprintf("Scheduled message to %s skipped: %s", who, oe.Msg), Data: obj("outboundId", r.ID)}, now); err != nil {
			return false, err
		}
		return false, NoticeToOwner(q, r.OwnerChatID, fmt.Sprintf("Pesan terjadwal ke %s nggak aku kirim: %s", who, oe.Msg), "system", nil, now)
	}
	chat, ok, err := chats.Get(q, contact.ChatID)
	if err != nil {
		return false, err
	}
	if !ok {
		if err := s.cancel(r, now); err != nil {
			return false, err
		}
		return false, NoticeToOwner(q, r.OwnerChatID, fmt.Sprintf("Pesan terjadwal ke %s dibatalin: chat-nya udah nggak ada.", who), "system", nil, now)
	}
	expires := now + OutboundTTLMs
	return true, q.Tx(func(tx db.Q) error {
		row, err := outbox.Enqueue(tx, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: r.Text, Format: "plain"}, Source: SourceOf(r.ID), ExpiresAt: &expires}, now)
		if err != nil {
			return err
		}
		return s.advance(tx, r, now, row.ID)
	})
}

func (s *SchedulerExt) Recover(now int64) error {
	_, err := s.DB.Exec("UPDATE outbound_messages SET status = 'scheduled', updated_at = ? WHERE status = 'firing'", now)
	return err
}

func (s *SchedulerExt) Sweep(now int64) (int, error) {
	q := s.DB
	rows, qerr := q.WQuery("UPDATE outbound_messages SET status = 'firing', updated_at = ? WHERE status = 'scheduled' AND fire_at <= ? RETURNING "+db.OutboundMessageCols, now, now)
	claimed, err := db.All(rows, qerr, db.ScanOutboundMessage)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, r := range claimed {
		ok, err := s.fire(r, now)
		if err != nil {
			s.Log.Error("outbound fire failed", logx.Fields{"outbound_id": r.ID, "err": err})
			_, _ = q.Exec("UPDATE outbound_messages SET status = 'scheduled' WHERE id = ? AND status = 'firing'", r.ID)
			continue
		}
		if ok {
			sent++
		}
	}
	if sent > 0 {
		outbox.Notify()
	}
	return len(claimed), nil
}

func (s *SchedulerExt) NextDue() (int64, bool, error) {
	var v sql.NullInt64
	if err := s.DB.QueryRow("SELECT min(fire_at) FROM outbound_messages WHERE status = 'scheduled'").Scan(&v); err != nil {
		return 0, false, err
	}
	return v.Int64, v.Valid, nil
}

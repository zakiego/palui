package tgbot_test

import (
	"context"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram/tgtest"
	"github.com/zakiego/palui/apps/server/internal/connectors/tgbot"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// tgbot has no TS test of its own; these check that it is the Telegram
// runtime under its own manifest.
func TestManifestSpreadsTelegram(t *testing.T) {
	m := tgbot.Manifest
	if m.ID != "tgbot" || !m.Multiple || m.Kind != "channel" || len(m.Setup) != 1 || m.Setup[0].Field != "botToken" {
		t.Fatalf("%+v", m)
	}
	if m.RemoveWarning != telegram.Manifest.RemoveWarning || len(m.SecretFields) != 1 || m.ParseConfig == nil || m.ParseSecrets == nil {
		t.Fatal("telegram fields not carried over")
	}
	if telegram.Manifest.ID != "telegram" || len(telegram.Manifest.Setup) != 2 {
		t.Fatal("the telegram manifest changed")
	}
}

func TestRunsTheTelegramRuntime(t *testing.T) {
	c := config.Config{Mode: "prod"}
	config.Set(&c)
	t.Cleanup(func() { config.Set(nil) })
	fake := tgtest.New()
	rt := tgbot.New(telegram.Options{NewTransport: fake.NewTransport})
	if rt.Manifest.ID != "tgbot" {
		t.Fatal(rt.Manifest.ID)
	}
	ctx := conntest.NewChannelCtx(t, "tgbot-kerja", "tgbot", nil, map[string]string{"botToken": "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"}, "")
	r, err := rt.Start(ctx.Context)
	testutil.Must(t, err)
	defer r.Stop(context.Background())
	fake.WaitPolling()
	conntest.Until(t, func() bool { return ctx.LastStatus() == "on" })
	if r.Channel.Channel() != "telegram" {
		t.Fatal(r.Channel.Channel())
	}
	fake.FeedJSON(`{"update_id":1,"message":{"message_id":5,"date":1,"chat":{"id":7,"type":"private"},"from":{"id":7,"is_bot":false,"first_name":"A"},"text":"halo"}}`)
	in := ctx.Inbound()
	if len(in) != 1 || in[0].ConnectorID != "tgbot-kerja" || in[0].Channel != "telegram" {
		t.Fatalf("%+v", in)
	}
}

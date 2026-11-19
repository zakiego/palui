package chat_test

// Voice notes (rpt_0mwmyqranb28lw1ivey): the owner's voice note reached the
// model as "transcription off for this agent" with no way to fix it, and the
// audio was gone. A note that isn't transcribed now says why, links the page
// that fixes it, keeps the audio, and a temporary failure is retried.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/voice"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

var voiceAudio = base64.StdEncoding.EncodeToString([]byte("opus bytes"))

func ownerVoiceRow(t *testing.T, d *db.DB) db.Message {
	t.Helper()
	_, row, err := chat.WriteInbound(d, tgMsg("", func(m *connectors.InboundMessage) {
		m.Media = []connectors.InboundMedia{{Kind: "audio", MimeType: "audio/ogg", Data: voiceAudio}}
	}), 1, true)
	testutil.Must(t, err)
	return row
}

func transcriber(fn func() (string, error)) []connectors.RunningEntry {
	return []connectors.RunningEntry{{ID: "voice", Handle: &connectors.RunningConnector{Transcribe: func(context.Context, connectors.InboundMedia) (string, error) { return fn() }}}}
}

const appURL = "https://palui.example"

func TestPrepareMediaVoiceReasons(t *testing.T) {
	on := chat.VoiceSetup{On: true, AppURL: appURL, AgentSlug: "palui"}

	t.Run("transcribes the owner's voice note when the agent has voice notes on", func(t *testing.T) {
		d := testutil.DB(t)
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), transcriber(func() (string, error) { return "ingetin aku besok", nil }), on, nil))
		eq(t, row.Text, "[voice] ingetin aku besok")
		contains(t, string(row.Content), `"note":"transcribed by voice"`)
		notContains(t, string(row.Content), voiceAudio)
	})

	t.Run("switched off for the agent: says so, links the switch and keeps the audio", func(t *testing.T) {
		d := testutil.DB(t)
		off := on
		off.On = false
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), transcriber(func() (string, error) { return "x", nil }), off, nil))
		contains(t, row.Text, "[voice] (pesan suara, gak ditranskrip: transkripsi voice note dimatiin buat agent ini")
		contains(t, row.Text, appURL+"/app/agents/palui?tab=settings")
		contains(t, string(row.Content), `"note":"transcription off for this agent"`)
		contains(t, string(row.Content), voiceAudio)
	})

	t.Run("no transcriber set up: says so and links where to add one", func(t *testing.T) {
		d := testutil.DB(t)
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), nil, on, nil))
		contains(t, row.Text, "belum ada connector transkripsi suara")
		contains(t, row.Text, appURL+"/app/connectors/new")
		contains(t, string(row.Content), `"note":"no transcriber set up"`)
		contains(t, string(row.Content), voiceAudio)
	})

	t.Run("transcriber switched off: says so and links the connector", func(t *testing.T) {
		d := testutil.DB(t)
		no := false
		testutil.SeedConnector(t, d, "voice", "skill", testutil.SeedOptions{Enabled: &no}, 1)
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), nil, on, nil))
		contains(t, row.Text, "lagi dimatiin")
		contains(t, row.Text, appURL+"/app/connectors/voice")
		contains(t, string(row.Content), `"note":"transcriber off"`)
	})

	t.Run("transcriber on but not up yet: asks for a retry, then reports it", func(t *testing.T) {
		d := testutil.DB(t)
		testutil.SeedConnector(t, d, "voice", "skill", testutil.SeedOptions{Status: "error"}, 1)
		orig := ownerVoiceRow(t, d)
		_, err := chat.PrepareMedia(context.Background(), d, orig, nil, on, nil)
		var later *chat.TranscribeLaterError
		if !errors.As(err, &later) {
			t.Fatalf("want TranscribeLaterError, got %v", err)
		}
		final := on
		final.Final = true
		row := need(chat.PrepareMedia(context.Background(), d, orig, nil, final, nil))
		contains(t, row.Text, "lagi gak jalan (error)")
		contains(t, string(row.Content), voiceAudio)
	})

	t.Run("a provider outage asks for a retry and leaves the row as it was", func(t *testing.T) {
		d := testutil.DB(t)
		orig := ownerVoiceRow(t, d)
		_, err := chat.PrepareMedia(context.Background(), d, orig, transcriber(func() (string, error) {
			return "", &voice.Error{Status: 503, Message: "ElevenLabs answered 503: busy"}
		}), on, nil)
		var later *chat.TranscribeLaterError
		if !errors.As(err, &later) {
			t.Fatalf("want TranscribeLaterError, got %v", err)
		}
		contains(t, later.Cause, "503")
		now := need(db.ScanMessage(d.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE id = ?", orig.ID)))
		eq(t, string(now.Content), string(orig.Content))
		eq(t, now.Text, "")
	})

	t.Run("after the retries, a temporary failure is reported and the audio kept", func(t *testing.T) {
		d := testutil.DB(t)
		final := on
		final.Final = true
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), transcriber(func() (string, error) {
			return "", &voice.Error{Status: 429, Message: "ElevenLabs answered 429: slow down"}
		}), final, nil))
		contains(t, row.Text, "gangguan sementara")
		contains(t, row.Text, "udah dicoba ulang")
		contains(t, string(row.Content), `"note":"transcription failed after retries"`)
		contains(t, string(row.Content), voiceAudio)
	})

	t.Run("a bad key fails at once with the error and the connector link", func(t *testing.T) {
		d := testutil.DB(t)
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), transcriber(func() (string, error) {
			return "", &voice.Error{Status: 401, Message: "ElevenLabs rejected the API key (401): invalid"}
		}), on, nil))
		contains(t, row.Text, "transkripsinya gagal (ElevenLabs rejected the API key (401)")
		contains(t, row.Text, appURL+"/app/connectors/voice")
		contains(t, string(row.Content), `"note":"transcription failed"`)
		contains(t, string(row.Content), voiceAudio)
	})

	t.Run("a note over the provider's size limit says it's too long", func(t *testing.T) {
		d := testutil.DB(t)
		row := need(chat.PrepareMedia(context.Background(), d, ownerVoiceRow(t, d), transcriber(func() (string, error) {
			return "", &voice.Error{Status: 413, Message: "The voice note is larger than 25 MB"}
		}), on, nil))
		contains(t, row.Text, "kepanjangan")
		contains(t, string(row.Content), `"note":"too long"`)
	})
}

// voiceWorld is the owner on Telegram with voice notes on and a voice
// connector whose transcription answers from fn.
func voiceWorld(t *testing.T, fn func() (string, error)) *world {
	t.Helper()
	rt := conntest.ToolsRuntime("voice", tools(), "skill", func(h *connectors.RunningConnector) {
		h.Transcribe = func(context.Context, connectors.InboundMedia) (string, error) { return fn() }
	})
	w := basic(t, nil, extra{registry: map[string]*connectors.Runtime{"voice": rt}, rows: []chattest.Row{{"voice", "skill"}}})
	def := need(agents.GetDefault(w.DB, 1))
	yes := true
	need(agents.Update(w.DB, def.ID, agents.Input{TranscribeVoice: &yes}, 1))
	testutil.Must(t, w.Engine.Start())
	return w
}

func sendVoice(m *connectors.InboundMessage) {
	m.Media = []connectors.InboundMedia{{Kind: "audio", MimeType: "audio/ogg", Data: voiceAudio}}
}

func TestVoiceTranscriptionRetry(t *testing.T) {
	t.Run("retries a temporary failure and answers the transcript", func(t *testing.T) {
		calls := 0
		w := voiceWorld(t, func() (string, error) {
			calls++
			if calls == 1 {
				return "", &voice.Error{Status: 503, Message: "ElevenLabs answered 503: busy"}
			}
			return "ingetin aku besok", nil
		})
		w.m.WithFallback(models.Say("siap"))
		r := w.sendIdle("", sendVoice)
		// Nothing reaches the model yet, and nothing is said: a blip stays silent.
		eq(t, len(w.m.Prompts), 0)
		eq(t, len(outboxTexts(t, w.DB)), 0)
		eq(t, count(t, w.DB, "messages WHERE id = ? AND status = 'pending'", r.MessageID), 1)
		jsonEq(t, w.Engine.RetryWaiting(), fmt.Sprintf(`[%q]`, r.MessageID))
		eq(t, eventCount(t, w, "turn.retry"), 1)

		w.Engine.RetryNow()
		w.Engine.Idle()
		eq(t, calls, 2)
		row := need(db.ScanMessage(w.DB.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE id = ?", r.MessageID)))
		eq(t, row.Text, "[voice] ingetin aku besok")
		eq(t, row.Status, "handled")
		contains(t, models.PromptText(w.m.Prompts[0]), "[voice] ingetin aku besok")
		jsonEq(t, outboxTexts(t, w.DB), `["siap"]`)
		eq(t, len(w.Engine.RetryWaiting()), 0)
	})

	t.Run("gives up after the cap with an accurate note, telling the owner once", func(t *testing.T) {
		w := voiceWorld(t, func() (string, error) {
			return "", &voice.Error{Status: 503, Message: "ElevenLabs answered 503: busy"}
		})
		w.m.WithFallback(models.Say("maaf, belum kedengeran"))
		r := w.sendIdle("", sendVoice)
		for range chat.RetryDelaysMs {
			w.Engine.RetryNow()
			w.Engine.Idle()
		}
		texts := outboxTexts(t, w.DB)
		eq(t, countText(texts, "Voice note-mu belum bisa ditranskrip"), 1)
		eq(t, countText(texts, "maaf, belum kedengeran"), 1)
		eq(t, eventCount(t, w, "turn.retry"), len(chat.RetryDelaysMs))
		eq(t, eventCount(t, w, "turn.retry_exhausted"), 1)
		row := need(db.ScanMessage(w.DB.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE id = ?", r.MessageID)))
		contains(t, row.Text, "gangguan sementara")
		contains(t, string(row.Content), voiceAudio)
		eq(t, row.Status, "handled")
		eq(t, len(w.Engine.RetryWaiting()), 0)
	})

	t.Run("a newer message doesn't pull the waiting voice note into its turn", func(t *testing.T) {
		calls := 0
		w := voiceWorld(t, func() (string, error) {
			calls++
			if calls == 1 {
				return "", errors.New("Post \"https://api.elevenlabs.io\": net/http: TLS handshake timeout")
			}
			return "beli gula", nil
		})
		w.m.WithFallback(models.Say("oke"))
		voiceMsg := w.sendIdle("", sendVoice)
		w.sendIdle("halo")
		eq(t, len(w.m.Prompts), 1)
		eq(t, calls, 1)
		notContains(t, models.PromptText(w.m.Prompts[0]), "[voice] (")
		eq(t, count(t, w.DB, "messages WHERE id = ? AND status = 'pending'", voiceMsg.MessageID), 1)
		w.Engine.RetryNow()
		w.Engine.Idle()
		contains(t, models.PromptText(w.m.Prompts[1]), "[voice] beli gula")
	})
}

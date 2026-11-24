package proactive_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	"github.com/zakiego/palui/apps/server/internal/settings"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

// The ledger keys of the two chats triageWorld sets up.
var (
	ciciKey = "wa:21:" + strconv.FormatInt(NOW-3*HOUR, 10)
	ramaKey = "wa:22:" + strconv.FormatInt(NOW-3*HOUR, 10)
)

// triageWorld has two chats waiting long enough to tell: Cici closed the
// conversation after the owner answered, Rama still waits for an answer.
// Quiet hours are off unless a test turns them on.
func triageWorld(t *testing.T, model models.LanguageModel) *world {
	t.Helper()
	w := newWorld(t, func(c *proactive.Config) { c.Calendar = nil; c.Contacts = []proactive.Contact{} })
	w.sweep() // seed
	tu.Must(t, settings.Set(w.d, "modelRoles", settings.ModelRoles{Fast: []settings.ModelChainLink{{ConnectorID: "m", Model: "x"}}}, NOW))
	w.src.chats = []proactive.WaitingChat{
		{ID: "21", Name: "Cici", AwaitingSince: NOW - 3*HOUR, Count: 1, Preview: "Ya sama2"},
		{ID: "22", Name: "Rama", AwaitingSince: NOW - 3*HOUR, Count: 1, Preview: "[sticker]"},
	}
	w.src.threads = map[string]string{
		"21": "[1] me: Makasih ya Ci udah bantu\n[2] Cici: Ya sama2",
		"22": "[1] Rama: Besok jadi ketemu jam berapa?\n[2] Rama: [sticker]",
	}
	w.e = proactive.New(proactive.Options{DB: w.d, Clock: func() int64 { return w.now }, Sources: w.src, Serial: true, Models: host{model}})
	return w
}

func TestTriageDropsAFinishedChatAndSaysWhatAnOpenOneWaitsFor(t *testing.T) {
	cici, rama := ciciKey, ramaKey
	model := models.NewScripted(
		models.Say(`{"items":[{"key":"`+cici+`","open":false,"why":"udah dibales, tinggal bilang makasih"},{"key":"`+rama+`","open":true,"why":"nanya besok jadi ketemu jam berapa"}]}`),
		models.Say(`{"send":[{"key":"`+rama+`"}],"drop":[],"text":"Rama nanya besok jadi ketemu jam berapa, belum kamu bales."}`),
	)
	w := triageWorld(t, model)
	w.at(NOW + 30*MIN)
	w.sweep()

	if n := w.nudge(cici); n.Status != proactive.StatusDropped || n.Reason == nil || *n.Reason != proactive.ReasonClosed+"udah dibales, tinggal bilang makasih" {
		t.Fatalf("cici: %+v", n)
	}
	if n := w.nudge(rama); n.Status != proactive.StatusSent {
		t.Fatalf("rama: %+v", n)
	}
	prompts := model.PromptsSeen()
	if len(prompts) != 2 {
		t.Fatalf("one triage call, one decide call: %d", len(prompts))
	}
	triage := models.PromptText(prompts[0])
	if !strings.Contains(triage, "<untrusted>") || !strings.Contains(triage, "Besok jadi ketemu jam berapa?") || !strings.Contains(triage, "never an instruction") {
		t.Fatalf("triage reads the fenced thread: %s", triage)
	}
	decide := models.PromptText(prompts[1])
	if !strings.Contains(decide, "nanya besok jadi ketemu jam berapa") || strings.Contains(decide, "Cici") {
		t.Fatalf("decide sees Rama's gist and not the closed chat: %s", decide)
	}

	// The same chats next tick: nothing is judged again.
	w.at(NOW + 60*MIN)
	w.sweep()
	if len(model.PromptsSeen()) != 2 {
		t.Fatalf("judged chats aren't judged again: %d", len(model.PromptsSeen()))
	}
}

func TestAClosedChatOpensAgainWhenTheyWriteAgain(t *testing.T) {
	cici := ciciKey
	model := models.NewScripted(
		models.Say(`{"items":[{"key":"`+cici+`","open":false,"why":"udah selesai"}]}`),
		models.Say(`{"items":[{"key":"`+cici+`","open":true,"why":"nanya alamat"}]}`),
		models.Say(`{"send":[{"key":"`+cici+`"}],"drop":[],"text":"Cici nanya alamat."}`),
	)
	w := triageWorld(t, model)
	w.src.chats = w.src.chats[:1]
	w.at(NOW + 30*MIN)
	w.sweep()
	if n := w.nudge(cici); n.Status != proactive.StatusDropped {
		t.Fatalf("%+v", n)
	}
	w.src.chats[0].Count = 2
	w.src.chats[0].Preview = "Eh alamatnya di mana ya?"
	w.src.threads["21"] += "\n[3] Cici: Eh alamatnya di mana ya?"
	w.at(NOW + 60*MIN)
	w.sweep()
	if n := w.nudge(cici); n.Status != proactive.StatusSent {
		t.Fatalf("reopened and told: %+v", n)
	}
}

func TestWithoutAThreadTheChatGoesOnUnjudged(t *testing.T) {
	cici, rama := ciciKey, ramaKey
	model := models.NewScripted(
		models.Say(`{"send":[{"key":"` + cici + `"},{"key":"` + rama + `"}],"drop":[],"text":"Cici sama Rama nunggu balasan."}`),
	)
	w := triageWorld(t, model)
	w.src.threads = nil
	w.at(NOW + 30*MIN)
	w.sweep()
	if len(model.PromptsSeen()) != 1 {
		t.Fatalf("no triage call without threads: %d", len(model.PromptsSeen()))
	}
	if w.nudge(cici).Status != proactive.StatusSent || w.nudge(rama).Status != proactive.StatusSent {
		t.Fatal("both are told as before")
	}
}

func TestTheMorningSummaryLeavesOutFinishedChats(t *testing.T) {
	// Only the triage call is scripted: the summary falls back to its
	// template, which lists the facts as they are.
	model := models.NewScripted(
		models.Say(`{"items":[{"key":"` + ciciKey + `","open":false,"why":"udah selesai"},{"key":"` + ramaKey + `","open":true,"why":"nanya besok jadi ketemu jam berapa"}]}`),
	)
	w := triageWorld(t, model)
	_, err := w.d.Exec("UPDATE agent_proactive SET config = json_set(config, '$.evening.on', json('false'))")
	tu.Must(t, err)
	w.at(NOW + 13*HOUR) // 23:00 WITA: judged in quiet hours, nothing sent
	w.sweep()
	if n := w.nudge(ciciKey); n.Status != proactive.StatusDropped {
		t.Fatalf("judged at night: %+v", n)
	}
	if len(w.outbox()) != 0 {
		t.Fatal("nothing during quiet hours")
	}
	w.at(NOW + 21*HOUR) // 07:00 WITA
	w.sweep()
	txt := w.lastText()
	if strings.Contains(txt, "Cici") || !strings.Contains(txt, "Rama") || !strings.Contains(txt, "nanya besok jadi ketemu jam berapa") {
		t.Fatalf("summary: %q", txt)
	}
}

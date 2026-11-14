package rpcsetup_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// The Models tab asks for every model connector's list at the same time. The
// sort used to share one collator, which keeps iteration state, and parallel
// calls panicked ("index out of range"), so the picker showed an error.
func TestListConnectorModelsInParallel(t *testing.T) {
	ids := []any{"gpt-5.2", "Claude-Opus", "claude-haiku", "gemini-3-pro-preview", "o4-mini", "deepseek-v3.2", "llama_4", "qwen3-coder", "Ünïcode-model", "a-1", "a_2", "B3"}
	result := jsonx.NewMap[any]()
	result.Set("models", ids)
	action := jsonx.NewMap[any]()
	action.Set("result", result)
	actions := jsonx.NewMap[*jsonx.Object]()
	actions.Set("listModels", action)
	rt := fakeRuntime(t, []FakeConnector{
		{ID: "m1", Enabled: true, Actions: actions},
		{ID: "m2", Enabled: true, Actions: actions},
	})
	e, token := env(t, testutil.DB(t), t.TempDir(), rt, 0, "")

	var wg sync.WaitGroup
	errs := make(chan string, 400)
	for i := 0; i < 40; i++ {
		for _, id := range []string{"m1", "m2"} {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				for j := 0; j < 5; j++ {
					status, body := e.Get(token, "listConnectorModels", `{"id":"`+id+`"}`)
					if status != 200 {
						errs <- body
						return
					}
					var out struct{ Models []string }
					if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Models) != len(ids) {
						errs <- body
						return
					}
				}
			}(id)
		}
	}
	wg.Wait()
	close(errs)
	for body := range errs {
		t.Fatalf("a parallel call failed: %s", body)
	}
}

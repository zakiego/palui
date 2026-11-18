package rpcpeople

import (
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
)

// SetRuntimeOf swaps the runtime the handlers call; the returned func puts
// the real one back.
func SetRuntimeOf(fn func(*httpapi.Call) (Runtime, error)) (restore func()) {
	old := runtimeOf
	runtimeOf = fn
	return func() { runtimeOf = old }
}

// Clip is lib/format.ts clip, for its own test.
var Clip = clip

// TranscriptMedia is the media each transcript message shows, by message id.
func TranscriptMedia(q db.Q, chatID string) (map[string][]string, error) {
	v, err := transcript(q, chatID)
	if err != nil || v == nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, m := range v.Messages {
		for _, s := range m.Media {
			out[m.ID] = append(out[m.ID], s.Kind+" "+s.Filename+" "+s.Caption+" "+s.Status)
		}
	}
	return out, nil
}

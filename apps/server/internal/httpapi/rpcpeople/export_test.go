package rpcpeople

import "github.com/zakiego/palui/apps/server/internal/httpapi"

// SetRuntimeOf swaps the runtime the handlers call; the returned func puts
// the real one back.
func SetRuntimeOf(fn func(*httpapi.Call) (Runtime, error)) (restore func()) {
	old := runtimeOf
	runtimeOf = fn
	return func() { runtimeOf = old }
}

// Clip is lib/format.ts clip, for its own test.
var Clip = clip

package ack_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/ack"
)

// Ports packages/core/src/ack.test.ts.

func TestTakesOneEmojiIncludingSkinTonesZWJAndFlags(t *testing.T) {
	cases := map[string]string{"👀": "👀", " ✍️ ": "✍️", "👍🏽": "👍🏽", "👨‍👩‍👧‍👦": "👨‍👩‍👧‍👦", "🇮🇩": "🇮🇩"}
	for in, want := range cases {
		if got, ok := ack.ParseEmoji(in); !ok || got != want {
			t.Errorf("ParseEmoji(%q) = %q, %v", in, got, ok)
		}
	}
	for _, e := range ack.EmojiChoices {
		if _, ok := ack.ParseEmoji(e); !ok {
			t.Errorf("choice %q refused", e)
		}
	}
}

func TestRejectsTextSeveralEmojiAndNothing(t *testing.T) {
	for _, in := range []string{"", "ok", "a", "👀👀", "👀 ok"} {
		if got, ok := ack.ParseEmoji(in); ok {
			t.Errorf("ParseEmoji(%q) = %q", in, got)
		}
	}
}

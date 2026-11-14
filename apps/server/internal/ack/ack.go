// Package ack ports packages/core/src/ack.ts: the reaction a channel puts on
// the message being answered while the agent thinks.
package ack

import (
	"github.com/rivo/uniseg"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

const DefaultEmoji = "⏳"

// EmojiChoices is what the dashboard offers. /ack takes any single emoji.
var EmojiChoices = []string{"⏳", "👀", "🤔", "✍️", "🧠", "🙏", "👍", "🔥"}

func pictographic(r rune) bool {
	lo, hi := 0, len(extendedPictographic)
	for lo < hi {
		mid := (lo + hi) / 2
		rg := extendedPictographic[mid]
		switch {
		case r < rg[0]:
			hi = mid
		case r > rg[1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

func regionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// ParseEmoji returns one emoji (a single grapheme, so skin tones and ZWJ
// sequences count), or "" and false.
func ParseEmoji(input string) (string, bool) {
	s := strx.Trim(input)
	if s == "" || strx.Len16(s) > 32 {
		return "", false
	}
	if uniseg.GraphemeClusterCount(s) != 1 {
		return "", false
	}
	for _, r := range s {
		if pictographic(r) || regionalIndicator(r) {
			return s, true
		}
	}
	return "", false
}

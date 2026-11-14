// Package channelutil ports connectors/src/channel-utils: what the channel
// connectors share. Markdown conversion for Telegram (HTML) and WhatsApp,
// message splitting, action reply hints, QR codes as SVG, and timers.
package channelutil

import (
	"errors"
	"math"
	"unicode/utf16"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Split a long message into chunks that fit a channel's size limit (Telegram
// 4096). Break preference: paragraph, line, sentence, word, then a hard cut. A
// cut that would land inside a fenced code block moves to just before the
// fence when that leaves a reasonable chunk; otherwise the fence is closed at
// the end of the chunk and reopened at the start of the next one, so every
// chunk renders on its own.
//
// Lengths and cuts count UTF-16 code units, like the TS code. A hard cut that
// splits a surrogate pair leaves a lone half, which Go turns into U+FFFD (JS
// keeps the lone surrogate).

// Only the backticks and the language word are carried over to the next chunk.
const maxOpener = 24

// ErrLimitTooSmall is splitMessage's "limit too small" throw.
var ErrLimitTooSmall = errors.New("splitMessage: limit too small")

type u16 = []uint16

func enc(s string) u16   { return utf16.Encode([]rune(s)) }
func dec(u u16) string   { return string(utf16.Decode(u)) }
func isSp(c uint16) bool { return strx.IsSpace(rune(c)) }

func isWordU(c uint16) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// splitLines is s.split("\n").
func splitLines(s u16) []u16 {
	var out []u16
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// isFence is /^\s*```/.
func isFence(line u16) bool {
	i := 0
	for i < len(line) && isSp(line[i]) {
		i++
	}
	return len(line)-i >= 3 && line[i] == '`' && line[i+1] == '`' && line[i+2] == '`'
}

func isLangChar(c uint16) bool { return isWordU(c) || c == '+' || c == '#' || c == '.' || c == '-' }

// openerOf is OPENER = /^\s*(```[\w+#.-]*)/, capped at MAX_OPENER.
func openerOf(line u16) u16 {
	i := 0
	for i < len(line) && isSp(line[i]) {
		i++
	}
	fence := enc("```")
	if !(len(line)-i >= 3 && line[i] == '`' && line[i+1] == '`' && line[i+2] == '`') {
		return fence
	}
	j := i + 3
	for j < len(line) && isLangChar(line[j]) {
		j++
	}
	lang := line[i:j]
	if len(lang) <= maxOpener {
		return lang
	}
	return fence
}

type fenceState struct {
	open   bool
	opener u16
}

func fenceStateAt(text u16) fenceState {
	st := fenceState{}
	for _, line := range splitLines(text) {
		if isFence(line) {
			st.open = !st.open
			if st.open {
				st.opener = openerOf(line)
			} else {
				st.opener = nil
			}
		}
	}
	return st
}

func lastIndex(s, sub u16) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		ok := true
		for j := range sub {
			if s[i+j] != sub[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// naturalBreak is the index just after the best natural break inside window
// (0 when none found).
func naturalBreak(window u16, min int) int {
	if para := lastIndex(window, u16{'\n', '\n'}); para >= min {
		return para + 2
	}
	if line := lastIndex(window, u16{'\n'}); line >= min {
		return line + 1
	}
	// /[.!?][)"'\]]?\s/g, the end of the last match.
	sentence := -1
	for i := 0; i < len(window); {
		c := window[i]
		if c == '.' || c == '!' || c == '?' {
			if i+2 < len(window) && (window[i+1] == ')' || window[i+1] == '"' || window[i+1] == '\'' || window[i+1] == ']') && isSp(window[i+2]) {
				sentence = i + 3
				i += 3
				continue
			}
			if i+1 < len(window) && isSp(window[i+1]) {
				sentence = i + 2
				i += 2
				continue
			}
		}
		i++
	}
	if sentence >= min {
		return sentence
	}
	if space := lastIndex(window, u16{' '}); space >= min {
		return space + 1
	}
	return 0
}

func trimEndSp(s u16) u16 {
	for len(s) > 0 && isSp(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

func trimStartSp(s u16) u16 {
	for len(s) > 0 && isSp(s[0]) {
		s = s[1:]
	}
	return s
}

func cat(parts ...u16) u16 {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make(u16, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// SplitMessage is splitMessage(text, limit). It panics with
// ErrLimitTooSmall below 16, like the TS throw.
func SplitMessage(text string, limit int) []string {
	if limit < 16 {
		panic(ErrLimitTooSmall)
	}
	var chunks []string
	rest := enc(text)
	// When a fence was split, the next chunk starts by reopening it.
	var reopen u16
	for len(rest) > 0 {
		input := rest
		if len(reopen) > 0 {
			input = cat(reopen, u16{'\n'}, rest)
		}
		reopen = nil
		if len(input) <= limit {
			chunks = append(chunks, dec(input))
			break
		}
		// Leave room to close a fence ("\n```") if the cut lands inside one.
		budget := limit - 4
		window := input[:budget]
		min := int(math.Floor(float64(budget) * 0.3))
		cut := naturalBreak(window, min)
		if cut == 0 {
			cut = budget
		}

		state := fenceStateAt(input[:cut])
		if state.open {
			// Try cutting right before the fence opened.
			pos, openAt, inFence := 0, -1, false
			for _, line := range splitLines(input[:cut]) {
				if isFence(line) {
					inFence = !inFence
					if inFence {
						openAt = pos
					}
				}
				pos += len(line) + 1
			}
			if openAt >= min {
				cut = openAt
				state = fenceState{}
			} else {
				// The block itself is too big: cut on a line inside it and carry the fence over.
				nl := lastIndex(input[:cut], u16{'\n'})
				if nl > openAt+len(state.opener) {
					cut = nl + 1
				}
			}
		}

		chunk := trimEndSp(input[:cut])
		next := input[cut:]
		if state.open {
			chunk = cat(chunk, enc("\n```"))
			reopen = state.opener
			for len(next) > 0 && next[0] == '\n' {
				next = next[1:]
			}
		} else {
			next = trimStartSp(next)
		}
		// Safety net: every pass must consume part of the original text. If it
		// didn't, hard cut the original text and drop the fence carry-over.
		if len(next) >= len(rest) {
			end := min2(limit, len(rest))
			chunk = rest[:end]
			next = rest[end:]
			reopen = nil
		}
		if len(trimStartSp(trimEndSp(chunk))) > 0 {
			chunks = append(chunks, dec(chunk))
		}
		rest = next
		if len(rest) == 0 && len(reopen) > 0 {
			reopen = nil
		}
	}
	return chunks
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

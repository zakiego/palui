package strx

import (
	"strings"
	"unicode"
	"unicode/utf16"
)

// Lower is String.prototype.toLowerCase for the cases Go's rules differ on: a
// dotted capital I and a final sigma. (flows and tools keep their own copies.)
func Lower(s string) string {
	if !strings.ContainsAny(s, "İΣ") {
		return strings.ToLower(s)
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		switch r {
		case 'İ':
			b.WriteString("i̇")
		case 'Σ':
			// Final when a letter comes before and none after.
			before := i > 0 && unicode.IsLetter(runes[i-1])
			after := i+1 < len(runes) && unicode.IsLetter(runes[i+1])
			if before && !after {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteString(strings.ToLower(string(r)))
		}
	}
	return b.String()
}

// Slice16Range is s.slice(start, end) in UTF-16 code units (negative or
// out-of-range bounds clamp like JS). A cut through a surrogate pair leaves
// U+FFFD for the half.
func Slice16Range(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	if start < 0 {
		start = max(len(u)+start, 0)
	}
	if end < 0 {
		end = max(len(u)+end, 0)
	}
	start, end = min(start, len(u)), min(end, len(u))
	if start >= end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}

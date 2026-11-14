// Package outbound ports packages/core/src/outbound: messages to contacts on
// the owner's behalf (docs/design/outbound-messages.md). Every guard that must
// hold whatever the model does lives here: the kill switch, the allowlist
// (contacts), and the rate limits. Callers add the origin checks.
package outbound

// Phone numbers for WhatsApp contacts (phone.ts). Indonesian local forms
// become 62...; other countries need the leading "+" (or "00").

import (
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

var (
	phoneLike = regexp.MustCompile(`^\+?[0-9` + strx.SpaceClass + `().-]{8,24}$`)
	nonDigit  = regexp.MustCompile(`[^0-9]`)
	phoneIn   = regexp.MustCompile(`\+?[0-9][0-9` + strx.SpaceClass + `().-]{7,22}[0-9]`)
)

// LooksLikePhone: something that looks like a phone number (not a name).
func LooksLikePhone(raw string) bool { return phoneLike.MatchString(strx.Trim(raw)) }

// NormalizePhone: "0822 0000-1001", "+62 822 0000 1001", "6282200001001",
// "82200001001" -> "6282200001001". "" when it can't be a number.
func NormalizePhone(raw string) string {
	s := strx.Trim(raw)
	if !LooksLikePhone(s) {
		return ""
	}
	digits := nonDigit.ReplaceAllString(s, "")
	switch {
	case strings.HasPrefix(s, "+"):
		// Already international.
	case strings.HasPrefix(digits, "00"):
		digits = digits[2:]
	case strings.HasPrefix(digits, "0"):
		digits = "62" + digits[1:]
	case strings.HasPrefix(digits, "8"):
		digits = "62" + digits
	case !strings.HasPrefix(digits, "62"):
		return ""
	}
	if strings.HasPrefix(digits, "620") {
		digits = "62" + digits[3:]
	}
	if len(digits) < 10 || len(digits) > 15 {
		return ""
	}
	return digits
}

func WaJID(digits string) string { return digits + "@s.whatsapp.net" }

// FormatPhone: "+62 822-0000-1001" for 62 numbers, "+<digits>" otherwise.
func FormatPhone(digits string) string {
	if strings.HasPrefix(digits, "62") && len(digits) >= 11 {
		rest := digits[2:]
		return "+62 " + rest[:3] + "-" + rest[3:7] + "-" + rest[7:]
	}
	return "+" + digits
}

// PhonesIn is every phone number written in a text, normalized.
func PhonesIn(text string) []string {
	var out []string
	for _, m := range phoneIn.FindAllString(text, -1) {
		if n := NormalizePhone(m); n != "" {
			out = append(out, n)
		}
	}
	return out
}

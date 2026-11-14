package strx_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

// The helpers M7 added for the channels (the rest of strx is tested in
// jsonx_test.go's TestStrx).
func TestChannelHelpers(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{strx.Lower("@Palui_Test_BOT"), "@palui_test_bot"},
		{strx.Lower("İSTANBUL"), "i̇stanbul"},
		{strx.Lower("ΟΔΟΣ"), "οδος"},
		{strx.Slice16Range("😀😀 @bot", 5, 9), "@bot"},
		{strx.Slice16Range("abc", -2, 10), "bc"},
		{strx.Slice16Range("abc", 2, 1), ""},
		{strx.Slice16Range("😀x", 1, 3), "�x"},
		{strx.ToFixed(1310720.0/1048576, 1), "1.3"},
		{strx.ToFixed(5, 1), "5.0"},
		{strx.ToFixed(1.05, 1), "1.1"},
		{strx.ToFixed(0.04, 1), "0.0"},
		{strx.ToFixed(-1.25, 1), "-1.3"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

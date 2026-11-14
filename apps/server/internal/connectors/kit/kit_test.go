package kit_test

import (
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

func TestDateParseReadsTheISOFormsV8Reads(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"2026-11-10T02:00:00Z", 1794276000000, true},
		{"2026-11-10T09:00:00.5+0700", 1794276000500, true},
		{"2026-11-10T09:00:00+07:00", 1794276000000, true},
		{"2026-02-30", 1772409600000, true},
		{"2026-04-31T10:00Z", 1777629600000, true},
		{"2026-02-32", 0, false},
		{"2026-13-01", 0, false},
		{"2026-11-10T09:00+07", 0, false},
		{"Nov 10 2026", 0, false},
	}
	for _, c := range cases {
		got, ok := kit.DateParse(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: %d %v", c.in, got, ok)
		}
	}
	local, ok := kit.DateParse("2026-11-10T09:00")
	if want := time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local).UnixMilli(); !ok || local != want {
		t.Errorf("local: %d", local)
	}
}

func TestISOIsToISOString(t *testing.T) {
	if got := kit.ISO(1794276000500); got != "2026-11-10T02:00:00.500Z" {
		t.Fatal(got)
	}
}

func TestODropsUndefinedLikeJSONStringify(t *testing.T) {
	if got := jsonx.Stringify(kit.O("a", 1, "b", kit.Undef, "c", nil, "d", kit.If(false, 2), "e", kit.If(true, 3))); got != `{"a":1,"c":null,"e":3}` {
		t.Fatal(got)
	}
}

func TestDecodeReadsAnyInputAsAnObject(t *testing.T) {
	for raw, want := range map[string]string{"": "{}", "  ": "{}", "[1]": "{}", `{"a":1}`: `{"a":1}`} {
		in, err := kit.Decode([]byte(raw))
		if err != nil || jsonx.Stringify(in.O) != want {
			t.Fatal(raw, err)
		}
	}
}

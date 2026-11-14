package logx_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/log.test.ts, plus the redact case in connectors/store.test.ts.

const bot = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ"

func TestRedactCoversKeysAndBotTokensInURLs(t *testing.T) {
	cases := map[string]string{
		"key rcl_ab-cd_ef1234 end":                          "key rcl_•••• end",
		"GET https://api.telegram.org/bot" + bot + "/getMe": "GET https://api.telegram.org/bot1234••••/getMe",
		"token " + bot:                        "token 1234••••",
		"sk-abcdefgh1234 and zkg_abc-123_xyz": "sk-a•••• and zkg_••••",
	}
	for in, want := range cases {
		if got := logx.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	if got := logx.Redact("run 12:30 at 2026-11-10"); got != "run 12:30 at 2026-11-10" {
		t.Fatal(got)
	}
	// 13 digits before the colon: the lookbehind never lets a match start.
	long := "1234567890123:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ"
	if got := logx.Redact(long); got != long {
		t.Fatal(got)
	}
}

func TestRedactDeepWalksNestedEventData(t *testing.T) {
	v, _ := jsonx.Parse([]byte(`{"a":["x rcl_secret-key_1"],"b":{"c":1,"d":null}}`))
	got := jsonx.Stringify(logx.RedactDeep(v))
	if got != `{"a":["x rcl_••••"],"b":{"c":1,"d":null}}` {
		t.Fatal(got)
	}
}

func TestHidesKnownSecretShapes(t *testing.T) {
	s := logx.Redact("key rcl_abcdefgh123 and zkg_abcdefgh and sk-abcdefghijk and 123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw0 Bearer abcdefghijklmnop")
	if regexp.MustCompile(`abcdefgh123|AAHdqTcv|abcdefghijklmnop`).MatchString(s) {
		t.Fatal(s)
	}
}

func TestLogEventRedactsMessageAndData(t *testing.T) {
	d := testutil.DB(t)
	data := jsonx.NewMap[any]()
	data.Set("url", "/bot"+bot+"/x")
	_, err := events.Log(d, events.Input{Kind: "x", Message: "failed with rcl_abc-def_123", Data: data}, 1)
	testutil.Must(t, err)
	list, err := events.List(d, events.ListOptions{})
	testutil.Must(t, err)
	if list[0].Message != "failed with rcl_••••" {
		t.Fatal(list[0].Message)
	}
	if strings.Contains(jsonx.Stringify(list[0].Data), bot) {
		t.Fatal("bot token stored")
	}
}

func TestSetConnectorStatusRedactsTheDetail(t *testing.T) {
	d := testutil.DB(t)
	testutil.SeedConnector(t, d, "telegram", "channel", testutil.SeedOptions{}, 1)
	detail := "409 at https://api.telegram.org/bot" + bot + "/getUpdates"
	testutil.Must(t, connectors.SetStatus(d, "telegram", "error", &detail, 2))
	row, _, _ := connectors.Get(d, "telegram")
	if strings.Contains(*row.StatusDetail, bot) {
		t.Fatal("detail stored the token")
	}
	list, _ := events.List(d, events.ListOptions{})
	if strings.Contains(list[0].Message, bot) {
		t.Fatal("event stored the token")
	}
}

func TestLoggerWritesRedactedJSONLines(t *testing.T) {
	var lines []string
	l := logx.New(logx.Fields{"connector_id": "telegram"}, func(s string) { lines = append(lines, s) }, logx.Info)
	l.Debug("hidden")
	l.Child(logx.Fields{"run_id": "run_1"}).Error("boom rcl_abcdefgh123", logx.Fields{"err": errors.New("x")})
	if len(lines) != 1 {
		t.Fatal(lines)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["level"] != "error" || rec["msg"] != "boom rcl_••••" || rec["connector_id"] != "telegram" || rec["run_id"] != "run_1" {
		t.Fatal(lines[0])
	}
	if !strings.HasPrefix(lines[0], `{"t":"`) {
		t.Fatal(lines[0])
	}
	if e := rec["err"].(map[string]any); e["message"] != "x" || e["name"] != "Error" {
		t.Fatal(lines[0])
	}
	logx.Silent.Error("nothing")
}

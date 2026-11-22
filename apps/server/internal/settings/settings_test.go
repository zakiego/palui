package settings_test

import (
	"errors"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/settings.test.ts and the settings case in
// connectors/store.test.ts.

var NOW = testutil.NOW

func TestSetClearsANullableKeyBackToItsDefault(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "turnsPausedUntil", NOW+1000, NOW))
	v, err := settings.Get[*int64](d, "turnsPausedUntil")
	if err != nil || v == nil || *v != NOW+1000 {
		t.Fatal(v, err)
	}
	testutil.Must(t, settings.Set(d, "turnsPausedUntil", nil, NOW))
	if v, _ := settings.Get[*int64](d, "turnsPausedUntil"); v != nil {
		t.Fatal(*v)
	}
	testutil.Must(t, settings.Set(d, "turnsPausedReason", nil, NOW))
	if v, _ := settings.Get[*string](d, "turnsPausedReason"); v != nil {
		t.Fatal(*v)
	}
}

func TestReturnsDefaultsAndMergesNewDefaultFields(t *testing.T) {
	d := testutil.DB(t)
	if tz, _ := settings.Timezone(d); tz != "Asia/Makassar" {
		t.Fatal(tz)
	}
	b := settings.Defaults().Budgets
	b.MaxSteps = 5
	testutil.Must(t, settings.Set(d, "budgets", b, NOW))
	got, _ := settings.GetBudgets(d)
	if got.MaxSteps != 5 || got.MaxRunsPerHour != 60 {
		t.Fatalf("%+v", got)
	}
	// A row written before a field existed gets the default for it.
	_, err := d.Exec(`UPDATE settings SET value = '{"maxSteps":7}' WHERE key = 'budgets'`)
	testutil.Must(t, err)
	got, _ = settings.GetBudgets(d)
	if got.MaxSteps != 7 || got.TurnTimeoutMs != 300_000 {
		t.Fatalf("%+v", got)
	}
}

func TestStoredJSONMatchesTheTSShape(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "budgets", settings.Defaults().Budgets, NOW))
	var raw string
	testutil.Must(t, d.QueryRow("SELECT value FROM settings WHERE key = 'budgets'").Scan(&raw))
	if raw != `{"maxSteps":12,"turnTimeoutMs":300000,"maxTools":64,"dailyTokenCap":2000000,"maxRunsPerHour":60}` {
		t.Fatal(raw)
	}
}

func TestOutboundUpgradesOldRows(t *testing.T) {
	d := testutil.DB(t)
	_, err := d.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('outbound', '{"enabled":false,"signature":""}', 1)`)
	testutil.Must(t, err)
	o, _ := settings.GetOutbound(d)
	if o.Enabled || !o.SignatureOn || o.Signature != settings.DefaultOutboundSignature || o.PerDay != 20 {
		t.Fatalf("%+v", o)
	}
	_, err = d.Exec(`UPDATE settings SET value = '{"signature":"  custom  "}' WHERE key = 'outbound'`)
	testutil.Must(t, err)
	if o, _ := settings.GetOutbound(d); o.Signature != "custom" || !o.SignatureOn {
		t.Fatalf("%+v", o)
	}
	_, err = d.Exec(`UPDATE settings SET value = '{"signatureOn":false,"signature":""}' WHERE key = 'outbound'`)
	testutil.Must(t, err)
	if o, _ := settings.GetOutbound(d); o.Signature != "" || o.SignatureOn {
		t.Fatalf("%+v", o)
	}
}

func TestAllAndUnknownKeys(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", "UTC", NOW))
	all, err := settings.All(d)
	if err != nil || all.Timezone != "UTC" || all.Budgets.MaxSteps != 12 || len(all.ModelRoles.Default) != 1 {
		t.Fatalf("%+v %v", all, err)
	}
	if _, err := settings.Get[string](d, "nope"); !errors.Is(err, settings.ErrUnknownKey) {
		t.Fatal(err)
	}
}

func TestNewInstallsStartInUTC(t *testing.T) {
	// testutil pins DefaultTimezone for the goldens; what ships is UTC.
	if settings.ShippedTimezone != "UTC" {
		t.Fatal(settings.ShippedTimezone)
	}
}

func TestParsePatch(t *testing.T) {
	p, err := settings.ParsePatch([]byte(`{"timezone":"Asia/Jakarta","modelRoles":{"default":[{"connectorId":"cliproxy","model":"  m  ","effort":"high"}],"fast":[{"connectorId":"c","model":"f"}],"vision":[{"connectorId":"c","model":"v"}]},"budgets":{"maxSteps":5,"turnTimeoutMs":10000,"maxTools":1,"dailyTokenCap":0,"maxRunsPerHour":1000},"extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if *p.Timezone != "Asia/Jakarta" || p.ModelRoles.Default[0].Model != "m" || p.Budgets.DailyTokenCap != 0 {
		t.Fatalf("%+v", p)
	}
	bad := []string{
		`{"timezone":"Mars/Base"}`,
		`{"language":"fr"}`,
		`{"fallbackChannel":"sms"}`,
		`{"budgets":{"maxSteps":5.5,"turnTimeoutMs":10000,"maxTools":1,"dailyTokenCap":0,"maxRunsPerHour":1}}`,
		`{"budgets":{"maxSteps":5,"turnTimeoutMs":10000,"maxTools":1,"dailyTokenCap":5000,"maxRunsPerHour":1}}`,
		`{"modelRoles":{"default":[],"fast":[{"connectorId":"c","model":"f"}],"vision":[{"connectorId":"c","model":"v"}]}}`,
		`{"modelRoles":{"default":[{"connectorId":"c","model":"f","effort":"turbo"}],"fast":[{"connectorId":"c","model":"f"}],"vision":[{"connectorId":"c","model":"v"}]}}`,
	}
	for _, b := range bad {
		if _, err := settings.ParsePatch([]byte(b)); err == nil {
			t.Errorf("accepted %s", b)
		}
	}
	o, err := settings.ValidateOutbound("outbound", settings.OutboundInput{PerContactPerDay: 1, PerDay: 1, Signature: "\r\n hi \r\n"})
	if err != nil || o.Signature != "hi" {
		t.Fatal(o, err)
	}
}

package settings

// The zod schemas of settings-schema.ts, for callers that need zod's exact
// issues (the settings tool's "Nilainya nggak valid" reply, M8's endpoints).
// ParsePatch in schema.go stays the Go validator M1 wrote.

import "github.com/zakiego/palui/apps/server/internal/zod"

// TimezoneZod is timezoneSchema.
func TimezoneZod() *zod.StringS {
	return zod.String().Min(1).Max(64).Refine(IsTimeZone, "Use an IANA zone like Asia/Makassar.")
}

// ModelChainZod is modelChainSchema.
func ModelChainZod() *zod.ArrayS {
	return zod.Array(ModelLinkZod()).Min(1).Max(6)
}

// ModelLinkZod is one step of a model chain.
func ModelLinkZod() *zod.ObjectS {
	return zod.Object(
		zod.F("connectorId", zod.String().Min(1).Max(64)),
		zod.F("model", zod.String().Trim().Min(1).Max(120)),
		zod.F("effort", zod.Enum(ReasoningEfforts...).Optional()),
	)
}

// BudgetsZod is budgetsSchema.
func BudgetsZod() *zod.ObjectS {
	return zod.Object(
		zod.F("maxSteps", zod.Int().Min(1).Max(50)),
		zod.F("turnTimeoutMs", zod.Int().Min(10_000).Max(600_000)),
		zod.F("maxTools", zod.Int().Min(1).Max(128)),
		zod.F("dailyTokenCap", zod.Union(zod.Literal(float64(0)), zod.Int().Min(10_000).Max(100_000_000))),
		zod.F("maxRunsPerHour", zod.Int().Min(1).Max(1000)),
	)
}

func LanguageZod() *zod.EnumS        { return zod.Enum("auto", "id", "en") }
func UILanguageZod() *zod.EnumS      { return zod.Enum("en", "id") }
func FallbackChannelZod() *zod.EnumS { return zod.Enum("telegram", "whatsapp", "none") }

// PatchZod is settingsPatchSchema.
func PatchZod() *zod.ObjectS {
	return zod.Object(
		zod.F("timezone", TimezoneZod().Optional()),
		zod.F("language", LanguageZod().Optional()),
		zod.F("uiLanguage", UILanguageZod().Optional()),
		zod.F("modelRoles", zod.Object(zod.F("default", ModelChainZod()), zod.F("fast", ModelChainZod()), zod.F("vision", ModelChainZod())).Optional()),
		zod.F("budgets", BudgetsZod().Optional()),
		zod.F("fallbackChannel", FallbackChannelZod().Optional()),
	)
}

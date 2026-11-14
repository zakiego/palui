package flows

// Schedule sources and check-in answers (source-config.ts), shared by the flow
// schema, the scheduler and the check-in code.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var DayCodes = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

const MaxItems = 24

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func hhmm() *zString {
	return zstr().regex(hhmmRe, `^([01]\d|2[0-3]):[0-5]\d$`, "Use HH:mm (24h, two-digit hour)")
}

func label() *zString         { return zstr().trim().min(1).max(60) }
func fieldPath() *zString     { return zstr().trim().min(1).max(200) }
func offsetMin() *zDefault    { return withDefault(zint(-10080, 10080), func() any { return 0.0 }) }
func emptyObject() func() any { return func() any { return jsonx.NewMap[any]() } }

var rruleStartRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$`)

var timesSourceSchema = zobj(
	f("kind", zlit("times")),
	f("times", zarr(zobj(f("time", hhmm()), f("label", opt(label())))).min(1).max(MaxItems)),
)

var rruleSourceSchema = zobj(
	f("kind", zlit("rrule")),
	f("rrule", zstr().trim().min(1).max(300)),
	f("start", zstr().regex(rruleStartRe, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$`, "Use YYYY-MM-DDTHH:mm")),
	f("label", opt(label())),
)

var toolSourceSchema = zobj(
	f("kind", zlit("tool")),
	f("connectorId", zstr().trim().min(1).max(80)),
	f("tool", zstr().trim().min(1).max(120)),
	f("args", withDefault(zrecord(zstr(), &zUnknown{}), emptyObject())),
	f("refreshAt", withDefault(hhmm(), func() any { return "00:05" })),
	f("items", opt(zarr(zobj(f("field", fieldPath()), f("label", opt(label())), f("offsetMin", offsetMin()))).min(1).max(MaxItems))),
	f("list", opt(zobj(
		f("path", withDefault(zstr().trim().max(200), func() any { return "" })),
		f("at", fieldPath()),
		f("label", opt(fieldPath())),
		f("time", withDefault(hhmm(), func() any { return "09:00" })),
		f("offsetMin", offsetMin()),
	))),
)

var sourceSchema = zdisc("kind", timesSourceSchema, rruleSourceSchema, toolSourceSchema)

var answerIDRe = regexp.MustCompile(`^[a-z0-9_]{1,20}$`)

var answerSchema = zobj(
	f("id", zstr().regex(answerIDRe, `^[a-z0-9_]{1,20}$`, "Use lowercase letters, digits and _ (max 20)")),
	f("label", zstr().trim().min(1).max(30)),
	f("words", withDefault(zarr(zstr().trim().min(1).max(40)).max(12), func() any { return []any{} })),
	f("closes", withDefault(&zBool{}, func() any { return true })),
	f("reply", opt(zstr().trim().max(300))),
)

// DefaultAnswers is DEFAULT_ANSWERS.
func DefaultAnswers() []Answer {
	return []Answer{
		{ID: "done", Label: "Sudah", Words: []string{"udah", "udh", "dah", "done", "selesai", "beres"}, Closes: true},
		{ID: "not_yet", Label: "Belum", Words: []string{"blm", "not yet", "nanti"}, Closes: false},
	}
}

func defaultAnswersValue() any {
	var out []any
	for _, a := range DefaultAnswers() {
		words := make([]any, len(a.Words))
		for i, w := range a.Words {
			words[i] = w
		}
		out = append(out, obj("id", a.ID, "label", a.Label, "words", words, "closes", a.Closes))
	}
	return out
}

func daysSchema() zSchema {
	return nullable(zarr(zenum(DayCodes...)).min(1).max(7))
}

var answerPunct = regexp.MustCompile(`[!.,?]+`)

// NormalizeAnswer is normalizeAnswer.
func NormalizeAnswer(text string) string {
	t := jsLower(text)
	t = answerPunct.ReplaceAllString(t, " ")
	return strx.Trim(collapseJSSpace(t))
}

var jsSpaceRun = regexp.MustCompile(`[` + strx.SpaceClass + `]+`)

func collapseJSSpace(s string) string { return jsSpaceRun.ReplaceAllString(s, " ") }

// MatchAnswer is matchAnswer: the option whose id, label or word the reply is
// exactly (case and trailing punctuation ignored).
func MatchAnswer(options []Answer, text string) *Answer {
	t := NormalizeAnswer(text)
	if t == "" {
		return nil
	}
	for i := range options {
		o := &options[i]
		words := append([]string{o.ID, strings.ReplaceAll(o.ID, "_", " "), o.Label}, o.Words...)
		for _, w := range words {
			if NormalizeAnswer(w) == t {
				return o
			}
		}
	}
	return nil
}

// DescribeSource is describeSource: one line in the bot's language.
func DescribeSource(src Source, days []string, hasDays bool) string {
	d := ""
	if hasDays {
		d = " (" + strings.Join(days, ",") + ")"
	}
	switch src.Kind {
	case "times":
		parts := make([]string, len(src.Times))
		for i, t := range src.Times {
			l := ""
			if t.Label != nil {
				l = *t.Label + " "
			}
			parts[i] = l + strings.Replace(t.Time, ":", ".", 1)
		}
		return fmt.Sprintf("Tiap hari%s: %s", d, strings.Join(parts, ", "))
	case "rrule":
		return fmt.Sprintf("RRULE %s mulai %s%s", src.RRule, src.Start, d)
	case "tool":
		var what string
		if src.Items != nil {
			parts := make([]string, len(src.Items))
			for i, it := range src.Items {
				name := it.Field
				if it.Label != nil {
					name = *it.Label
				}
				if it.OffsetMin != 0 {
					sign := ""
					if it.OffsetMin > 0 {
						sign = "+"
					}
					name += fmt.Sprintf(" %s%dm", sign, it.OffsetMin)
				}
				parts[i] = name
			}
			what = strings.Join(parts, ", ")
		} else {
			p := ""
			if src.List != nil {
				p = src.List.Path
			}
			if p == "" {
				p = "(result)"
			}
			what = "list " + p
		}
		return fmt.Sprintf("Dari %s.%s (refresh %s)%s: %s", src.ConnectorID, src.Tool, src.RefreshAt, d, what)
	}
	return ""
}

package rpcsched

import (
	"net/http"
	"regexp"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

// apps/app/src/lib/fns/jobs.ts and the writes of server/jobs-bridge.ts: the
// Jobs page. Every write pokes the runtime so the scheduler re-arms at once.

const jobGoneText = "That job doesn't exist anymore."

// ianaZone is jobs.ts ianaZone (reminders.ts uses the same refine).
func ianaZone() *zod.StringS {
	return zod.String().Min(1).Max(64).Refine(luxonZoneValid, "Use an IANA zone like Asia/Makassar.")
}

var (
	freqRe       = regexp.MustCompile(`^FREQ=`)
	localStartRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$`)
)

// jobInputZod is jobs.ts jobInput. dailyTokenCap is an integer here, so the
// service never sees a fractional cap.
func jobInputZod() *zod.ObjectS {
	return zod.Object(
		zod.F("id", zod.String().Max(100).Optional()),
		zod.F("name", zod.String().Trim().Min(1).Max(80)),
		zod.F("kind", zod.Enum("job", "heartbeat", "briefing").Optional()),
		zod.F("rrule", zod.String().Trim().Min(5).Max(300).Regex(freqRe, "^FREQ=", "Start the schedule with FREQ=, e.g. FREQ=DAILY;BYHOUR=7;BYMINUTE=0")),
		zod.F("tz", ianaZone()),
		zod.F("localStart", zod.String().Regex(localStartRe, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$`)),
		zod.F("prompt", zod.String().Trim().Min(1).Max(4000)),
		zod.F("chatId", zod.String().Min(1).Max(100)),
		zod.F("modelRole", zod.Enum("default", "fast", "vision")),
		zod.F("dailyTokenCap", zod.Int().Min(1000).Max(5_000_000)),
	)
}

type jobInput struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Kind          *string `json:"kind"`
	RRule         string  `json:"rrule"`
	TZ            string  `json:"tz"`
	LocalStart    string  `json:"localStart"`
	Prompt        string  `json:"prompt"`
	ChatID        string  `json:"chatId"`
	ModelRole     string  `json:"modelRole"`
	DailyTokenCap int64   `json:"dailyTokenCap"`
}

func init() {
	httpapi.Register(httpapi.Handler{Name: "getJobs", Method: http.MethodGet, Guard: httpapi.User, Fn: getJobs})
	httpapi.Register(httpapi.Handler{
		Name: "runJobNow", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: idInput(),
		Fn: func(c *httpapi.Call) (any, error) {
			rt, err := c.Runtime()
			if err != nil {
				return nil, err
			}
			out, err := rt.RunJob(inputID(c))
			if err != nil {
				return nil, err
			}
			if res, ok := out.(jobs.NowResult); ok && !res.OK {
				if res.Detail == "" {
					return nil, httpapi.Fail("The runtime didn't start the job.")
				}
				return nil, httpapi.Fail("%s", res.Detail)
			}
			return out, nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "setJobEnabledFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("enabled", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			q := c.DB()
			if _, ok, err := jobs.Get(q, in.ID); err != nil {
				return nil, err
			} else if !ok {
				return nil, httpapi.Fail(jobGoneText)
			}
			if _, err := jobs.SetEnabled(q, in.ID, in.Enabled, c.Now()); err != nil {
				return nil, friendly(err)
			}
			poke(c)
			return obj("ok", true), nil
		},
	})
	httpapi.Register(httpapi.Handler{Name: "saveJob", Method: http.MethodPost, Guard: httpapi.Writer, Input: jobInputZod(), Fn: saveJob})
	httpapi.Register(httpapi.Handler{
		Name: "deleteJobFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: idInput(),
		Fn: func(c *httpapi.Call) (any, error) {
			ok, err := jobs.Delete(c.DB(), inputID(c))
			if err != nil {
				return nil, err
			}
			poke(c)
			if !ok {
				return nil, httpapi.Fail(jobGoneText)
			}
			return obj("ok", true), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "getJobPreset", Method: http.MethodGet, Guard: httpapi.User,
		Input: zod.Object(zod.F("preset", zod.Enum("dailyBriefing", "heartbeat")), zod.F("chatId", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				Preset string `json:"preset"`
				ChatID string `json:"chatId"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			tz, err := settings.Timezone(c.DB())
			if err != nil {
				return nil, err
			}
			if in.Preset == "heartbeat" {
				return jobs.Heartbeat(in.ChatID, tz, c.Now()), nil
			}
			return jobs.DailyBriefing(in.ChatID, tz, c.Now()), nil
		},
	})
}

func saveJob(c *httpapi.Call) (any, error) {
	var in jobInput
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	q, now := c.DB(), c.Now()
	cap := in.DailyTokenCap
	if in.ID != "" {
		if _, ok, err := jobs.Get(q, in.ID); err != nil {
			return nil, err
		} else if !ok {
			return nil, httpapi.Fail(jobGoneText)
		}
		row, err := jobs.Update(q, in.ID, jobs.Patch{Name: &in.Name, Kind: in.Kind, RRule: &in.RRule, TZ: &in.TZ, LocalStart: &in.LocalStart, Prompt: &in.Prompt, ChatID: &in.ChatID, ModelRole: &in.ModelRole, DailyTokenCap: &cap}, now)
		if err != nil {
			return nil, friendly(err)
		}
		poke(c)
		return obj("id", row.ID), nil
	}
	kind := ""
	if in.Kind != nil {
		kind = *in.Kind
	}
	row, err := jobs.Create(q, jobs.CreateInput{Name: in.Name, Kind: kind, RRule: in.RRule, TZ: in.TZ, LocalStart: in.LocalStart, Prompt: in.Prompt, ChatID: in.ChatID, ModelRole: in.ModelRole, DailyTokenCap: &cap}, now)
	if err != nil {
		return nil, friendly(err)
	}
	poke(c)
	return obj("id", row.ID), nil
}

// chatOption is queries.ts chatOptions' row.
type chatOption struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Title   string `json:"title"`
}

// chatOptions is queries.ts chatOptions: the allowed chats by channel, then title.
func chatOptions(q db.Q) ([]chatOption, error) {
	rows, err := q.Query("SELECT id, channel, title, external_id FROM chats WHERE allowed = 1 ORDER BY channel ASC, title ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chatOption{}
	for rows.Next() {
		var o chatOption
		var title *string
		var ext string
		if err := rows.Scan(&o.ID, &o.Channel, &title, &ext); err != nil {
			return nil, err
		}
		o.Title = titleOr(title, ext)
		out = append(out, o)
	}
	return out, rows.Err()
}

// titleOr is `title || externalId`.
func titleOr(title *string, ext string) string {
	if title != nil && *title != "" {
		return *title
	}
	return ext
}

// chatTitles is queries.ts chatTitles: every chat's display name.
func chatTitles(q db.Q) (map[string]string, error) {
	rows, err := q.Query("SELECT id, title, external_id FROM chats")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, ext string
		var title *string
		if err := rows.Scan(&id, &title, &ext); err != nil {
			return nil, err
		}
		out[id] = titleOr(title, ext)
	}
	return out, rows.Err()
}

// jobRow is jobList's row: the job row plus its chat title and today's tokens.
type jobRow struct {
	db.Job
	ChatTitle   *string `json:"chatTitle"`
	TokensToday int64   `json:"tokensToday"`
}

// jobList is queries.ts jobList.
func jobList(q db.Q, tz string, now int64) ([]jobRow, error) {
	titles, err := chatTitles(q)
	if err != nil {
		return nil, err
	}
	today := zone.StartOfDay(now, zone.MustLoad(tz))
	rows, err := q.Query("SELECT " + db.JobCols + " FROM jobs ORDER BY created_at ASC")
	list, err := db.All(rows, err, db.ScanJob)
	if err != nil {
		return nil, err
	}
	out := []jobRow{}
	for _, j := range list {
		r := jobRow{Job: j}
		if t, ok := titles[j.ChatID]; ok {
			r.ChatTitle = &t
		}
		if err := q.QueryRow("SELECT coalesce(sum(tokens_in + tokens_out), 0) FROM runs WHERE job_id = ? AND started_at >= ?", j.ID, today).Scan(&r.TokensToday); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func getJobs(c *httpapi.Call) (any, error) {
	q := c.DB()
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	list, err := jobList(q, tz, c.Now())
	if err != nil {
		return nil, err
	}
	chats, err := chatOptions(q)
	if err != nil {
		return nil, err
	}
	return obj("jobs", list, "chats", chats, "timezone", tz), nil
}

package rpcsetup

// apps/app/src/lib/fns/settings.ts: the Settings page.

import (
	"net/http"
	"slices"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/backup"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// settingValue is getSetting(db, key) as the stored JSON: the default when
// there's no row, and an object row spread over the default object
// ({...default, ...stored}) so a new default field shows up on old rows.
func settingValue(q db.Q, key string) (any, error) {
	b, err := jsonx.Marshal(settings.Defaults())
	if err != nil {
		return nil, err
	}
	defs, err := jsonx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	def, _ := defs.Get(key)
	var raw string
	switch err := q.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&raw); {
	case err == nil:
	case isNoRows(err):
		return def, nil
	default:
		return nil, err
	}
	stored, err := jsonx.Parse([]byte(raw))
	if err != nil {
		return nil, err
	}
	d, defIsObject := def.(*jsonx.Object)
	s, storedIsObject := stored.(*jsonx.Object)
	if defIsObject && storedIsObject {
		merged := d.Clone()
		for _, k := range s.Keys() {
			v, _ := s.Get(k)
			merged.Set(k, v)
		}
		return merged, nil
	}
	return stored, nil
}

type modelOption struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Status string  `json:"status"`
	Icon   *string `json:"icon,omitempty"`
}

func getSettingsPage(c *httpapi.Call) (any, error) {
	q := c.DB()
	page := jsonx.NewMap[any]()
	values := jsonx.NewMap[any]()
	for _, k := range []string{"timezone", "language", "uiLanguage", "modelRoles", "budgets", "fallbackChannel"} {
		v, err := settingValue(q, k)
		if err != nil {
			return nil, err
		}
		values.Set(k, v)
	}
	page.Set("settings", values)
	rows, err := connectors.List(q)
	if err != nil {
		return nil, err
	}
	models := []modelOption{}
	for _, r := range rows {
		if r.Kind != "model" {
			continue
		}
		m := modelOption{ID: r.ID, Label: r.Label, Status: r.Status}
		if man, ok := registry.Manifest(r.Type); ok {
			icon := man.Icon
			m.Icon = &icon
		}
		models = append(models, m)
	}
	page.Set("models", models)
	passkeys, err := auth.ListPasskeys(q, c.Viewer.User.ID)
	if err != nil {
		return nil, err
	}
	page.Set("passkeys", passkeys)
	page.Set("backup", backup.LatestBackup(c.DataDir()))
	list, err := hooks.List(q)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []hooks.View{}
	}
	page.Set("hooks", list)
	chats, err := chatOptions(q)
	if err != nil {
		return nil, err
	}
	page.Set("chats", chats)
	page.Set("appUrl", c.AppURL())
	return page, nil
}

// localeCompare is String.prototype.localeCompare (ICU root collation). A
// Collator keeps iteration state, so it is not safe for concurrent use: the
// Models tab asks for every model connector's list at once, and a shared one
// panicked with an index out of range. Each sort gets its own.
func localeCompare() *collate.Collator { return collate.New(language.Und) }

// listConnectorModels is the model ids a running model connector offers, for
// the model picker. A paused connector or a down runtime is an error the
// picker shows.
func listConnectorModels(c *httpapi.Call) (any, error) {
	rt, err := c.Runtime()
	if err != nil {
		return nil, err
	}
	res, err := rt.Action(c.Ctx, str(c, "id"), "listModels", jsonx.NewMap[any]())
	if err != nil {
		return nil, err
	}
	models := []string{}
	if res != nil {
		if list, ok := mustGet(res, "models").([]any); ok {
			seen := map[string]bool{}
			for _, m := range list {
				if s, ok := m.(string); ok && !seen[s] {
					seen[s] = true
					models = append(models, s)
				}
			}
		}
	}
	coll := localeCompare()
	slices.SortStableFunc(models, func(a, b string) int { return coll.CompareString(a, b) })
	return struct {
		Models []string `json:"models"`
	}{models}, nil
}

func saveSettings(c *httpapi.Call) (any, error) {
	o := c.Obj()
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		if zod.IsUndefined(v) {
			continue
		}
		if err := settings.Set(c.DB(), k, v, c.Now()); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"ok": true}, nil
}

func createHook(c *httpapi.Call) (any, error) {
	created, err := hooks.Create(c.DB(), hooks.CreateInput{Name: str(c, "name"), ChatID: str(c, "chatId"), Mode: str(c, "mode")}, c.MasterKey(), c.Now())
	if err != nil {
		return nil, httpapi.Fail("%s", err.Error())
	}
	return struct {
		Slug   string `json:"slug"`
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}{created.Hook.Slug, c.AppURL() + "/api/hooks/" + created.Hook.Slug, created.Secret}, nil
}

func init() {
	httpapi.Register(httpapi.Handler{Name: "getSettingsPage", Method: http.MethodGet, Guard: httpapi.User, Fn: getSettingsPage})
	httpapi.Register(httpapi.Handler{Name: "listConnectorModels", Method: http.MethodGet, Guard: httpapi.User, Input: idInput(), Fn: listConnectorModels})
	httpapi.Register(httpapi.Handler{Name: "saveSettings", Method: http.MethodPost, Guard: httpapi.Writer, Input: settings.PatchZod(), Fn: saveSettings})
	httpapi.Register(httpapi.Handler{Name: "createHookFn", Method: http.MethodPost, Guard: httpapi.Writer, Input: zod.Object(
		zod.F("name", zod.String().Trim().Min(1).Max(60)),
		zod.F("chatId", zod.String().Min(1).Max(100)),
		zod.F("mode", zod.Enum("forward", "summarize")),
	), Fn: createHook})
}

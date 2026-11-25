package rpcsetup

// apps/app/src/lib/fns/connectors.ts: the Connectors page (list,
// pause/resume, the connect wizard, test, actions, remove) and the owner
// settings that live on it.

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/connectors/manage"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

func idZod() *zod.StringS { return zod.String().Min(1).Max(64) }

func idInput() *zod.ObjectS { return zod.Object(zod.F("id", idZod())) }

// str reads a string field of the validated input ("" when absent).
func str(c *httpapi.Call, key string) string {
	v, _ := c.Obj().Get(key)
	s, _ := v.(string)
	return s
}

// present reports whether key was sent (null counts, undefined doesn't).
func present(o *jsonx.Object, key string) (any, bool) {
	v, ok := o.Get(key)
	if !ok || zod.IsUndefined(v) {
		return nil, false
	}
	return v, true
}

// runtimeUp is `await runtime().status()` without a throw.
func runtimeUp(c *httpapi.Call) (*httpapi.RuntimeStatus, bool) {
	rt, err := c.Runtime()
	if err != nil {
		return nil, false
	}
	st, err := rt.Status()
	if err != nil {
		return nil, false
	}
	return &st, true
}

// poke is connectors.ts poke(): reconcile now; false when the runtime is off.
func poke(c *httpapi.Call) bool {
	rt, err := c.Runtime()
	if err != nil {
		return false
	}
	rt.Reconcile()
	return true
}

type pokeResult struct {
	OK               bool `json:"ok"`
	RuntimeReachable bool `json:"runtimeReachable"`
}

type pageItem struct {
	manage.Item
	// The names of the agents a scoped connector serves ("" for one that's
	// gone), in its order; empty when global.
	OwnerNames []string `json:"ownerNames"`
}

type liveEntry struct {
	Running bool    `json:"running"`
	Status  string  `json:"status"`
	Detail  *string `json:"detail"`
}

type agentChoice struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	IsDefault   bool    `json:"isDefault"`
	AvatarImage *string `json:"avatarImage"`
	AvatarIcon  *string `json:"avatarIcon"`
	AvatarColor *string `json:"avatarColor"`
}

func getConnectorsPage(c *httpapi.Call) (any, error) {
	q := c.DB()
	list, err := agents.List(q, c.Now())
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, a := range list {
		names[a.ID] = a.Name
	}
	rows, err := manage.ListItems(q, c.MasterKey())
	if err != nil {
		return nil, err
	}
	items := make([]pageItem, 0, len(rows))
	for _, it := range rows {
		p := pageItem{Item: it, OwnerNames: []string{}}
		for _, id := range it.AgentIDs {
			p.OwnerNames = append(p.OwnerNames, names[id])
		}
		items = append(items, p)
	}
	live := jsonx.NewMap[liveEntry]()
	st, up := runtimeUp(c)
	if up {
		for _, l := range st.Connectors {
			live.Set(l.ID, liveEntry{Running: l.Running, Status: l.Status, Detail: l.Detail})
		}
	}
	// For the "Which agents" choice on a tool connector.
	list, err = agents.List(q, c.Now())
	if err != nil {
		return nil, err
	}
	choices := make([]agentChoice, 0, len(list))
	for _, a := range list {
		choices = append(choices, agentChoice{a.ID, a.Slug, a.Name, a.IsDefault, a.AvatarImage, a.AvatarIcon, a.AvatarColor})
	}
	return struct {
		Items     []pageItem            `json:"items"`
		Live      *jsonx.Map[liveEntry] `json:"live"`
		RuntimeUp bool                  `json:"runtimeUp"`
		AppURL    string                `json:"appUrl"`
		Agents    []agentChoice         `json:"agents"`
	}{items, live, up, c.AppURL(), choices}, nil
}

type detailEvent struct {
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type owners struct {
	Telegram bool    `json:"telegram"`
	WhatsApp *string `json:"whatsapp"`
}

type rowStatus struct {
	Status       string  `json:"status"`
	StatusDetail *string `json:"statusDetail"`
}

var jidSuffix = regexp.MustCompile(`@.*`)

func getConnectorDetail(c *httpapi.Call) (any, error) {
	q := c.DB()
	id := str(c, "id")
	evts, err := events.List(q, events.ListOptions{ConnectorID: id, Limit: 15})
	if err != nil {
		return nil, err
	}
	list := make([]detailEvent, 0, len(evts))
	for _, e := range evts {
		list = append(list, detailEvent{e.ID, e.At, e.Level, e.Kind, e.Message})
	}
	live := jsonx.NewMap[any]()
	st, up := runtimeUp(c)
	if up {
		for _, l := range st.Connectors {
			if l.ID == id {
				if l.Live != nil {
					live = l.Live
				}
				break
			}
		}
	}
	var o owners
	if _, ok, err := owner.GetOwner(q, "telegram"); err != nil {
		return nil, err
	} else {
		o.Telegram = ok
	}
	if row, ok, err := owner.GetOwner(q, "whatsapp"); err != nil {
		return nil, err
	} else if ok {
		phone := jidSuffix.ReplaceAllString(row.ExternalID, "")
		o.WhatsApp = &phone
	}
	// The page polls this, so it carries the status too.
	var status *rowStatus
	if row, ok, err := connectors.Get(q, id); err != nil {
		return nil, err
	} else if ok {
		status = &rowStatus{row.Status, row.StatusDetail}
	}
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	return struct {
		Events    []detailEvent `json:"events"`
		Live      *jsonx.Object `json:"live"`
		RuntimeUp bool          `json:"runtimeUp"`
		Owners    owners        `json:"owners"`
		Status    *rowStatus    `json:"status"`
		Timezone  string        `json:"timezone"`
	}{list, live, up, o, status, tz}, nil
}

func pauseConnector(c *httpapi.Call) (any, error) {
	if err := manage.Pause(c.DB(), str(c, "id"), "dashboard", c.Now()); err != nil {
		return nil, err
	}
	return pokeResult{true, poke(c)}, nil
}

func resumeConnector(c *httpapi.Call) (any, error) {
	if err := manage.Resume(c.DB(), str(c, "id"), "dashboard", c.Now()); err != nil {
		return nil, err
	}
	return pokeResult{true, poke(c)}, nil
}

// RuntimeDownRemoved is what removeConnectorAction says when it deleted the
// row itself.
const RuntimeDownRemoved = "The runtime was down, so nothing was unlinked on the device side. Credentials are deleted."

type removeResult struct {
	OK               bool    `json:"ok"`
	RuntimeReachable bool    `json:"runtimeReachable"`
	Detail           *string `json:"detail,omitempty"`
}

// removeConnector is removeConnectorAction: through the runtime, which
// unlinks devices and deletes the row; with the runtime off, the row and its
// credentials are deleted here and the caller is told.
func removeConnector(c *httpapi.Call) (any, error) {
	q, id, now := c.DB(), str(c, "id"), c.Now()
	if _, err := manage.MustExist(q, id); err != nil {
		return nil, err
	}
	rt, err := c.Runtime()
	if err != nil {
		if err := connectors.Delete(q, id, now); err != nil {
			return nil, err
		}
		d := RuntimeDownRemoved
		return removeResult{true, false, &d}, nil
	}
	res := rt.Remove(c.Ctx, id)
	if !res.OK {
		msg := "The runtime couldn't remove it."
		if res.Detail != nil {
			msg = *res.Detail
		}
		return nil, &manage.Error{Message: msg}
	}
	// The runtime normally deletes the row itself; make sure it's gone.
	if _, found, err := connectors.Get(q, id); err != nil {
		return nil, err
	} else if found {
		if err := connectors.Delete(q, id, now); err != nil {
			return nil, err
		}
	}
	return removeResult{true, true, res.Detail}, nil
}

func draftZod() *zod.ObjectS {
	return zod.Object(
		zod.F("id", idZod()),
		zod.F("type", zod.String().Min(1).Max(64)),
		zod.F("label", zod.String().Trim().Min(1).Max(80).Optional()),
		zod.F("config", zod.Record(zod.Unknown()).Optional()),
		zod.F("secrets", zod.Record(zod.String().Max(4000)).Optional()),
		zod.F("trust", zod.Enum("own", "external").Optional()),
		zod.F("agentIds", agentIDsZod().Optional()),
		// The older form: one agent, or null for every agent.
		zod.F("agentId", zod.Nullable(zod.String().Min(1).Max(100)).Optional()),
	)
}

func agentIDsZod() *zod.ArrayS { return zod.Array(zod.String().Min(1).Max(100)).Max(100) }

// stringList reads a validated array of strings.
func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func draftInput(o *jsonx.Object) manage.DraftInput {
	in := manage.DraftInput{}
	in.ID, _ = mustGet(o, "id").(string)
	in.Type, _ = mustGet(o, "type").(string)
	if v, ok := present(o, "label"); ok {
		s, _ := v.(string)
		in.Label = &s
	}
	if v, ok := present(o, "config"); ok {
		in.Config, _ = v.(*jsonx.Object)
	}
	if v, ok := present(o, "secrets"); ok {
		if m, ok := v.(*jsonx.Object); ok {
			in.Secrets = jsonx.NewMap[string]()
			for _, k := range m.Keys() {
				s, _ := m.Get(k)
				str, _ := s.(string)
				in.Secrets.Set(k, str)
			}
		}
	}
	if v, ok := present(o, "trust"); ok {
		s, _ := v.(string)
		in.Trust = &s
	}
	if v, ok := present(o, "agentIds"); ok {
		in.AgentIDsSet, in.AgentIDs = true, stringList(v)
	} else if v, ok := present(o, "agentId"); ok {
		in.AgentIDsSet = true
		if s, ok := v.(string); ok {
			in.AgentIDs = []string{s}
		}
	}
	return in
}

func mustGet(o *jsonx.Object, key string) any {
	v, _ := o.Get(key)
	return v
}

type fieldResult struct {
	OK               bool               `json:"ok"`
	Message          string             `json:"message,omitempty"`
	Fields           *jsonx.Map[string] `json:"fields,omitempty"`
	RuntimeReachable *bool              `json:"runtimeReachable,omitempty"`
}

// asFieldResult turns a ConnectorError into {ok: false, message, fields};
// anything else is thrown.
func asFieldResult(err error) (any, error) {
	if me, ok := manage.IsError(err); ok {
		fields := me.Fields
		if fields == nil {
			fields = jsonx.NewMap[string]()
		}
		return struct {
			OK      bool               `json:"ok"`
			Message string             `json:"message"`
			Fields  *jsonx.Map[string] `json:"fields"`
		}{false, me.Message, fields}, nil
	}
	return nil, err
}

func saveConnectorDraft(c *httpapi.Call) (any, error) {
	if _, err := manage.SaveDraft(c.DB(), draftInput(c.Obj()), c.MasterKey(), c.Now()); err != nil {
		return asFieldResult(err)
	}
	return fieldResult{OK: true}, nil
}

// connectConnector saves whatever the form holds, then asks the supervisor to
// bring it up.
func connectConnector(c *httpapi.Call) (any, error) {
	in := draftInput(c.Obj())
	if _, err := manage.SaveDraft(c.DB(), in, c.MasterKey(), c.Now()); err != nil {
		return asFieldResult(err)
	}
	if err := manage.Connect(c.DB(), in.ID, c.MasterKey(), c.Now()); err != nil {
		return asFieldResult(err)
	}
	reachable := poke(c)
	return fieldResult{OK: true, RuntimeReachable: &reachable}, nil
}

func testConnector(c *httpapi.Call) (any, error) {
	rt, err := c.Runtime()
	if err != nil {
		return nil, err
	}
	res := rt.Test(str(c, "id"))
	if !res.OK {
		return nil, httpapi.Fail("%s", res.Detail)
	}
	data := res.Data
	if data == nil {
		data = jsonx.NewMap[any]()
	}
	return struct {
		OK     bool          `json:"ok"`
		Detail string        `json:"detail"`
		Data   *jsonx.Object `json:"data"`
	}{res.OK, res.Detail, data}, nil
}

func connectorAction(c *httpapi.Call) (any, error) {
	id, name := str(c, "id"), str(c, "name")
	// The Google OAuth step runs before the connector is started, so the URL
	// is built here from the saved client id instead of through the runtime.
	if name == "authUrl" {
		url, err := gcal.GoogleAuthURLFor(c.DB(), id, c.MasterKey(), c.AppURL(), c.Now())
		if err != nil {
			return nil, httpapi.Fail("%s", err.Error())
		}
		return struct {
			URL         string `json:"url"`
			RedirectURI string `json:"redirectUri"`
		}{url, gcal.RedirectURIFor(c.AppURL())}, nil
	}
	rt, err := c.Runtime()
	if err != nil {
		return nil, err
	}
	var input *jsonx.Object
	if v, ok := present(c.Obj(), "input"); ok {
		input, _ = v.(*jsonx.Object)
	}
	return rt.Action(c.Ctx, id, name, input)
}

func setWhatsAppOwner(c *httpapi.Call) (any, error) {
	row, err := owner.SetWhatsAppOwner(c.DB(), str(c, "phone"), c.Now())
	if err != nil {
		return nil, err
	}
	return struct {
		JID string `json:"jid"`
	}{row.ExternalID}, nil
}

// duplicateConnector copies a connector into a new paused draft and returns
// its id, so the page can open the copy.
func duplicateConnector(c *httpapi.Call) (any, error) {
	row, err := manage.Duplicate(c.DB(), str(c, "id"), c.MasterKey(), c.Now())
	if err != nil {
		if me, ok := manage.IsError(err); ok {
			return nil, httpapi.Fail("%s", me.Message)
		}
		return nil, err
	}
	return struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}{true, row.ID}, nil
}

// setConnectorOwner makes a tool connector global (agentIds empty) or serve
// only the agents listed. agentId (one agent, or null for global) is the older
// form, kept for /mcp clients. The runtime reads the row each turn, so no poke
// is needed.
func setConnectorOwner(c *httpapi.Call) (any, error) {
	q, id := c.DB(), str(c, "id")
	row, found, err := connectors.Get(q, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httpapi.Fail("That connector doesn't exist anymore.")
	}
	if row.Kind != "tools" {
		return nil, httpapi.Fail("Only tool connectors can belong to an agent.")
	}
	var ids []string
	if v, ok := present(c.Obj(), "agentIds"); ok {
		ids = stringList(v)
	} else if v, ok := present(c.Obj(), "agentId"); ok {
		if s, ok := v.(string); ok {
			ids = []string{s}
		}
	} else {
		return nil, httpapi.Fail("Send agentIds: the agents, or an empty list for every agent.")
	}
	ids = connectors.CleanAgentIDs(ids)
	if err := manage.AgentsExist(q, ids, c.Now()); err != nil {
		return nil, httpapi.Fail("That agent doesn't exist anymore.")
	}
	if _, _, err := connectors.SetAgents(q, id, ids, c.Now()); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// setConnectorData sets or clears the owner's "holds no private data" mark on
// an MCP server (PLAN 19.4): its non-destructive tools then reach shared
// chats. Owner only: the dashboard's Tools tab, never a chat tool or /mcp.
// The runtime reads the row each turn, so no poke is needed.
func setConnectorData(c *httpapi.Call) (any, error) {
	id := str(c, "id")
	on := mustGet(c.Obj(), "noPrivateData") == true
	err := c.DB().Tx(func(tx db.Q) error {
		row, found, err := connectors.Get(tx, id)
		if err != nil {
			return err
		}
		if !found {
			return httpapi.Fail("That connector doesn't exist anymore.")
		}
		if row.Type != "mcp" {
			return httpapi.Fail("Only an MCP server can be marked as holding no private data.")
		}
		if _, _, err := connectors.SetNoPrivateData(tx, id, on, c.Now()); err != nil {
			return err
		}
		in := events.Input{ConnectorID: &id, Kind: "mcp.data", Message: "No longer marked as holding no private data"}
		if on {
			in.Level, in.Message = "warn", "Marked as holding no private data: shared chats can use its tools"
		}
		_, err = events.Log(tx, in, c.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// applySuggestedToolClasses saves, for every tool of an MCP server the owner
// hasn't classified yet, the class the server's annotations suggest. Until
// then those tools run as destructive (fail closed); a saved class is never
// touched, and a tool without class hints gets no pref, so it stays
// unclassified (destructive) and a hint the server adds later can still fill
// it. Owner only: the
// annotations are the server's own word, so only the owner can take it.
func applySuggestedToolClasses(c *httpapi.Call) (any, error) {
	id := str(c, "id")
	row, found, err := connectors.Get(c.DB(), id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httpapi.Fail("That connector doesn't exist anymore.")
	}
	if row.Type != "mcp" {
		return nil, httpapi.Fail("Only an MCP server suggests tool classes.")
	}
	rt, err := c.Runtime()
	if err != nil {
		return nil, err
	}
	listed, err := rt.Action(c.Ctx, id, "listTools", nil)
	if err != nil {
		return nil, err
	}
	suggested := mcpconn.SuggestionsFrom(listed)
	saved := 0
	err = c.DB().Tx(func(tx db.Q) error {
		// Read the prefs again here: a class saved since the listing wins.
		row, found, err := connectors.Get(tx, id)
		if err != nil {
			return err
		}
		if !found {
			return httpapi.Fail("That connector doesn't exist anymore.")
		}
		prefs, n := mcpconn.WithSuggested(row.Config, suggested)
		if n == 0 {
			return nil
		}
		in := manage.DraftInput{ID: id, Type: "mcp", Config: jsonx.NewMap[any]()}
		in.Config.Set("toolPrefs", prefs)
		if _, err := manage.SaveDraft(tx, in, c.MasterKey(), c.Now()); err != nil {
			return err
		}
		saved = n
		msg := fmt.Sprintf("Saved the server's suggested class for %d tool%s", n, kit.Plural(n))
		_, err = events.Log(tx, events.Input{ConnectorID: &id, Kind: "mcp.classes", Message: msg}, c.Now())
		return err
	})
	if err != nil {
		if me, ok := manage.IsError(err); ok {
			return nil, httpapi.Fail("%s", me.Message)
		}
		return nil, err
	}
	// The running connector picks the new prefs up on restart. Wait for it,
	// so the page's next load lists the tools with their new classes instead
	// of missing the server mid-restart.
	if saved > 0 {
		rt.Sup.Reconcile()
	}
	return struct {
		OK    bool `json:"ok"`
		Saved int  `json:"saved"`
	}{true, saved}, nil
}

// lockedConfigKeys are config keys updateConnectorFn refuses, with why.
var lockedConfigKeys = map[string]string{
	"toolPrefs":                 "send toolPrefs on its own, not inside config",
	connectors.NoPrivateDataKey: "only the owner marks a server as holding no private data, in the dashboard",
	"addedFromChat":             "Palui sets it when a server is added from chat, to keep it on public addresses",
}

func toolPrefPatchZod() zod.Schema {
	return zod.Nullable(zod.Object(
		zod.F("enabled", zod.Bool().Optional()),
		zod.F("class", zod.Enum("read", "write", "destructive").Optional()),
		zod.F("flowApproval", zod.Enum("version", "every_run").Optional()),
	))
}

func updateConnectorZod() *zod.ObjectS {
	return zod.Object(
		zod.F("id", idZod()),
		zod.F("label", zod.String().Trim().Min(1).Max(80).Optional()),
		zod.F("config", zod.Record(zod.Unknown()).Optional()),
		zod.F("toolPrefs", zod.Record(toolPrefPatchZod()).Optional()),
	)
}

// mergeToolPrefs applies a toolPrefs patch to the saved prefs: a tool sent
// null loses its saved pref (it's unclassified, so destructive, again); a tool
// sent an object keeps the fields it doesn't send. A tool without a saved pref
// starts enabled and destructive, so leaving out class never makes it safer.
func mergeToolPrefs(stored *jsonx.Object, patch *jsonx.Object) *jsonx.Object {
	prefs := jsonx.NewMap[any]()
	if stored != nil {
		if raw, ok := stored.Get("toolPrefs"); ok {
			if saved, ok := raw.(*jsonx.Object); ok {
				prefs = saved.Clone()
			}
		}
	}
	for _, name := range patch.Keys() {
		v, _ := patch.Get(name)
		p, ok := v.(*jsonx.Object)
		if !ok {
			prefs.Delete(name)
			continue
		}
		pref := jsonx.NewMap[any]()
		pref.Set("enabled", true)
		pref.Set("class", "destructive")
		if old, ok := prefs.Get(name); ok {
			if o, ok := old.(*jsonx.Object); ok {
				pref = o.Clone()
			}
		}
		for _, k := range []string{"enabled", "class", "flowApproval"} {
			if v, ok := present(p, k); ok {
				pref.Set(k, v)
			}
		}
		// "version" is the default; the dashboard leaves it out too.
		if fa, _ := pref.Get("flowApproval"); fa == "version" {
			pref.Delete("flowApproval")
		}
		prefs.Set(name, pref)
	}
	return prefs
}

// updateConnector edits a connector that exists through the same SaveDraft
// the dashboard's connector page uses: its label, its non-secret config
// fields (checked by the type's ConfigZod) and, on an MCP server, per-tool
// prefs merged into the saved ones. It never takes or returns secrets. The
// change is logged, and the supervisor restarts the connector on its new
// config before this returns.
func updateConnector(c *httpapi.Call) (any, error) {
	o, id := c.Obj(), str(c, "id")
	var saved db.Connector
	err := c.DB().Tx(func(tx db.Q) error {
		row, found, err := connectors.Get(tx, id)
		if err != nil {
			return err
		}
		if !found {
			return httpapi.Fail("That connector doesn't exist anymore.")
		}
		m, ok := registry.Manifest(row.Type)
		if !ok {
			return httpapi.Fail("Unknown connector type %q.", row.Type)
		}
		in := manage.DraftInput{ID: id, Type: row.Type}
		var changed []string
		if v, ok := present(o, "label"); ok {
			s, _ := v.(string)
			in.Label = &s
			changed = append(changed, "label")
		}
		if v, ok := present(o, "config"); ok {
			cfg, _ := v.(*jsonx.Object)
			for _, k := range cfg.Keys() {
				if why, locked := lockedConfigKeys[k]; locked {
					return httpapi.Fail("config.%s can't be changed here: %s.", k, why)
				}
				if m.ConfigZod == nil || m.ConfigZod.Field(k) == nil {
					return httpapi.Fail("A %s connector has no config field %q.", m.Name, k)
				}
			}
			if cfg.Len() > 0 {
				in.Config = cfg.Clone()
				changed = append(changed, "config "+strings.Join(cfg.Keys(), ", "))
			}
		}
		if v, ok := present(o, "toolPrefs"); ok {
			if row.Type != "mcp" {
				return httpapi.Fail("Only an MCP server has per-tool settings.")
			}
			patch, _ := v.(*jsonx.Object)
			if patch.Len() > 0 {
				if in.Config == nil {
					in.Config = jsonx.NewMap[any]()
				}
				in.Config.Set("toolPrefs", mergeToolPrefs(row.Config, patch))
				changed = append(changed, "tool settings for "+strings.Join(patch.Keys(), ", "))
			}
		}
		if len(changed) == 0 {
			return httpapi.Fail("Nothing to change: send label, config or toolPrefs.")
		}
		if saved, err = manage.SaveDraft(tx, in, c.MasterKey(), c.Now()); err != nil {
			return err
		}
		_, err = events.Log(tx, events.Input{ConnectorID: &id, Kind: "connector.update", Message: "Changed " + strings.Join(changed, "; ")}, c.Now())
		return err
	})
	if err != nil {
		if me, ok := manage.IsError(err); ok {
			return nil, httpapi.Fail("%s", me.FieldsText())
		}
		return nil, err
	}
	// Wait for the restart, so a get_connector right after sees the new
	// config live (applySuggestedToolClasses does the same).
	rt, rtErr := c.Runtime()
	if rtErr == nil {
		rt.Sup.Reconcile()
	}
	return struct {
		OK               bool          `json:"ok"`
		RuntimeReachable bool          `json:"runtimeReachable"`
		Label            string        `json:"label"`
		Config           *jsonx.Object `json:"config"`
	}{true, rtErr == nil, saved.Label, saved.Config}, nil
}

// phoneRe is /^\+?[\d\s-]{8,20}$/ with JS \s.
var phoneRe = regexp.MustCompile(`^\+?[\d` + strx.SpaceClass + `-]{8,20}$`)

func init() {
	get := func(name string, input zod.Schema, fn func(*httpapi.Call) (any, error)) {
		httpapi.Register(httpapi.Handler{Name: name, Method: http.MethodGet, Guard: httpapi.User, Input: input, Fn: fn})
	}
	post := func(name string, input zod.Schema, fn func(*httpapi.Call) (any, error)) {
		httpapi.Register(httpapi.Handler{Name: name, Method: http.MethodPost, Guard: httpapi.Writer, Input: input, Fn: fn})
	}
	get("getConnectorsPage", nil, getConnectorsPage)
	get("getConnectorDetail", idInput(), getConnectorDetail)
	post("pauseConnectorFn", idInput(), pauseConnector)
	post("resumeConnectorFn", idInput(), resumeConnector)
	post("removeConnectorFn", idInput(), removeConnector)
	post("saveConnectorDraft", draftZod(), saveConnectorDraft)
	post("connectConnectorFn", draftZod(), connectConnector)
	post("testConnectorFn", idInput(), testConnector)
	post("connectorActionFn", zod.Object(zod.F("id", idZod()), zod.F("name", zod.String().Min(1).Max(64)), zod.F("input", zod.Record(zod.Unknown()).Optional())), connectorAction)
	post("setWhatsAppOwnerFn", zod.Object(zod.F("phone", zod.String().Trim().Regex(phoneRe, `^\+?[\d\s-]{8,20}$`, "Use digits, like 0812 3456 7890 or +62 812 3456 7890."))), setWhatsAppOwner)
	post("duplicateConnectorFn", idInput(), duplicateConnector)
	post("setConnectorOwnerFn", zod.Object(zod.F("id", idZod()), zod.F("agentIds", agentIDsZod().Optional()), zod.F("agentId", zod.Nullable(zod.String().Min(1).Max(100)).Optional())), setConnectorOwner)
	post("setConnectorDataFn", zod.Object(zod.F("id", idZod()), zod.F("noPrivateData", zod.Bool())), setConnectorData)
	post("applySuggestedToolClassesFn", idInput(), applySuggestedToolClasses)
	post("updateConnectorFn", updateConnectorZod(), updateConnector)
}

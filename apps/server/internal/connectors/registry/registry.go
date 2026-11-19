// Package registry lists every connector type: the Go equivalent of
// connectors/src/manifests.ts and runtimes.ts. Order is the catalog and
// Connectors page order within each kind.
//
// Every slot has a Runtime since M7 (the channels); Manifests and Runtimes
// still skip a nil one.
package registry

import (
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/aigateway"
	"github.com/zakiego/palui/apps/server/internal/connectors/attachments"
	"github.com/zakiego/palui/apps/server/internal/connectors/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/cliproxy"
	"github.com/zakiego/palui/apps/server/internal/connectors/flows"
	"github.com/zakiego/palui/apps/server/internal/connectors/gcal"
	"github.com/zakiego/palui/apps/server/internal/connectors/hooks"
	"github.com/zakiego/palui/apps/server/internal/connectors/httpreq"
	"github.com/zakiego/palui/apps/server/internal/connectors/jobs"
	"github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/connectors/notes"
	"github.com/zakiego/palui/apps/server/internal/connectors/outbound"
	"github.com/zakiego/palui/apps/server/internal/connectors/printer"
	"github.com/zakiego/palui/apps/server/internal/connectors/recall"
	"github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/connectors/remotemcp"
	"github.com/zakiego/palui/apps/server/internal/connectors/settings"
	"github.com/zakiego/palui/apps/server/internal/connectors/t3code"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/connectors/tgbot"
	"github.com/zakiego/palui/apps/server/internal/connectors/voice"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
)

// Entry is one catalog slot.
type Entry struct {
	ID      string
	Runtime *connectors.Runtime
}

// Catalog is every connector type in manifests.ts order.
func Catalog() []Entry {
	return []Entry{
		{"telegram", telegram.Runtime},
		{"tgbot", tgbot.Runtime},
		{"whatsapp", whatsapp.Runtime},
		{"web", web.Runtime},
		{"cliproxy", cliproxy.Runtime},
		{"ai-gateway", aigateway.Runtime},
		{"recall", recall.Runtime},
		{"mcp", mcp.Runtime},
		{"http", httpreq.Runtime},
		{"gcal", gcal.Runtime},
		{"t3code", t3code.Runtime},
		{"printer", printer.Runtime},
		{"reminders", reminders.Runtime},
		{"outbound", outbound.Runtime},
		{"flows", flows.Runtime},
		{"jobs", jobs.Runtime},
		{"chats", chats.Runtime},
		{"notes", notes.Runtime},
		{"settings", settings.Runtime},
		{"remote-mcp", remotemcp.Runtime},
		{"attachments", attachments.Runtime},
		{"voice", voice.Runtime},
		{"webfetch", webfetch.Runtime},
		{"hooks", hooks.Runtime},
	}
}

// Manifests is manifestList: every ported manifest in catalog order.
func Manifests() []connectors.Manifest {
	var out []connectors.Manifest
	for _, e := range Catalog() {
		if e.Runtime != nil {
			out = append(out, e.Runtime.Manifest)
		}
	}
	return out
}

// Manifest is getManifest(id).
func Manifest(id string) (connectors.Manifest, bool) {
	for _, e := range Catalog() {
		if e.ID == id && e.Runtime != nil {
			return e.Runtime.Manifest, true
		}
	}
	return connectors.Manifest{}, false
}

// Runtimes is the runtimes map the supervisor takes
// (connectors.SupervisorOptions.Registry).
func Runtimes() map[string]*connectors.Runtime {
	out := map[string]*connectors.Runtime{}
	for _, e := range Catalog() {
		if e.Runtime != nil {
			out[e.ID] = e.Runtime
		}
	}
	return out
}

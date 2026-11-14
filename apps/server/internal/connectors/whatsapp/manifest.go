// Package whatsapp is the WhatsApp channel connector (connectors/src/whatsapp,
// PLAN 4.1, 5.1): a linked device on go.mau.fi/whatsmeow, with its session in
// a SQL store of its own at $DATA_DIR/wa/<connectorId>.db.
package whatsapp

import (
	"regexp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

func phone() *zod.StringS {
	return zod.String().Regex(regexp.MustCompile(`^\+?[\d\s().-]{8,24}$`), `^\+?[\d\s().-]{8,24}$`, "Enter a phone number like 0812 3456 789 or +62 812 3456 789")
}

// ConfigZod: ownerPhone is the owner's own number (messages from it and its
// learned LID are accepted); pairPhone is the number being linked (pairing
// codes are requested for it); linkedNumber is learned on connect.
var ConfigZod = zod.Object(
	zod.F("ownerPhone", phone().Optional()),
	zod.F("pairPhone", phone().Optional()),
	zod.F("linkedNumber", zod.String().Optional()),
)

var restartOnError = false

var Manifest = connectors.Manifest{
	ID:      "whatsapp",
	Kind:    "channel",
	Name:    "WhatsApp",
	Icon:    "whatsapp",
	Summary: "Chat from WhatsApp, as a linked device.",
	Setup: []connectors.SetupStep{
		{Action: "ownerPhone", Label: "Your phone number", Help: "Palui only answers DMs from this number."},
		{Action: "pairWhatsApp", Label: "Link the WhatsApp number"},
	},
	RemoveWarning:  "This unlinks the WhatsApp device. You'll need to pair again.",
	UnlinkOnRemove: true,
	// The runtime reconnects with its own backoff, and its error states
	// (unlinked, pairing expired) are final on purpose: restarting would push
	// new pairing codes.
	RestartOnError: &restartOnError,
	Docs: &connectors.Docs{Notes: []string{
		"DM only in v1. Groups, status updates and newsletters are ignored.",
		"An ⏳ reaction shows while Palui is thinking and clears when it replies.",
		"Buttons become a reply hint, for example \"Balas: ya / batal\".",
	}},
	ConfigZod:   ConfigZod,
	ParseConfig: kit.ParseConfig(ConfigZod),
}

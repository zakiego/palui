package rpcall_test

import (
	"net/http"
	"sort"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcall"
)

// Every createServerFn in apps/app/src/lib/fns, with its method.
var want = map[string]string{
	// activity.ts
	"getEvents": "GET", "getRuns": "GET", "getRun": "GET", "getUsage": "GET",
	// agents.ts
	"getAgentsPage": "GET", "getAgentPage": "GET", "getAgentToolsFn": "GET", "getAgentMemoryFn": "GET",
	"saveAgentFn": "POST", "deleteAgentFn": "POST", "setAgentToolPermissionsFn": "POST", "setChatAgentFn": "POST",
	"setChatReplyModeFn": "POST", "setChatAckFn": "POST", "setChatAllowedFn": "POST", "setChatDeployNoticeFn": "POST",
	"getAgentMembersFn": "GET", "saveMemberFn": "POST", "setAgentMemberFn": "POST",
	// chat rules (agent page)
	"getChatRulesFn": "GET", "setMemberRulesFn": "POST", "deleteChatRuleFn": "POST", "undoChatRuleChangeFn": "POST",
	// attachments.ts
	"getAttachments": "GET", "deleteAttachmentFn": "POST", "getAttachmentShares": "GET",
	"createAttachmentShareFn": "POST", "revokeAttachmentShareFn": "POST", "setChatSaveMediaFn": "POST", "deleteAutoSavedFn": "POST",
	// chats.ts
	"getChats": "GET", "getTranscript": "GET", "sendWebMessage": "POST", "decideApproval": "POST", "stopTurn": "POST",
	// connectors.ts
	"getConnectorsPage": "GET", "getConnectorDetail": "GET", "pauseConnectorFn": "POST", "resumeConnectorFn": "POST",
	"removeConnectorFn": "POST", "saveConnectorDraft": "POST", "connectConnectorFn": "POST", "testConnectorFn": "POST",
	"connectorActionFn": "POST", "setWhatsAppOwnerFn": "POST", "setConnectorOwnerFn": "POST", "setConnectorDataFn": "POST", "applySuggestedToolClassesFn": "POST", "duplicateConnectorFn": "POST",
	// dev-fixtures.ts
	"getFixtureState": "GET", "setFixture": "POST",
	// flows.ts
	"getFlows": "GET", "getFlowFn": "GET",
	// home.ts
	"getShell": "GET", "getHome": "GET", "dismissChecklist": "POST", "getStatusSummary": "GET",
	// jobs.ts
	"getJobs": "GET", "runJobNow": "POST", "setJobEnabledFn": "POST", "saveJob": "POST", "deleteJobFn": "POST", "getJobPreset": "GET",
	// notes.ts
	"getNotes": "GET", "saveInstructionFn": "POST", "setInstructionEnabledFn": "POST", "deleteInstructionFn": "POST",
	"setPreferenceFn": "POST", "deletePreferenceFn": "POST",
	// commands.ts
	"getCommandsPage": "GET", "saveCommandFn": "POST", "setCommandEnabledFn": "POST", "deleteCommandFn": "POST",
	// mcp-access.ts
	"getMcpAccess": "GET", "createMcpToken": "POST", "revokeMcpAccess": "POST", "getOAuthRequest": "GET", "decideOAuthRequest": "POST",
	// outbound.ts
	"getOutbound": "GET", "setOutboundSwitch": "POST", "saveOutboundLimits": "POST", "addContactFn": "POST",
	"setContactPolicy": "POST", "removeContactFn": "POST", "cancelOutboundFn": "POST",
	// reminders.ts
	"getReminders": "GET", "parseReminder": "POST", "createReminderFn": "POST", "cancelReminderFn": "POST", "snoozeReminderFn": "POST",
	// session.ts
	"getSession": "GET", "getInvite": "GET", "acceptInvite": "POST", "listPasskeys": "GET", "removePasskey": "POST",
	// settings.ts
	"getSettingsPage": "GET", "listConnectorModels": "GET", "saveSettings": "POST", "createHookFn": "POST",
}

var public = map[string]bool{"getSession": true, "getInvite": true, "acceptInvite": true}

func TestEveryServerFunctionIsRegistered(t *testing.T) {
	if len(want) != 99 {
		t.Fatalf("the list has %d names, want 99", len(want))
	}
	got := map[string]httpapi.Handler{}
	for _, h := range httpapi.Handlers() {
		got[h.Name] = h
	}
	var missing []string
	for name, method := range want {
		h, ok := got[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if h.Method != method {
			t.Errorf("%s: method %s, want %s", name, h.Method, method)
		}
		if err := httpapi.CheckHandler(h); err != nil {
			t.Error(err)
		}
		if h.Guard == httpapi.Public && !public[name] {
			t.Errorf("%s is public", name)
		}
		if h.Method == http.MethodPost && h.Guard != httpapi.Writer && h.Guard != httpapi.Public {
			t.Errorf("%s: POST without Writer", name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d functions not registered: %v", len(missing), missing)
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s is registered but is not a server function", name)
		}
	}
}

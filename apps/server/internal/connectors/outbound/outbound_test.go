package outbound_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	outboundconn "github.com/zakiego/palui/apps/server/internal/connectors/outbound"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Tuesday 2026-11-10 10:00 WITA.
const (
	NOW   = int64(1794276000000)
	WITA  = "Asia/Makassar"
	AGENT = "agt_palui"
)

var SIG = settings.DefaultOutboundSignature

const otwText = "Halo, ini asisten Zaki. Zaki lagi otw ya."

var otw = `{"to":"Zaki","text":"` + otwText + `"}`

type env struct {
	db     *db.DB
	chatID string
	turn   connectors.TurnInfo
}

func setup(t *testing.T, mut func(*connectors.TurnInfo), tainted bool) env {
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, NOW)
	testutil.Must(t, err)
	owner := "chat Zaki sekarang, bilang gue otw"
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: chat.ID, Channel: "telegram", Trigger: "message", TZ: WITA, Now: NOW, Origin: "message", Mode: "owner", AgentID: AGENT, Tainted: func() bool { return tainted }, OwnerText: &owner}
	if mut != nil {
		mut(&turn)
	}
	return env{d, chat.ID, turn}
}

func (e env) tools(t *testing.T) *connectors.ToolSet {
	set, err := outboundconn.Tools(e.db, e.turn, func() int64 { return NOW })
	testutil.Must(t, err)
	return set
}

func (e env) add(t *testing.T, name, phone string) db.Contact {
	c, err := outbound.AddContact(e.db, outbound.AddContactInput{AgentID: AGENT, Name: name, Phone: phone}, NOW)
	testutil.Must(t, err)
	return c
}

func (e env) allow(t *testing.T, c db.Contact) {
	_, err := outbound.UpdateContact(e.db, c.ID, outbound.ContactPatch{Policy: testutil.Ptr("allow")}, NOW)
	testutil.Must(t, err)
}

func tool(t *testing.T, set *connectors.ToolSet, name string) connectors.PaluiTool {
	pt, ok := set.Get(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	return pt
}

func outboxText(t *testing.T, d *db.DB, source string) (string, string, int64) {
	t.Helper()
	var payload, chatID string
	var next int64
	testutil.Must(t, d.QueryRow("SELECT payload, chat_id, next_attempt_at FROM outbox WHERE source = ?", source).Scan(&payload, &chatID, &next))
	return conntest.Of(rawJSON(payload)).S("text"), chatID, next
}

type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) { return []byte(r), nil }

func TestManifestIsABuiltinSkill(t *testing.T) {
	m := outboundconn.Manifest
	if m.ID != "outbound" || m.Kind != "skill" || !m.Builtin {
		t.Fatalf("%+v", m)
	}
}

func TestOnlyTurnsTheOwnerStartedInHisOwnChatGetTheSendTools(t *testing.T) {
	muts := []func(*connectors.TurnInfo){
		func(x *connectors.TurnInfo) { x.Origin = "flow" },
		func(x *connectors.TurnInfo) { x.Origin = "schedule" },
		func(x *connectors.TurnInfo) { x.Origin = "hook" },
		func(x *connectors.TurnInfo) { x.Origin = "reminder" },
		func(x *connectors.TurnInfo) { x.Mode = "guest" },
		func(x *connectors.TurnInfo) { x.Mode = "sharedOwner" },
		func(x *connectors.TurnInfo) { x.Origin = "" },
	}
	for i, m := range muts {
		keys := setup(t, m, false).tools(t).Keys()
		sort.Strings(keys)
		if strings.Join(keys, ",") != "list_contacts,list_outbound_messages" {
			t.Fatal(i, keys)
		}
	}
	keys := setup(t, nil, false).tools(t).Keys()
	sort.Strings(keys)
	if strings.Join(keys, ",") != "add_contact,cancel_message,list_contacts,list_outbound_messages,send_message" {
		t.Fatal(keys)
	}
}

func TestAlwaysAsksForAnAskContactAndForANewNumber(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	pt := tool(t, e.tools(t), "send_message")
	if !pt.NeedsApproval([]byte(otw)) || !pt.NeedsApproval([]byte(`{"to":"081111111111","name":"Budi","text":"halo"}`)) {
		t.Fatal("should ask")
	}
}

func TestSkipsApprovalForAnAllowContactOnlyWhenTheOwnerNamedItInAnUntaintedTurn(t *testing.T) {
	named := setup(t, nil, false)
	named.allow(t, named.add(t, "Zaki", "082200001001"))
	if tool(t, named.tools(t), "send_message").NeedsApproval([]byte(otw)) {
		t.Fatal("named asked")
	}
	tainted := setup(t, nil, true)
	tainted.allow(t, tainted.add(t, "Zaki", "082200001001"))
	if !tool(t, tainted.tools(t), "send_message").NeedsApproval([]byte(otw)) {
		t.Fatal("tainted skipped")
	}
	unnamed := setup(t, func(x *connectors.TurnInfo) { s := "kabarin dia aja"; x.OwnerText = &s }, false)
	unnamed.allow(t, unnamed.add(t, "Zaki", "082200001001"))
	if !tool(t, unnamed.tools(t), "send_message").NeedsApproval([]byte(otw)) {
		t.Fatal("unnamed skipped")
	}
	resumed := setup(t, func(x *connectors.TurnInfo) { x.OwnerText, x.Trigger = nil, "approval" }, false)
	resumed.allow(t, resumed.add(t, "Zaki", "082200001001"))
	if !tool(t, resumed.tools(t), "send_message").NeedsApproval([]byte(otw)) {
		t.Fatal("resumed skipped")
	}
}

func TestPreviewsTheRecipientTheTimeAndTheExactText(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	pt := tool(t, e.tools(t), "send_message")
	if got := pt.Preview([]byte(otw)); got != "Kirim pesan ke Zaki (+62 822-0000-1001)\nKapan: sekarang (ditahan 15 detik, bisa dibatalin)\nIsi pesan:\n"+otwText+"\n\n"+SIG {
		t.Fatal(got)
	}
	if got := pt.Preview([]byte(`{"to":"081111111111","name":"Budi","text":"halo","localDateTime":"2026-11-10T20:00"}`)); got != "Kirim pesan ke Budi (+62 811-1111-1111), kontak baru\nKapan: Hari ini, 20.00 WITA\nIsi pesan:\nhalo\n\n"+SIG {
		t.Fatal(got)
	}
	if got := pt.Preview([]byte(`{"to":"Zaki","text":"kabar","localDateTime":"2026-11-13T11:00","rrule":"FREQ=WEEKLY;BYDAY=FR"}`)); !strings.Contains(got, "Kapan: Tiap Jumat") {
		t.Fatal(got)
	}
}

func TestSendsNowToASavedContactWithAHoldAndACancelNotice(t *testing.T) {
	e := setup(t, nil, false)
	c := e.add(t, "Zaki", "082200001001")
	r := conntest.Call(t, e.tools(t), "send_message", otw)
	if !r.B("ok") {
		t.Fatal(r)
	}
	text, chatID, next := outboxText(t, e.db, "outbound:"+r.S("id"))
	if chatID != c.ChatID || next != NOW+15_000 || text != otwText+"\n\n"+SIG {
		t.Fatal(chatID, next, text)
	}
	notice, noticeChat, _ := outboxText(t, e.db, "outbound-notice:"+r.S("id"))
	if noticeChat != e.chatID || !strings.Contains(notice, otwText+"\n\n"+SIG) {
		t.Fatal(notice)
	}
	var approvedBy, origin, ownerChat string
	testutil.Must(t, e.db.QueryRow("SELECT approved_by, origin, owner_chat_id FROM outbound_messages").Scan(&approvedBy, &origin, &ownerChat))
	if approvedBy != "policy" || origin != "chat" || ownerChat != e.chatID {
		t.Fatal(approvedBy, origin, ownerChat)
	}
}

func TestSavesANewNumberOnlyInAnApprovedRun(t *testing.T) {
	plain := setup(t, nil, false)
	if got := conntest.Call(t, plain.tools(t), "send_message", `{"to":"082200001001","name":"Zaki","text":"otw"}`).String(); got != `{"ok":false,"error":"A new number needs the owner's approval first."}` {
		t.Fatal(got)
	}
	if list, _ := outbound.ListContacts(plain.db, nil); len(list) != 0 {
		t.Fatal("saved")
	}
	approved := setup(t, func(x *connectors.TurnInfo) { x.Trigger, x.OwnerText = "approval", nil }, false)
	r := conntest.Call(t, approved.tools(t), "send_message", `{"to":"082200001001","name":"Zaki","text":"otw"}`)
	if !r.B("ok") || !r.B("newContact") {
		t.Fatal(r)
	}
	list, _ := outbound.ListContacts(approved.db, nil)
	if len(list) != 1 || list[0].Name != "Zaki" || list[0].Address != "6282200001001" || list[0].Policy != "ask" {
		t.Fatalf("%+v", list)
	}
	var by string
	testutil.Must(t, approved.db.QueryRow("SELECT approved_by FROM outbound_messages").Scan(&by))
	if by != "owner" {
		t.Fatal(by)
	}
}

func TestRefusesUnknownNamesAndNumbersWithoutAName(t *testing.T) {
	e := setup(t, nil, false)
	if got := conntest.Call(t, e.tools(t), "send_message", `{"to":"Budi","text":"x"}`).S("error"); !strings.Contains(got, "No saved contact named Budi") {
		t.Fatal(got)
	}
	if got := conntest.Call(t, e.tools(t), "send_message", `{"to":"082200001001","text":"x"}`).S("error"); !strings.Contains(got, "Ask the owner for the person's name") {
		t.Fatal(got)
	}
}

func TestSchedulesAOneOffAndARepeat(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	later := conntest.Call(t, e.tools(t), "send_message", `{"to":"Zaki","text":"`+otwText+`","localDateTime":"2026-11-10T20:00"}`)
	if later.S("message") != "Oke, pesan ke Zaki dijadwalin: Hari ini, 20.00 WITA." {
		t.Fatal(later)
	}
	weekly := conntest.Call(t, e.tools(t), "send_message", `{"to":"Zaki","text":"kabar","localDateTime":"2026-11-13T11:00","rrule":"FREQ=WEEKLY;BYDAY=FR"}`)
	if !strings.Contains(weekly.S("message"), "Tiap Jumat") {
		t.Fatal(weekly)
	}
	var n int
	testutil.Must(t, e.db.QueryRow("SELECT count(*) FROM outbox WHERE source LIKE 'outbound:%'").Scan(&n))
	if n != 0 {
		t.Fatal(n)
	}
}

func TestRefusesWhenTheKillSwitchIsOff(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	_, err := outbound.SetEnabled(e.db, false, NOW)
	testutil.Must(t, err)
	if got := conntest.Call(t, e.tools(t), "send_message", otw).S("error"); !strings.Contains(got, "turned off") {
		t.Fatal(got)
	}
}

func TestCancelsAHeldSend(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	r := conntest.Call(t, e.tools(t), "send_message", otw)
	if got := conntest.Call(t, e.tools(t), "cancel_message", `{"id":"`+r.S("id")+`"}`).String(); got != `{"ok":true,"message":"Oke, pesan ke Zaki dibatalin."}` {
		t.Fatal(got)
	}
}

func TestSignaturePreviewsNoSignatureWhenTheOwnerOptedOutAndSendsItBare(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	pt := tool(t, e.tools(t), "send_message")
	in := `{"to":"Zaki","text":"` + otwText + `","signature":false}`
	if got := pt.Preview([]byte(in)); got != "Kirim pesan ke Zaki (+62 822-0000-1001)\nKapan: sekarang (ditahan 15 detik, bisa dibatalin)\nTanpa tanda tangan\nIsi pesan:\n"+otwText {
		t.Fatal(got)
	}
	r := conntest.Exec(t, pt.Tool, in)
	if text, _, _ := outboxText(t, e.db, "outbound:"+r.S("id")); text != otwText {
		t.Fatal(text)
	}
}

func TestSignatureTellsTheModelTheOptOutIsOnlyForAnExplicitAsk(t *testing.T) {
	e := setup(t, nil, false)
	schema := conntest.Of(rawJSON(tool(t, e.tools(t), "send_message").Tool.InputSchema))
	if !regexp.MustCompile(`only when the owner explicitly asked`).MatchString(schema.S("properties.signature.description")) {
		t.Fatal(schema)
	}
}

func TestSignatureDoesNotSignTwiceWhenTheAgentAlreadyWroteIt(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	for _, sig := range append([]string{SIG}, settings.LegacyOutboundSignatures...) {
		text := otwText + "\n\n" + sig
		in := conntest.Of(map[string]string{"to": "Zaki", "text": text}).String()
		pt := tool(t, e.tools(t), "send_message")
		if !strings.Contains(pt.Preview([]byte(in)), otwText+"\n\n\u2014") {
			t.Fatal(pt.Preview([]byte(in)))
		}
		r := conntest.Exec(t, pt.Tool, in)
		if sent, _, _ := outboxText(t, e.db, "outbound:"+r.S("id")); sent != text {
			t.Fatal(sent)
		}
	}
}

func TestSignatureSchedulesAOneOffAndARepeatWithTheSignature(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	conntest.Call(t, e.tools(t), "send_message", `{"to":"Zaki","text":"`+otwText+`","localDateTime":"2026-11-10T20:00"}`)
	conntest.Call(t, e.tools(t), "send_message", `{"to":"Zaki","text":"kabar","localDateTime":"2026-11-13T11:00","rrule":"FREQ=WEEKLY;BYDAY=FR"}`)
	rows, err := e.db.Query("SELECT text FROM outbound_messages ORDER BY rowid")
	testutil.Must(t, err)
	var texts []string
	for rows.Next() {
		var s string
		testutil.Must(t, rows.Scan(&s))
		texts = append(texts, s)
	}
	rows.Close()
	if len(texts) != 2 || texts[0] != otwText+"\n\n"+SIG || texts[1] != "kabar\n\n"+SIG {
		t.Fatal(texts)
	}
}

func TestSignaturePreviewsTheOwnersEditedSignature(t *testing.T) {
	e := setup(t, nil, false)
	e.add(t, "Zaki", "082200001001")
	d := settings.Defaults().Outbound
	_, err := outbound.SaveSettings(e.db, outbound.SettingsPatch{PerContactPerDay: d.PerContactPerDay, PerDay: d.PerDay, HoldSeconds: d.HoldSeconds, SignatureOn: d.SignatureOn, Signature: "_Asisten Zaki_"}, NOW)
	testutil.Must(t, err)
	if got := tool(t, e.tools(t), "send_message").Preview([]byte(otw)); !strings.HasSuffix(got, "lagi otw ya.\n\n_Asisten Zaki_") {
		t.Fatal(got)
	}
}

func TestPerAgentAnotherAgentsTurnCantSeeOrMessageThisAgentsContacts(t *testing.T) {
	e := setup(t, nil, false)
	e.allow(t, e.add(t, "Zaki", "082200001001"))
	owner := "chat Zaki"
	other := connectors.TurnInfo{RunID: "run_2", ChatID: e.chatID, Channel: "telegram", Trigger: "message", TZ: WITA, Now: NOW, Origin: "message", Mode: "owner", AgentID: "agt_other", Tainted: func() bool { return false }, OwnerText: &owner}
	set, err := outboundconn.Tools(e.db, other, func() int64 { return NOW })
	testutil.Must(t, err)
	if conntest.Call(t, set, "list_contacts", `{}`).JSON("contacts") != "[]" {
		t.Fatal("saw contacts")
	}
	if !strings.Contains(conntest.Call(t, set, "send_message", otw).S("error"), "No saved contact named Zaki") {
		t.Fatal("found Zaki")
	}
}

var ana = `{"name":"Ana","phone":"+62 822-0000-1002"}`

func approvedRun(t *testing.T) env {
	return setup(t, func(x *connectors.TurnInfo) { x.Trigger, x.OwnerText = "approval", nil }, false)
}

func TestAddContactNormalizesTheNumberTheSameWayTheDashboardFormDoes(t *testing.T) {
	for _, phone := range []string{"082200001002", "0822 0000 1002", "0822-0000-1002", "+62 822-0000-1002", "+6282200001002", "6282200001002"} {
		e := approvedRun(t)
		r := conntest.Call(t, e.tools(t), "add_contact", `{"name":"Ana","phone":"`+phone+`"}`)
		if !r.B("ok") || r.B("existing") {
			t.Fatal(phone, r)
		}
		list, _ := outbound.ListContacts(e.db, nil)
		if len(list) != 1 || list[0].Name != "Ana" || list[0].Channel != "whatsapp" || list[0].Address != "6282200001002" {
			t.Fatalf("%+v", list)
		}
	}
	if got := conntest.Call(t, approvedRun(t).tools(t), "add_contact", `{"name":"Ana","phone":"12345"}`).S("error"); !strings.Contains(got, "isn't a phone number") {
		t.Fatal(got)
	}
}

func TestAddContactAlwaysAsksAndPreviewsTheNormalizedNumber(t *testing.T) {
	pt := tool(t, setup(t, nil, false).tools(t), "add_contact")
	if pt.Class != "write" || !pt.NeedsApproval([]byte(ana)) || !pt.NeedsApproval([]byte(`{"name":"Ana","phone":"+62 822-0000-1002","policy":"allow"}`)) {
		t.Fatal("should ask")
	}
	if !tool(t, setup(t, nil, true).tools(t), "add_contact").NeedsApproval([]byte(ana)) {
		t.Fatal("tainted skipped")
	}
	if got := pt.Preview([]byte(`{"name":"Ana","phone":"+62 822-0000-1002","note":"istri"}`)); got != "Tambah kontak Ana (+62 822-0000-1002)\nCatatan: istri\nPersetujuan kirim: selalu tanya" {
		t.Fatal(got)
	}
}

func TestAddContactSavesNothingUntilTheOwnerApproves(t *testing.T) {
	e := setup(t, nil, false)
	if got := conntest.Call(t, e.tools(t), "add_contact", ana).String(); got != `{"ok":false,"error":"Adding a contact needs the owner's approval first."}` {
		t.Fatal(got)
	}
	if list, _ := outbound.ListContacts(e.db, nil); len(list) != 0 {
		t.Fatal("saved")
	}
}

func TestAddContactOnlyExistsInTurnsTheOwnerStarted(t *testing.T) {
	for _, m := range []func(*connectors.TurnInfo){
		func(x *connectors.TurnInfo) { x.Origin, x.Trigger = "flow", "flow" },
		func(x *connectors.TurnInfo) { x.Origin = "hook" },
		func(x *connectors.TurnInfo) { x.Origin = "schedule" },
		func(x *connectors.TurnInfo) { x.Mode = "guest" },
	} {
		if _, ok := setup(t, m, false).tools(t).Get("add_contact"); ok {
			t.Fatal("offered")
		}
	}
}

func TestAddContactNewContactsAlwaysStartAtAlwaysAsk(t *testing.T) {
	e := approvedRun(t)
	conntest.Call(t, e.tools(t), "add_contact", `{"name":"Ana","phone":"+62 822-0000-1002","policy":"allow","note":"istri"}`)
	list, _ := outbound.ListContacts(e.db, nil)
	if len(list) != 1 || list[0].Policy != "ask" || list[0].Note == nil || *list[0].Note != "istri" || list[0].AgentID == nil || *list[0].AgentID != AGENT {
		t.Fatalf("%+v", list)
	}
}

func TestAddContactReturnsAnExistingNumberInsteadOfSavingItTwice(t *testing.T) {
	e := approvedRun(t)
	e.add(t, "Ana", "082200001002")
	pt := tool(t, e.tools(t), "add_contact")
	in := `{"name":"Istri","phone":"0822 0000 1002"}`
	if pt.NeedsApproval([]byte(in)) {
		t.Fatal("asked")
	}
	r := conntest.Exec(t, pt.Tool, in)
	if !r.B("ok") || !r.B("existing") || !strings.Contains(r.S("message"), "sudah ada sebagai Ana") {
		t.Fatal(r)
	}
	if list, _ := outbound.ListContacts(e.db, nil); len(list) != 1 {
		t.Fatal(len(list))
	}
}

func TestAddContactSaysSoWhenTheNameBelongsToAnotherNumber(t *testing.T) {
	e := approvedRun(t)
	e.add(t, "Ana", "081111111111")
	r := conntest.Call(t, e.tools(t), "add_contact", `{"name":"ana","phone":"082200001002"}`)
	if r.B("ok") || !strings.Contains(r.S("error"), "Ana is already used by another number") {
		t.Fatal(r)
	}
	if list, _ := outbound.ListContacts(e.db, nil); len(list) != 1 {
		t.Fatal(len(list))
	}
}

func TestAddContactSavesToTheCallingAgent(t *testing.T) {
	e := setup(t, nil, false)
	other := connectors.TurnInfo{RunID: "run_2", ChatID: e.chatID, Channel: "telegram", Trigger: "approval", TZ: WITA, Now: NOW, Origin: "message", Mode: "owner", AgentID: "agt_other", Tainted: func() bool { return false }}
	set, err := outboundconn.Tools(e.db, other, func() int64 { return NOW })
	testutil.Must(t, err)
	conntest.Call(t, set, "add_contact", ana)
	o, a := "agt_other", AGENT
	if l, _ := outbound.ListContacts(e.db, &o); len(l) != 1 {
		t.Fatal(len(l))
	}
	if l, _ := outbound.ListContacts(e.db, &a); len(l) != 0 {
		t.Fatal(len(l))
	}
}

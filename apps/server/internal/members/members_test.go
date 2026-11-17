package members_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Fake numbers only.
const (
	ownerPhone = "6281200000009"
	phoneA     = "6281200000001@s.whatsapp.net"
	lidA       = "100000000000001@lid"
	phoneB     = "6281200000002@s.whatsapp.net"
	lidB       = "100000000000002@lid"
)

func s(v string) *string { return &v }

func agent(t *testing.T, d *db.DB, slug string) db.Agent {
	t.Helper()
	a, err := agents.Create(d, agents.Input{Slug: slug, Name: s(slug), Persona: s("p")}, 1)
	testutil.Must(t, err)
	return a
}

func wa(addr string) members.AccountInput {
	return members.AccountInput{Channel: "whatsapp", Address: addr}
}

func memberErr(t *testing.T, err error, sub string) {
	t.Helper()
	var e *members.Error
	if !errors.As(err, &e) || !strings.Contains(e.Msg, sub) {
		t.Fatalf("want members.Error with %q, got %v", sub, err)
	}
}

func TestNormalizeAccount(t *testing.T) {
	for _, c := range []struct{ channel, in, want string }{
		{"whatsapp", "0812 0000 0001", phoneA},
		{"whatsapp", "+62 812-0000-0001", phoneA},
		{"whatsapp", lidA, lidA},
		{"whatsapp", phoneA, phoneA},
		{"telegram", "123456789", "123456789"},
	} {
		got, err := members.NormalizeAccount(c.channel, c.in)
		if err != nil || got != c.want {
			t.Fatalf("%s %q: %q %v", c.channel, c.in, got, err)
		}
	}
	for _, c := range []struct{ channel, in string }{
		{"telegram", "12ab"},
		{"telegram", "-100123"},
		{"whatsapp", "0812"},
		{"whatsapp", "1234567890123456789"},
		{"whatsapp", "120363000000000001@g.us"},
		{"web", "x"},
		{"email", "a@b.c"},
	} {
		if _, err := members.NormalizeAccount(c.channel, c.in); err == nil {
			t.Fatalf("%s %q accepted", c.channel, c.in)
		}
	}
}

func TestCreateRefusesOwnerAndTakenAccounts(t *testing.T) {
	d := testutil.DB(t)
	_, err := owner.SetWhatsAppOwner(d, ownerPhone, 1)
	testutil.Must(t, err)
	_, err = members.Create(d, "Me", []members.AccountInput{wa("0812 0000 0009")}, 1)
	memberErr(t, err, "That's your own account.")

	a, err := members.Create(d, "  Ani\nPutri ", []members.AccountInput{wa("0812 0000 0001")}, 1)
	testutil.Must(t, err)
	if a.Name != "Ani Putri" || len(a.Accounts) != 1 || a.Accounts[0].ExternalID != phoneA || len(a.AgentIDs) != 0 {
		t.Fatalf("%+v", a)
	}
	_, err = members.Create(d, "Budi", []members.AccountInput{wa(phoneA)}, 1)
	memberErr(t, err, "That account already belongs to Ani Putri.")
	_, err = members.Create(d, "", []members.AccountInput{wa(phoneB)}, 1)
	memberErr(t, err, "name")
	_, err = members.Create(d, strings.Repeat("x", members.MaxName+1), []members.AccountInput{wa(phoneB)}, 1)
	memberErr(t, err, "name")
	_, err = members.Create(d, "Budi", nil, 1)
	memberErr(t, err, "account")
	_, err = members.Create(d, "Budi", []members.AccountInput{wa(phoneB), wa("0812 0000 0002")}, 1)
	memberErr(t, err, "twice")
	six := []members.AccountInput{}
	for i := range 6 {
		six = append(six, members.AccountInput{Channel: "telegram", Address: strings.Repeat("1", i+1)})
	}
	_, err = members.Create(d, "Budi", six, 1)
	memberErr(t, err, "at most")
}

func TestIsMemberOnlyForLinkedAgent(t *testing.T) {
	d := testutil.DB(t)
	arisan := agent(t, d, "arisan")
	cs := agent(t, d, "cs")
	m, err := members.Create(d, "Ani", []members.AccountInput{wa(phoneA), {Channel: "telegram", Address: "4242"}}, 1)
	testutil.Must(t, err)
	if ok, _ := members.HasAny(d, arisan.ID); ok {
		t.Fatal("has members before link")
	}
	testutil.Must(t, members.Link(d, arisan.ID, m.ID, 2))
	testutil.Must(t, members.Link(d, arisan.ID, m.ID, 3)) // twice is fine
	for _, c := range []struct {
		agent string
		s     members.Sender
		want  bool
	}{
		{arisan.ID, members.Sender{Channel: "whatsapp", ExternalID: phoneA}, true},
		{arisan.ID, members.Sender{Channel: "whatsapp", ExternalID: lidA, AltID: phoneA}, true},
		{arisan.ID, members.Sender{Channel: "telegram", ExternalID: "4242"}, true},
		{arisan.ID, members.Sender{Channel: "telegram", ExternalID: phoneA}, false},
		{arisan.ID, members.Sender{Channel: "whatsapp", ExternalID: phoneB}, false},
		{arisan.ID, members.Sender{Channel: "whatsapp"}, false},
		{arisan.ID, members.Sender{Channel: "web", ExternalID: phoneA}, false},
		{cs.ID, members.Sender{Channel: "whatsapp", ExternalID: phoneA}, false},
	} {
		got, err := members.IsMember(d, c.agent, c.s)
		testutil.Must(t, err)
		if got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
	if ok, _ := members.HasAny(d, arisan.ID); !ok {
		t.Fatal("no members after link")
	}
	if ok, _ := members.HasAny(d, cs.ID); ok {
		t.Fatal("cs has members")
	}
	list, err := members.ForAgent(d, arisan.ID)
	testutil.Must(t, err)
	if len(list) != 1 || list[0].ID != m.ID || len(list[0].AgentIDs) != 1 || len(list[0].Accounts) != 2 {
		t.Fatalf("%+v", list)
	}
	empty, err := members.ForAgent(d, cs.ID)
	testutil.Must(t, err)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("%#v", empty)
	}
}

func TestLearnPairsBothWays(t *testing.T) {
	d := testutil.DB(t)
	arisan := agent(t, d, "arisan")
	a, err := members.Create(d, "Ani", []members.AccountInput{wa(phoneA)}, 1)
	testutil.Must(t, err)
	b, err := members.Create(d, "Budi", []members.AccountInput{wa(lidB)}, 1)
	testutil.Must(t, err)
	testutil.Must(t, members.Link(d, arisan.ID, a.ID, 1))
	testutil.Must(t, members.Link(d, arisan.ID, b.ID, 1))

	// A typed phone learns its LID; a picked LID learns its phone.
	testutil.Must(t, members.Learn(d, members.Sender{Channel: "whatsapp", ExternalID: lidA, AltID: phoneA}))
	testutil.Must(t, members.Learn(d, members.Sender{Channel: "whatsapp", ExternalID: lidB, AltID: phoneB}))
	for _, id := range []string{lidA, phoneB} {
		ok, err := members.IsMember(d, arisan.ID, members.Sender{Channel: "whatsapp", ExternalID: id})
		testutil.Must(t, err)
		if !ok {
			t.Fatalf("%s not matched after learning", id)
		}
	}
	// No account for the sender: no write.
	testutil.Must(t, members.Learn(d, members.Sender{Channel: "whatsapp", ExternalID: "6281200000003@s.whatsapp.net", AltID: "100000000000003@lid"}))
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM member_accounts WHERE alt_id IS NOT NULL").Scan(&n))
	if n != 2 {
		t.Fatal(n)
	}

	// The same person added twice (phone on one, LID on another): no pairing.
	d2 := testutil.DB(t)
	_, err = members.Create(d2, "C1", []members.AccountInput{wa(phoneA)}, 1)
	testutil.Must(t, err)
	_, err = members.Create(d2, "C2", []members.AccountInput{wa(lidA)}, 1)
	testutil.Must(t, err)
	testutil.Must(t, members.Learn(d2, members.Sender{Channel: "whatsapp", ExternalID: lidA, AltID: phoneA}))
	testutil.Must(t, d2.QueryRow("SELECT count(*) FROM member_accounts WHERE alt_id IS NOT NULL").Scan(&n))
	if n != 0 {
		t.Fatal(n)
	}
}

func TestUpdateKeepsLearnedAltID(t *testing.T) {
	d := testutil.DB(t)
	m, err := members.Create(d, "Ani", []members.AccountInput{wa(phoneA), {Channel: "telegram", Address: "4242"}}, 1)
	testutil.Must(t, err)
	testutil.Must(t, members.Learn(d, members.Sender{Channel: "whatsapp", ExternalID: lidA, AltID: phoneA}))
	up, err := members.Update(d, m.ID, "Ani B", []members.AccountInput{wa("0812 0000 0001"), wa(phoneB)}, 5)
	testutil.Must(t, err)
	if up.Name != "Ani B" || up.UpdatedAt != 5 || len(up.Accounts) != 2 {
		t.Fatalf("%+v", up)
	}
	if up.Accounts[0].ExternalID != phoneA || up.Accounts[0].AltID == nil || *up.Accounts[0].AltID != lidA || up.Accounts[1].ExternalID != phoneB {
		t.Fatalf("%+v", up.Accounts)
	}
	// Its own account is not "taken by another member".
	_, err = members.Update(d, m.ID, "Ani B", []members.AccountInput{wa(lidA)}, 6)
	testutil.Must(t, err)
	_, err = members.Update(d, "mem_gone", "X", []members.AccountInput{wa(phoneB)}, 6)
	memberErr(t, err, "gone")
}

func TestLinkAndUnlink(t *testing.T) {
	d := testutil.DB(t)
	palui, err := agents.GetDefault(d, 1)
	testutil.Must(t, err)
	arisan := agent(t, d, "arisan")
	cs := agent(t, d, "cs")
	m, err := members.Create(d, "Ani", []members.AccountInput{wa(phoneA)}, 1)
	testutil.Must(t, err)
	memberErr(t, members.Link(d, palui.ID, m.ID, 1), "Palui is yours alone and has no members.")
	memberErr(t, members.Link(d, "agt_gone", m.ID, 1), "gone")
	memberErr(t, members.Link(d, arisan.ID, "mem_gone", 1), "gone")
	testutil.Must(t, members.Link(d, arisan.ID, m.ID, 1))
	testutil.Must(t, members.Link(d, cs.ID, m.ID, 2))

	all, err := members.All(d)
	testutil.Must(t, err)
	if len(all) != 1 || strings.Join(all[0].AgentIDs, ",") != arisan.ID+","+cs.ID {
		t.Fatalf("%+v", all)
	}

	deleted, err := members.Unlink(d, arisan.ID, m.ID)
	testutil.Must(t, err)
	if deleted {
		t.Fatal("deleted while still in cs")
	}
	deleted, err = members.Unlink(d, cs.ID, m.ID)
	testutil.Must(t, err)
	if !deleted {
		t.Fatal("kept after last agent")
	}
	var n int
	testutil.Must(t, d.QueryRow("SELECT (SELECT count(*) FROM members) + (SELECT count(*) FROM member_accounts) + (SELECT count(*) FROM agent_members)").Scan(&n))
	if n != 0 {
		t.Fatal(n)
	}
}

func TestLinkCapsMembersPerAgent(t *testing.T) {
	d := testutil.DB(t)
	arisan := agent(t, d, "arisan")
	var last string
	for i := range members.MaxPerAgent + 1 {
		m, err := members.Create(d, "M", []members.AccountInput{{Channel: "telegram", Address: strconv.Itoa(7000 + i)}}, 1)
		testutil.Must(t, err)
		last = m.ID
		if i < members.MaxPerAgent {
			testutil.Must(t, members.Link(d, arisan.ID, m.ID, 1))
		}
	}
	memberErr(t, members.Link(d, arisan.ID, last, 1), "at most")
}

// Member accounts live in their own table, so re-claiming the owner never
// touches them (the bug keeping them in identities would have).
func TestOwnerClaimsLeaveMemberAccounts(t *testing.T) {
	d := testutil.DB(t)
	_, err := members.Create(d, "Ani", []members.AccountInput{wa(phoneA), {Channel: "telegram", Address: "4242"}}, 1)
	testutil.Must(t, err)
	_, err = owner.SetWhatsAppOwner(d, ownerPhone, 2)
	testutil.Must(t, err)
	c, err := owner.CreateClaim(d, "telegram", 2)
	testutil.Must(t, err)
	ok, err := owner.ClaimOwner(d, owner.ClaimInput{Channel: "telegram", Nonce: c.Nonce, ExternalID: "9999"}, 3)
	testutil.Must(t, err)
	if !ok {
		t.Fatal("claim failed")
	}
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM member_accounts").Scan(&n))
	if n != 2 {
		t.Fatal(n)
	}
	cur, _, err := owner.GetOwner(d, "whatsapp")
	testutil.Must(t, err)
	if cur.ExternalID != ownerPhone+"@s.whatsapp.net" {
		t.Fatalf("%+v", cur)
	}
}

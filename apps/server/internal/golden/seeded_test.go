package golden_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type seeded struct {
	MasterKey     string `json:"masterKey"`
	Now           int64  `json:"now"`
	SchemaVersion string `json:"schemaVersion"`
	Golden        struct {
		ChatID           string `json:"chatId"`
		HookID           string `json:"hookId"`
		HookSlug         string `json:"hookSlug"`
		HookSecret       string `json:"hookSecret"`
		HookBody         string `json:"hookBody"`
		HookTimestamp    string `json:"hookTimestamp"`
		HookSignature    string `json:"hookSignature"`
		AttachmentID     string `json:"attachmentId"`
		AttachmentBase64 string `json:"attachmentBase64"`
	} `json:"golden"`
	Connectors []struct {
		ID      string          `json:"id"`
		Secrets json.RawMessage `json:"secrets"`
	} `json:"connectors"`
	Hooks []struct {
		ID     string `json:"id"`
		Slug   string `json:"slug"`
		Secret string `json:"secret"`
	} `json:"hooks"`
	Attachments []struct {
		ID     string `json:"id"`
		Sha256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"attachments"`
	Counts map[string]int `json:"counts"`
}

// openCopy opens a scratch copy of testdata/seeded.db, so the committed file never changes.
func openCopy(t *testing.T, opts db.OpenOptions) (*db.DB, seeded) {
	t.Helper()
	var s seeded
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "seeded.json"))
	testutil.Must(t, err)
	testutil.Must(t, json.Unmarshal(b, &s))
	src, err := os.Open(filepath.Join("..", "..", "testdata", "seeded.db"))
	testutil.Must(t, err)
	defer src.Close()
	path := filepath.Join(t.TempDir(), "palui.db")
	dst, err := os.Create(path)
	testutil.Must(t, err)
	_, err = io.Copy(dst, src)
	testutil.Must(t, err)
	testutil.Must(t, dst.Close())
	d, err := db.Open(path, opts)
	testutil.Must(t, err)
	t.Cleanup(func() { d.Close() })
	return d, s
}

func TestSeededDatabaseUsesTheDevMasterKey(t *testing.T) {
	_, s := openCopy(t, db.OpenOptions{NoMigrate: true})
	if s.MasterKey != config.DevMasterKey {
		t.Fatal("the goldens must use the dev master key")
	}
}

// sincePort lists the migrations added after seeded.db was frozen at the Go
// port, in order. Append each new migration here: the frozen database must
// report exactly these as missing and take them on open.
var sincePort = []string{"0016_mcp_access", "0017_custom_commands", "0018_members", "0019_save_media", "0020_chat_rules", "0021_agent_models", "0022_outbox_media", "0023_connector_agents"}

func TestSeededDatabaseReportsSchemaOK(t *testing.T) {
	d, s := openCopy(t, db.OpenOptions{NoMigrate: true})
	st, err := db.GetSchemaState(d)
	testutil.Must(t, err)
	if len(sincePort) == 0 && (!st.OK || st.Version != s.SchemaVersion) {
		t.Fatalf("%+v", st)
	}
	if len(sincePort) > 0 && (st.OK || st.Reason != "behind" || !slices.Equal(st.Missing, sincePort)) {
		t.Fatalf("the frozen database should miss exactly %v: %+v", sincePort, st)
	}
	// Opening with migrations applies only the new ones, on real old rows.
	d2, _ := openCopy(t, db.OpenOptions{})
	st, err = db.GetSchemaState(d2)
	testutil.Must(t, err)
	if !st.OK {
		t.Fatalf("after migrating: %+v", st)
	}
	var n int
	testutil.Must(t, d2.QueryRow("SELECT count(*) FROM _migrations").Scan(&n))
	if n != s.Counts["_migrations"]+len(sincePort) {
		t.Fatal(n)
	}
}

func TestSeededDatabaseDecryptsEveryConnectorSecret(t *testing.T) {
	d, s := openCopy(t, db.OpenOptions{})
	want := map[string]string{}
	for _, c := range s.Connectors {
		var buf bytes.Buffer
		testutil.Must(t, json.Compact(&buf, c.Secrets))
		want[c.ID] = buf.String()
	}
	all, err := connectors.List(d)
	testutil.Must(t, err)
	found := 0
	for _, row := range all {
		if row.Secrets == nil {
			continue
		}
		found++
		got, err := connectors.Secrets(row, s.MasterKey)
		if err != nil {
			t.Errorf("%s: %v", row.ID, err)
			continue
		}
		// Same keys, same order, same values as the TS decryptJson.
		if jsonx.Stringify(got) != want[row.ID] {
			t.Errorf("%s: Go %s, TS %s", row.ID, jsonx.Stringify(got), want[row.ID])
		}
		masked, err := connectors.MaskedSecrets(row, s.MasterKey)
		if err != nil || masked.Len() == 0 {
			t.Errorf("%s: masked %v", row.ID, err)
		}
		if row.ConfigHash != connectors.ConfigHash(row.Config, row.Secrets) {
			t.Errorf("%s: config hash differs from the TS one", row.ID)
		}
	}
	if found != len(s.Connectors) || found < 2 {
		t.Fatalf("decrypted %d connectors, TS listed %d", found, len(s.Connectors))
	}
}

func TestSeededDatabaseDecryptsEveryHookSecretAndVerifiesATSSignature(t *testing.T) {
	d, s := openCopy(t, db.OpenOptions{NoMigrate: true})
	if len(s.Hooks) == 0 {
		t.Fatal("no hooks")
	}
	for _, h := range s.Hooks {
		row, ok, err := hooks.GetBySlug(d, h.Slug)
		if err != nil || !ok {
			t.Fatalf("%s: %v", h.Slug, err)
		}
		plain, err := secret.Decrypt(row.Secret, s.MasterKey)
		if err != nil || plain != h.Secret {
			t.Errorf("%s: %v", h.ID, err)
		}
	}
	g := s.Golden
	if hooks.Sign(g.HookSecret, g.HookTimestamp, []byte(g.HookBody)) != g.HookSignature {
		t.Fatal("Go signature differs from the TS one")
	}
	_, fail, err := hooks.VerifyRequest(d, g.HookSlug, []byte(g.HookBody), hooks.Headers{Timestamp: &g.HookTimestamp, Signature: &g.HookSignature}, s.MasterKey, s.Now)
	testutil.Must(t, err)
	if fail != nil {
		t.Fatalf("%+v", fail)
	}
}

// Migrated first, as the server always opens it: rows written before
// 0019_save_media must read with the columns it added.
func TestSeededDatabaseDecryptsEveryAttachment(t *testing.T) {
	d, s := openCopy(t, db.OpenOptions{})
	if len(s.Attachments) == 0 {
		t.Fatal("no attachments")
	}
	for _, a := range s.Attachments {
		f, ok, err := attachments.Read(d, a.ID, s.MasterKey)
		if err != nil || !ok {
			t.Fatalf("%s: %v", a.ID, err)
		}
		sum := sha256.Sum256(f.Bytes)
		if hex.EncodeToString(sum[:]) != a.Sha256 || int64(len(f.Bytes)) != a.Size || f.Meta.Sha256 != a.Sha256 {
			t.Errorf("%s: content differs", a.ID)
		}
	}
	f, _, err := attachments.Read(d, s.Golden.AttachmentID, s.MasterKey)
	testutil.Must(t, err)
	if base64.StdEncoding.EncodeToString(f.Bytes) != s.Golden.AttachmentBase64 {
		t.Fatal("golden attachment differs")
	}
}

// Every row the M1 tables hold scans into the Go row types, once the
// migrations since the port have added their columns.
func TestSeededDatabaseScansWithTheGoRowTypes(t *testing.T) {
	d, s := openCopy(t, db.OpenOptions{})
	check := func(table string, n int, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", table, err)
			return
		}
		if n != s.Counts[table] {
			t.Errorf("%s: scanned %d rows, TS wrote %d", table, n, s.Counts[table])
		}
	}
	scanAll := func(table, cols string, scan func(db.Scanner) error) {
		rows, err := d.Query("SELECT " + cols + " FROM " + table)
		if err != nil {
			check(table, 0, err)
			return
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			if err := scan(rows); err != nil {
				check(table, n, err)
				return
			}
			n++
		}
		check(table, n, rows.Err())
	}
	wrap := func(f func(db.Scanner) error) func(db.Scanner) error { return f }
	scanAll("connectors", db.ConnectorCols, wrap(func(s db.Scanner) error { _, err := db.ScanConnector(s); return err }))
	scanAll("chats", db.ChatCols, wrap(func(s db.Scanner) error { _, err := db.ScanChat(s); return err }))
	scanAll("sessions", db.SessionCols, wrap(func(s db.Scanner) error { _, err := db.ScanSession(s); return err }))
	scanAll("messages", db.MessageCols, wrap(func(s db.Scanner) error {
		m, err := db.ScanMessage(s)
		if err == nil {
			_, err = m.Content.Parse()
		}
		return err
	}))
	scanAll("tool_calls", db.ToolCallCols, wrap(func(s db.Scanner) error { _, err := db.ScanToolCall(s); return err }))
	scanAll("pending_approvals", db.ApprovalCols, wrap(func(s db.Scanner) error { _, err := db.ScanApproval(s); return err }))
	scanAll("saved_attachments", db.AttachmentMetaCols, wrap(func(s db.Scanner) error { _, err := db.ScanAttachmentMeta(s); return err }))
	scanAll("outbox", db.OutboxCols, wrap(func(s db.Scanner) error { _, err := db.ScanOutbox(s); return err }))
	scanAll("events", db.EventCols, wrap(func(s db.Scanner) error { _, err := db.ScanEvent(s); return err }))
	scanAll("hooks", db.HookCols, wrap(func(s db.Scanner) error { _, err := db.ScanHook(s); return err }))
	scanAll("identities", db.IdentityCols, wrap(func(s db.Scanner) error { _, err := db.ScanIdentity(s); return err }))
	scanAll("instructions", db.InstructionCols, wrap(func(s db.Scanner) error { _, err := db.ScanInstruction(s); return err }))
	scanAll("preferences", db.PreferenceCols, wrap(func(s db.Scanner) error { _, err := db.ScanPreference(s); return err }))
	scanAll("agents", db.AgentCols, wrap(func(s db.Scanner) error { _, err := db.ScanAgent(s); return err }))
	scanAll("attachment_shares", db.ShareCols, wrap(func(s db.Scanner) error { _, err := db.ScanShare(s); return err }))

	// The typed settings read what the TS build wrote.
	all, err := settings.All(d)
	testutil.Must(t, err)
	if all.Timezone == "" || len(all.ModelRoles.Default) == 0 || all.Budgets.MaxSteps == 0 {
		t.Fatalf("%+v", all)
	}
}

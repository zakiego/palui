package deploynotice_test

// Port of deploy-notice.test.ts.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deploynotice"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/version"
)

var NOW = time.Date(2026, 11, 12, 2, 0, 0, 0, time.UTC).UnixMilli()

func setup(t *testing.T) (*db.DB, db.Chat, db.Chat) {
	d := testutil.DB(t)
	dm, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42", Title: "Zaki", ConnectorID: "tg-bot"}, NOW)
	testutil.Must(t, err)
	group, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120@g.us", Title: "Keluarga", Kind: "group"}, NOW)
	testutil.Must(t, err)
	return d, dm, group
}

func str(s string) *string { return &s }

func setStr(t *testing.T, d *db.DB, key, v string) {
	testutil.Must(t, settings.Set(d, key, str(v), NOW))
}

func getStr(t *testing.T, d *db.DB, key string) string {
	v, err := settings.Get[*string](d, key)
	testutil.Must(t, err)
	if v == nil {
		return "<nil>"
	}
	return *v
}

func on(t *testing.T, d *db.DB, chatID string, v bool) {
	testutil.Must(t, deploynotice.Set(d, chatID, v, NOW))
}

func outboxRows(t *testing.T, d *db.DB) []db.Outbox {
	rows, err := d.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY rowid")
	out, err := db.All(rows, err, db.ScanOutbox)
	testutil.Must(t, err)
	return out
}

func texts(t *testing.T, d *db.DB) []string {
	var out []string
	for _, r := range outboxRows(t, d) {
		out = append(out, r.Payload.Text)
	}
	return out
}

func announce(t *testing.T, d *db.DB, ver string, b *version.BuildInfo, now int64) deploynotice.Result {
	r, err := deploynotice.Announce(d, ver, b, now)
	testutil.Must(t, err)
	return r
}

func TestSetDeployNotice(t *testing.T) {
	d, dm, group := setup(t)
	if ok, _ := deploynotice.On(d, dm.ID); ok {
		t.Fatal("on by default")
	}
	on(t, d, dm.ID, true)
	on(t, d, group.ID, true)
	on(t, d, dm.ID, true)
	ids, _ := settings.Get[[]string](d, "deployNoticeChats")
	if strings.Join(ids, ",") != group.ID+","+dm.ID {
		t.Fatal(ids)
	}
	on(t, d, group.ID, false)
	if ok, _ := deploynotice.On(d, group.ID); ok {
		t.Fatal("group still on")
	}
	if ok, _ := deploynotice.On(d, dm.ID); !ok {
		t.Fatal("dm off")
	}
}

func TestAnnounce(t *testing.T) {
	t.Run("off by default, still records the version", func(t *testing.T) {
		d, _, _ := setup(t)
		setStr(t, d, "lastRunVersion", "0.7.0")
		if r := announce(t, d, "0.8.0", nil, NOW); r.Sent || r.Reason != "off" {
			t.Fatal(r)
		}
		if getStr(t, d, "lastRunVersion") != "0.8.0" || len(outboxRows(t, d)) != 0 {
			t.Fatal("recorded")
		}
	})
	t.Run("quiet on a fresh install", func(t *testing.T) {
		d, dm, _ := setup(t)
		on(t, d, dm.ID, true)
		if r := announce(t, d, "0.8.0", nil, NOW); r.Sent || r.Reason != "first-run" || getStr(t, d, "lastRunVersion") != "0.8.0" {
			t.Fatal(r)
		}
	})
	t.Run("sends once to every chat that has it on, through that chat's bot", func(t *testing.T) {
		d, dm, group := setup(t)
		setStr(t, d, "lastRunVersion", "0.7.0")
		on(t, d, dm.ID, true)
		on(t, d, group.ID, true)
		if r := announce(t, d, "0.8.0", nil, NOW); !r.Sent || len(r.Skipped) != 0 {
			t.Fatal(r)
		}
		rows := outboxRows(t, d)
		if len(rows) != 2 {
			t.Fatal(rows)
		}
		var toDM, toGroup db.Outbox
		for _, r := range rows {
			if r.ChatID == dm.ID {
				toDM = r
			} else {
				toGroup = r
			}
		}
		if toDM.Channel != "telegram" || *toDM.ConnectorID != "tg-bot" || toDM.Source != "deploy:0.8.0" || toDM.Status != "pending" {
			t.Fatalf("%+v", toDM)
		}
		if toDM.Payload.Text != deploynotice.Text(deploynotice.Change{Version: "0.8.0", PreviousVersion: "0.7.0"}) {
			t.Fatal(toDM.Payload.Text)
		}
		if toGroup.Channel != "whatsapp" || toGroup.ConnectorID != nil {
			t.Fatalf("%+v", toGroup)
		}
		if r := announce(t, d, "0.8.0", nil, NOW+1000); r.Sent || r.Reason != "unchanged" || len(outboxRows(t, d)) != 2 {
			t.Fatal(r)
		}
	})
	t.Run("skips a chat that was removed or switched off", func(t *testing.T) {
		d, dm, group := setup(t)
		setStr(t, d, "lastRunVersion", "0.7.0")
		on(t, d, dm.ID, true)
		on(t, d, group.ID, true)
		on(t, d, "chat_missing", true)
		_, err := d.Exec("UPDATE chats SET allowed = 0 WHERE id = ?", group.ID)
		testutil.Must(t, err)
		r := announce(t, d, "0.8.0", nil, NOW)
		if !r.Sent || strings.Join(r.ChatIDs, ",") != dm.ID || strings.Join(r.Skipped, ",") != group.ID+",chat_missing" {
			t.Fatal(r)
		}
		if rows := outboxRows(t, d); len(rows) != 1 || rows[0].ChatID != dm.ID {
			t.Fatal(rows)
		}
	})
}

const (
	A = "aaaaaaa1111111111111111111111111111111111"
	B = "bbbbbbb2222222222222222222222222222222222"
	C = "ccccccc3333333333333333333333333333333333"
)

func build(commit string, history ...version.HistoryEntry) *version.BuildInfo {
	return &version.BuildInfo{Commit: commit, History: history}
}

func TestAnnounceWithCommit(t *testing.T) {
	t.Run("adds the short commit to a version bump", func(t *testing.T) {
		d, dm, _ := setup(t)
		setStr(t, d, "lastRunVersion", "0.7.0")
		setStr(t, d, "lastRunCommit", A)
		on(t, d, dm.ID, true)
		if r := announce(t, d, "0.8.0", build(B), NOW); !r.Sent {
			t.Fatal(r)
		}
		if got := texts(t, d); strings.Join(got, "|") != "Palui sudah jalan di versi 0.8.0 (sebelumnya 0.7.0), commit bbbbbbb. Yang baru: https://palui.zakiego.com/docs/changelog" {
			t.Fatal(got)
		}
		if outboxRows(t, d)[0].Source != "deploy:0.8.0:bbbbbbb" || getStr(t, d, "lastRunCommit") != B {
			t.Fatal("source")
		}
	})
	t.Run("announces a new commit on the same version with the subjects since the last one", func(t *testing.T) {
		d, dm, _ := setup(t)
		setStr(t, d, "lastRunVersion", "0.8.0")
		setStr(t, d, "lastRunCommit", A)
		on(t, d, dm.ID, true)
		info := build(C, version.HistoryEntry{Sha: C, Subjects: []string{"fix(app): open a flow from anywhere", "feat(core): early answers"}}, version.HistoryEntry{Sha: B, Subjects: []string{"fix(core): reply on limits"}}, version.HistoryEntry{Sha: A, Subjects: []string{"chore(release): v0.8.0"}})
		if r := announce(t, d, "0.8.0", info, NOW); !r.Sent || strings.Join(r.ChatIDs, ",") != dm.ID {
			t.Fatal(r)
		}
		want := "Palui di-update ke commit ccccccc (sebelumnya aaaaaaa), versi 0.8.0\n\n- fix(app): open a flow from anywhere\n- feat(core): early answers\n- fix(core): reply on limits"
		if got := texts(t, d); len(got) != 1 || got[0] != want {
			t.Fatalf("%q", got)
		}
		if getStr(t, d, "lastRunCommit") != C || getStr(t, d, "lastRunVersion") != "0.8.0" {
			t.Fatal("recorded")
		}
	})
	t.Run("nothing on restarts with the same commit", func(t *testing.T) {
		d, dm, _ := setup(t)
		setStr(t, d, "lastRunVersion", "0.8.0")
		setStr(t, d, "lastRunCommit", A)
		on(t, d, dm.ID, true)
		if r := announce(t, d, "0.8.0", build(B), NOW); !r.Sent {
			t.Fatal(r)
		}
		for i := int64(1); i <= 3; i++ {
			if r := announce(t, d, "0.8.0", build(B), NOW+i*1000); r.Sent || r.Reason != "unchanged" {
				t.Fatal(r)
			}
		}
		if len(outboxRows(t, d)) != 1 {
			t.Fatal("sent twice")
		}
	})
	t.Run("names only the new commit when the previous one isn't known", func(t *testing.T) {
		d, dm, _ := setup(t)
		setStr(t, d, "lastRunVersion", "0.8.0")
		on(t, d, dm.ID, true)
		announce(t, d, "0.8.0", build(B, version.HistoryEntry{Sha: B, Subjects: []string{"feat(core): commit notices"}}, version.HistoryEntry{Sha: A, Subjects: []string{"older"}}), NOW)
		if got := texts(t, d); len(got) != 1 || got[0] != "Palui di-update ke commit bbbbbbb, versi 0.8.0\n\n- feat(core): commit notices" {
			t.Fatalf("%q", got)
		}
	})
	t.Run("caps a long list of subjects", func(t *testing.T) {
		var subjects []string
		for i := 1; i <= 11; i++ {
			subjects = append(subjects, fmt.Sprintf("commit %d", i))
		}
		text := deploynotice.Text(deploynotice.Change{Version: "0.8.0", PreviousVersion: "0.8.0", Commit: C, PreviousCommit: A, Subjects: subjects})
		lines := strings.Split(text, "\n")[2:]
		var want []string
		for _, s := range subjects[:8] {
			want = append(want, "- "+s)
		}
		want = append(want, "- dan 3 lainnya")
		if strings.Join(lines, "|") != strings.Join(want, "|") || strings.HasSuffix(text, ".") {
			t.Fatal(lines)
		}
	})
	t.Run("falls back to the version alone when the build has no commit", func(t *testing.T) {
		d, dm, _ := setup(t)
		setStr(t, d, "lastRunVersion", "0.8.0")
		setStr(t, d, "lastRunCommit", A)
		on(t, d, dm.ID, true)
		if r := announce(t, d, "0.8.0", nil, NOW); r.Sent || r.Reason != "unchanged" || getStr(t, d, "lastRunCommit") != A {
			t.Fatal(r)
		}
		if r := announce(t, d, "0.9.0", nil, NOW); !r.Sent {
			t.Fatal(r)
		}
		got := texts(t, d)
		if len(got) != 1 || got[0] != deploynotice.Text(deploynotice.Change{Version: "0.9.0", PreviousVersion: "0.8.0"}) || strings.Contains(got[0], "commit") {
			t.Fatal(got)
		}
		if r := announce(t, d, "0.9.0", build(A), NOW); r.Sent || r.Reason != "unchanged" {
			t.Fatal(r)
		}
	})
	t.Run("records the commit on a fresh install without sending", func(t *testing.T) {
		d, dm, _ := setup(t)
		on(t, d, dm.ID, true)
		if r := announce(t, d, "0.8.0", build(A), NOW); r.Sent || r.Reason != "first-run" || getStr(t, d, "lastRunCommit") != A {
			t.Fatal(r)
		}
	})
}

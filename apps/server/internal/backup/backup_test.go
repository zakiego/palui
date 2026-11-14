package backup_test

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/backup"
	"github.com/zakiego/palui/apps/server/internal/db"
)

func liveDB(t *testing.T, dir string) (string, *db.DB) {
	file := filepath.Join(dir, "palui.db")
	d, err := db.Open(file, db.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return file, d
}

func TestBackupLeavesOnlyTheFinishedSnapshot(t *testing.T) {
	dir := t.TempDir()
	src, d := liveDB(t, dir)
	if _, err := d.Exec("INSERT INTO events (at, kind, level, message) VALUES (1, 'k', 'info', 'kept')"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backups", "pre-deploy-20261110T000000Z.db.gz")
	res, err := backup.Backup(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dest) {
		t.Fatalf("left behind: %v", entries)
	}
	if res.Dest != dest || res.Bytes <= 0 || res.DBBytes <= 0 {
		t.Fatalf("result %+v", res)
	}
	// It's a gzip of a self-contained database that holds the row.
	f, _ := os.Open(dest)
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	out := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	conn, _ := sql.Open("sqlite", "file:"+out)
	defer conn.Close()
	var mode, msg string
	if err := conn.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal mode %q %v", mode, err)
	}
	if err := conn.QueryRow("SELECT message FROM events").Scan(&msg); err != nil || msg != "kept" {
		t.Fatalf("row %q %v", msg, err)
	}
}

func TestBackupNeverCreatesDestWhenTheGzipFails(t *testing.T) {
	dir := t.TempDir()
	src, _ := liveDB(t, dir)
	dest := filepath.Join(dir, "backups", "pre-deploy-20261110T000000Z.db.gz")
	// A directory where the gzip temp file goes makes the write fail.
	if err := os.MkdirAll(fmt.Sprintf("%s.tmp-%d", dest, os.Getpid()), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Backup(src, dest); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dest exists")
	}
}

func TestBackupRefusesAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	if _, err := backup.Backup(filepath.Join(dir, "nope.db"), filepath.Join(dir, "b.db.gz")); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.db")); !os.IsNotExist(err) {
		t.Fatal("created the source")
	}
}

func TestLatestBackupIgnoresHalfWrittenTempFiles(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	os.MkdirAll(backups, 0o755)
	done := filepath.Join(backups, "pre-deploy-20261109T000000Z.db.gz")
	os.WriteFile(done, []byte("x"), 0o644)
	old := time.UnixMilli(1_000_000)
	os.Chtimes(done, old, old)
	os.WriteFile(filepath.Join(backups, "pre-deploy-20261110T000000Z.db.gz.tmp-42"), []byte("partial"), 0o644)
	os.WriteFile(filepath.Join(backups, "pre-deploy-20261110T000000Z.db.gz.tmp-42-db"), []byte("partial"), 0o644)
	got := backup.LatestBackup(dir)
	if got == nil || got.File != "pre-deploy-20261109T000000Z.db.gz" || got.Bytes != 1 || got.At != 1_000_000 {
		t.Fatalf("got %+v", got)
	}
	if backup.LatestBackup(filepath.Join(dir, "none")) != nil {
		t.Fatal("expected nil without a backups dir")
	}
}

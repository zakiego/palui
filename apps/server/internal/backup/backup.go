// Package backup ports apps/app/src/server/backup.ts (a consistent, gzipped
// snapshot of the live database, safe while the server writes) and
// latestBackup from queries.ts.
//
// The snapshot is taken with VACUUM INTO on a plain read-only connection (no
// migrations), which SQLite runs inside one read transaction, so it is
// consistent like the online backup API better-sqlite3 used. The gzip lands in
// a temp file next to dest and is renamed into place, so a crash never leaves a
// half-written snapshot under the real name.
package backup

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// Result is BackupResult.
type Result struct {
	Dest    string `json:"dest"`
	Bytes   int64  `json:"bytes"`
	DBBytes int64  `json:"dbBytes"`
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Backup is backupDb.
func Backup(src, dest string) (res Result, err error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(src); err != nil {
		return Result{}, err
	}
	// Neither temp name ends in .db or .gz, so pre-deploy's prune glob and
	// LatestBackup never pick them up.
	tmp := fmt.Sprintf("%s.tmp-%d", dest, os.Getpid())
	snapFile := tmp + "-db"
	defer func() {
		for _, f := range []string{snapFile, snapFile + "-wal", snapFile + "-shm", snapFile + "-journal", tmp} {
			_ = os.RemoveAll(f)
		}
	}()
	live, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return Result{}, err
	}
	_, err = live.Exec("VACUUM INTO " + sqlQuote(snapFile))
	live.Close()
	if err != nil {
		return Result{}, err
	}
	snap, err := sql.Open("sqlite", "file:"+snapFile)
	if err != nil {
		return Result{}, err
	}
	err = func() error {
		defer snap.Close()
		// One self-contained file; the server turns WAL on again when it opens it.
		var mode string
		if err := snap.QueryRow("PRAGMA journal_mode = DELETE").Scan(&mode); err != nil {
			return err
		}
		var check string
		if err := snap.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
			return err
		}
		if check != "ok" {
			return fmt.Errorf("snapshot failed quick_check: %s", check)
		}
		return nil
	}()
	if err != nil {
		return Result{}, err
	}
	st, err := os.Stat(snapFile)
	if err != nil {
		return Result{}, err
	}
	if err := gzipFile(snapFile, tmp); err != nil {
		return Result{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Result{}, err
	}
	out, err := os.Stat(dest)
	if err != nil {
		return Result{}, err
	}
	return Result{Dest: dest, Bytes: out.Size(), DBBytes: st.Size()}, nil
}

func gzipFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	zw, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		out.Close()
		return err
	}
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Latest is one snapshot in $DATA_DIR/backups.
type Latest struct {
	File  string `json:"file"`
	Bytes int64  `json:"bytes"`
	At    int64  `json:"at"`
}

var backupName = regexp.MustCompile(`\.(db|gz|sqlite)(\.gz)?$`)

// LatestBackup is latestBackup: the newest snapshot by mtime, skipping the
// temp files a backup in progress writes.
func LatestBackup(dataDir string) *Latest {
	dir := filepath.Join(dataDir, "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var best *Latest
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, ".tmp-") || !backupName.MatchString(name) {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		at := st.ModTime().UnixMilli()
		if best == nil || at > best.At {
			best = &Latest{File: name, Bytes: st.Size(), At: at}
		}
	}
	return best
}

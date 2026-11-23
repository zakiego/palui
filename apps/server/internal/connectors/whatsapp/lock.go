package whatsapp

// One process per WhatsApp session store. Two sockets on the same credentials
// make WhatsApp evict both with conflicts, forever (whatsapp/lock.ts).
//
// The lock file is created with O_EXCL and holds the pid plus a random nonce.
// The holder touches the file's mtime every 30 s; a lock whose mtime is older
// than 2 minutes is stale. Pids say nothing across containers that share the
// volume (every container's process is pid 1), so the pid is only kept for
// the error message.
//
// After a crash the lock stays fresh for up to 2 minutes. The connector
// supervisor retries a failed start (5 s, 30 s, 120 s), so the channel comes
// back on its own.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors/channelutil"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

const (
	HeartbeatMs  = 30_000
	StaleAfterMs = 120_000
)

// LockInfo is the lock file's JSON.
type LockInfo struct {
	PID       int    `json:"pid"`
	Nonce     string `json:"nonce"`
	StartedAt int64  `json:"startedAt"`
}

// AuthLockedError: another process holds the session.
type AuthLockedError struct {
	PID  int
	Path string
}

func (e *AuthLockedError) Error() string {
	return fmt.Sprintf("WhatsApp session is in use by another Palui process (pid %d, lock %s). If that process is gone, the lock frees itself within 2 minutes.", e.PID, e.Path)
}

func (e *AuthLockedError) JSName() string { return "AuthLockedError" }

// Locks this process holds right now: path -> nonce.
var (
	heldMu sync.Mutex
	held   = map[string]string{}
)

func heldNonce(path string) (string, bool) {
	heldMu.Lock()
	defer heldMu.Unlock()
	n, ok := held[path]
	return n, ok
}

func readLock(path string) *LockInfo {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var info LockInfo
	if json.Unmarshal(v["pid"], &info.PID) != nil || json.Unmarshal(v["nonce"], &info.Nonce) != nil {
		return nil
	}
	_ = json.Unmarshal(v["startedAt"], &info.StartedAt)
	return &info
}

func mtimeOf(path string) (int64, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return st.ModTime().UnixMilli(), true
}

// LockOptions are the TS LockOptions.
type LockOptions struct {
	// 0: os.Getpid().
	PID int
	// Clock for the staleness check and startedAt.
	Now          func() int64
	HeartbeatMs  int64
	StaleAfterMs int64
	Timers       channelutil.Timers
}

// Lock is a held session lock.
type Lock struct {
	Path  string
	Nonce string
	now   func() int64
	beat  any
	tm    channelutil.Timers
}

func (l *Lock) ours() bool {
	n, ok := heldNonce(l.Path)
	if !ok || n != l.Nonce {
		return false
	}
	info := readLock(l.Path)
	return info != nil && info.Nonce == l.Nonce
}

// Touch moves the lock's mtime to now (the heartbeat).
func (l *Lock) Touch() {
	if !l.ours() {
		return
	}
	t := time.UnixMilli(l.now())
	// The file went away; the next acquire sorts it out.
	_ = os.Chtimes(l.Path, t, t)
}

// Release stops the heartbeat and deletes the file if it is still ours.
func (l *Lock) Release() {
	l.tm.ClearInterval(l.beat)
	heldMu.Lock()
	n, ok := held[l.Path]
	if !ok || n != l.Nonce {
		heldMu.Unlock()
		return
	}
	info := readLock(l.Path)
	delete(held, l.Path)
	heldMu.Unlock()
	if info != nil && info.Nonce == l.Nonce {
		_ = os.Remove(l.Path)
	}
}

func newNonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// AcquireLock is acquireAuthLock.
func AcquireLock(path string, o LockOptions) (*Lock, error) {
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.Now == nil {
		o.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if o.HeartbeatMs == 0 {
		o.HeartbeatMs = HeartbeatMs
	}
	if o.StaleAfterMs == 0 {
		o.StaleAfterMs = StaleAfterMs
	}
	if o.Timers == nil {
		o.Timers = channelutil.RealTimers{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for range 2 {
		nonce := newNonce()
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.WriteString(jsonx.Stringify(obj("pid", o.PID, "nonce", nonce, "startedAt", o.Now())))
			cerr := f.Close()
			if werr != nil {
				return nil, werr
			}
			if cerr != nil {
				return nil, cerr
			}
			heldMu.Lock()
			held[path] = nonce
			heldMu.Unlock()
			l := &Lock{Path: path, Nonce: nonce, now: o.Now, tm: o.Timers}
			l.beat = o.Timers.SetInterval(l.Touch, o.HeartbeatMs)
			return l, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info := readLock(path)
		mtime, hasMtime := mtimeOf(path)
		n, isHeld := heldNonce(path)
		ownedHere := info != nil && isHeld && n == info.Nonce
		fresh := hasMtime && o.Now()-mtime < o.StaleAfterMs
		if info != nil && (ownedHere || fresh) {
			return nil, &AuthLockedError{PID: info.PID, Path: path}
		}
		// An unreadable file that is still fresh may be a lock mid-write: leave it.
		if info == nil && fresh {
			return nil, &AuthLockedError{PID: -1, Path: path}
		}
		// Stale: no heartbeat for StaleAfterMs. Re-check it's the same file first.
		again := readLock(path)
		if (again == nil && info == nil) || (again != nil && info != nil && again.Nonce == info.Nonce) {
			_ = os.Remove(path)
		}
	}
	info := readLock(path)
	pid := -1
	if info != nil {
		pid = info.PID
	}
	return nil, &AuthLockedError{PID: pid, Path: path}
}

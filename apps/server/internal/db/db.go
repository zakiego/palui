// Package db ports packages/db/src: opening the SQLite file with the four
// pragmas, the embedded migrations and their `_migrations` table, schema state,
// and the row types of the tables (schema.go).
//
// Write discipline: one write connection (a pool capped at one) plus a read
// pool. Q.Exec, Q.WQuery and Q.WQueryRow go to the write connection, Q.Query and
// Q.QueryRow to the read pool. Inside Tx everything runs on the transaction.
// Never call the outer *DB from inside a Tx callback: the write pool has one
// connection, so that deadlocks.
package db

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Q is what every query helper takes: a *DB or the Tx a transaction hands out.
type Q interface {
	// Exec runs a write on the write connection.
	Exec(query string, args ...any) (sql.Result, error)
	// Query and QueryRow read from the read pool.
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	// WQuery and WQueryRow run on the write connection: INSERT/UPDATE ... RETURNING.
	WQuery(query string, args ...any) (*sql.Rows, error)
	WQueryRow(query string, args ...any) *sql.Row
	// Tx runs fn in one transaction. Nested on a Tx, fn runs inside the outer one.
	Tx(fn func(tx Q) error) error
}

// DB is an open palui.db.
type DB struct {
	// W is the write pool, capped at one connection.
	W *sql.DB
	// R is the read pool. For an in-memory database it is W.
	R    *sql.DB
	Path string
}

// OpenOptions mirror the TS openDb options.
type OpenOptions struct {
	// Skip running migrations (the TS runtime opened with migrate: false and
	// checked SchemaState instead).
	NoMigrate bool
}

// The four pragmas the TS build set on every connection. busy_timeout goes
// first so the WAL switch waits for a lock instead of failing.
var pragmas = []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(ON)"}

func dsn(file string, extra ...string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Add(extra[i], extra[i+1])
	}
	return "file:" + file + "?" + q.Encode()
}

// Open opens (and by default migrates) the database at file. ":memory:" gives a
// private in-memory database.
func Open(file string, opts OpenOptions) (*DB, error) {
	var d *DB
	if file == ":memory:" {
		// One connection that never closes, so the database lives as long as d.
		w, err := sql.Open("sqlite", dsn(":memory:"))
		if err != nil {
			return nil, err
		}
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		w.SetConnMaxLifetime(0)
		w.SetConnMaxIdleTime(0)
		d = &DB{W: w, R: w, Path: file}
	} else {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return nil, err
		}
		w, err := sql.Open("sqlite", dsn(file, "_txlock", "immediate"))
		if err != nil {
			return nil, err
		}
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		w.SetConnMaxLifetime(0)
		w.SetConnMaxIdleTime(0)
		r, err := sql.Open("sqlite", dsn(file))
		if err != nil {
			w.Close()
			return nil, err
		}
		r.SetMaxOpenConns(4)
		r.SetMaxIdleConns(4)
		d = &DB{W: w, R: r, Path: file}
	}
	if err := d.W.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	if !opts.NoMigrate {
		if err := Migrate(d, Migrations()); err != nil {
			d.Close()
			return nil, err
		}
	}
	return d, nil
}

// Memory is an in-memory database with every migration applied, for tests.
func Memory() *DB {
	d, err := Open(":memory:", OpenOptions{})
	if err != nil {
		panic(err)
	}
	return d
}

// Close closes both pools.
func (d *DB) Close() error {
	err := d.W.Close()
	if d.R != d.W {
		if e := d.R.Close(); err == nil {
			err = e
		}
	}
	return err
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) { return d.W.Exec(query, args...) }
func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.R.Query(query, args...)
}
func (d *DB) QueryRow(query string, args ...any) *sql.Row { return d.R.QueryRow(query, args...) }
func (d *DB) WQuery(query string, args ...any) (*sql.Rows, error) {
	return d.W.Query(query, args...)
}
func (d *DB) WQueryRow(query string, args ...any) *sql.Row { return d.W.QueryRow(query, args...) }

// Tx runs fn in a transaction on the write connection. An error or a panic
// from fn rolls back.
func (d *DB) Tx(fn func(tx Q) error) (err error) {
	tx, err := d.W.Begin()
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(&Tx{tx: tx}); err != nil {
		done = true
		_ = tx.Rollback()
		return err
	}
	done = true
	return tx.Commit()
}

// Tx is an open transaction.
type Tx struct{ tx *sql.Tx }

func (t *Tx) Exec(query string, args ...any) (sql.Result, error) { return t.tx.Exec(query, args...) }
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.tx.Query(query, args...)
}
func (t *Tx) QueryRow(query string, args ...any) *sql.Row { return t.tx.QueryRow(query, args...) }
func (t *Tx) WQuery(query string, args ...any) (*sql.Rows, error) {
	return t.tx.Query(query, args...)
}
func (t *Tx) WQueryRow(query string, args ...any) *sql.Row { return t.tx.QueryRow(query, args...) }
func (t *Tx) Tx(fn func(tx Q) error) error                 { return fn(t) }

// DataDir is $DATA_DIR or ./data.
func DataDir() string {
	if v := os.Getenv("DATA_DIR"); v != "" {
		return v
	}
	return "./data"
}

// Path is <dir>/palui.db.
func FilePath(dir string) string {
	return filepath.Join(dir, "palui.db")
}

// Changes is RowsAffected, 0 on error.
func Changes(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

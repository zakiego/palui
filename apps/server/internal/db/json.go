package db

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// JSON columns hold JSON.stringify text (drizzle's mode: "json"). Three helpers:
//   - JSON[T]: a NOT NULL column with a known shape.
//   - NullJSON[T]: a nullable column; SQL NULL is Valid == false.
//   - RawJSON: any shape, kept byte for byte (messages.content, settings.value).
// Writes go through jsonx.Marshal, so the text matches JSON.stringify.

func textOf(src any) ([]byte, error) {
	switch v := src.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("db: JSON column holds %T", src)
}

// JSON is a NOT NULL JSON text column.
type JSON[T any] struct{ V T }

func (j *JSON[T]) Scan(src any) error {
	b, err := textOf(src)
	if err != nil {
		return err
	}
	if b == nil {
		return errors.New("db: NULL in a NOT NULL JSON column")
	}
	var v T
	if p, ok := any(&v).(*any); ok {
		val, err := jsonx.Parse(b)
		if err != nil {
			return err
		}
		*p = val
	} else if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	j.V = v
	return nil
}

func (j JSON[T]) Value() (driver.Value, error) {
	b, err := jsonx.Marshal(j.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (j JSON[T]) MarshalJSON() ([]byte, error)  { return jsonx.Marshal(j.V) }
func (j *JSON[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.V) }

// NullJSON is a nullable JSON text column.
type NullJSON[T any] struct {
	V     T
	Valid bool
}

// SomeJSON wraps a value for a nullable JSON column.
func SomeJSON[T any](v T) NullJSON[T] { return NullJSON[T]{V: v, Valid: true} }

func (j *NullJSON[T]) Scan(src any) error {
	if src == nil {
		var zero T
		j.V, j.Valid = zero, false
		return nil
	}
	var inner JSON[T]
	if err := inner.Scan(src); err != nil {
		return err
	}
	j.V, j.Valid = inner.V, true
	return nil
}

func (j NullJSON[T]) Value() (driver.Value, error) {
	if !j.Valid {
		return nil, nil
	}
	return JSON[T]{V: j.V}.Value()
}

func (j NullJSON[T]) MarshalJSON() ([]byte, error) {
	if !j.Valid {
		return []byte("null"), nil
	}
	return jsonx.Marshal(j.V)
}

func (j *NullJSON[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		var zero T
		j.V, j.Valid = zero, false
		return nil
	}
	j.Valid = true
	return json.Unmarshal(b, &j.V)
}

// RawJSON is JSON text kept as is. Empty means SQL NULL.
type RawJSON []byte

func (r *RawJSON) Scan(src any) error {
	b, err := textOf(src)
	if err != nil {
		return err
	}
	if b == nil {
		*r = nil
		return nil
	}
	*r = append(RawJSON(nil), b...)
	return nil
}

func (r RawJSON) Value() (driver.Value, error) {
	if len(r) == 0 {
		return nil, nil
	}
	return string(r), nil
}

func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

func (r *RawJSON) UnmarshalJSON(b []byte) error {
	*r = append(RawJSON(nil), b...)
	return nil
}

// Raw makes a RawJSON from any value, written like JSON.stringify.
func Raw(v any) (RawJSON, error) {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return RawJSON(b), nil
}

// Parse reads the text with jsonx.Parse (objects keep their key order).
func (r RawJSON) Parse() (any, error) {
	if len(r) == 0 {
		return nil, nil
	}
	return jsonx.Parse(r)
}

// Decode unmarshals the text into v.
func (r RawJSON) Decode(v any) error {
	if len(r) == 0 {
		return nil
	}
	return json.Unmarshal(r, v)
}

// Scanner is a *sql.Row or *sql.Rows.
type Scanner interface{ Scan(dest ...any) error }

// One scans a single row. ok is false when there is no row.
func One[T any](row *sql.Row, scan func(Scanner) (T, error)) (v T, ok bool, err error) {
	v, err = scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		var zero T
		return zero, false, nil
	}
	if err != nil {
		var zero T
		return zero, false, err
	}
	return v, true, nil
}

// All scans every row and closes rows. No rows is an empty, non-nil slice,
// like drizzle's .all(), so a result written as JSON is [] and never null.
func All[T any](rows *sql.Rows, err error, scan func(Scanner) (T, error)) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Bool is the 0/1 SQLite stores for a boolean.
func Bool(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

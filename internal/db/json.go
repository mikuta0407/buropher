package db

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON は JSON 列 (SQLite: TEXT + json_valid, PG: JSONB) を Go の値として読み書きする型。
//
//	var f db.JSON[map[string]Filter]
//	q.Get(ctx, &f, "SELECT filters FROM queries WHERE id = ?", id)
//
// Value は JSON テキスト (string) を返す。pgx は string を JSONB にそのまま渡す。
type JSON[T any] struct {
	V T
}

// NewJSON は v を包んだ JSON を返す。
func NewJSON[T any](v T) JSON[T] { return JSON[T]{V: v} }

// Value は driver.Valuer の実装。
func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, fmt.Errorf("db: marshal json: %w", err)
	}
	return string(b), nil
}

// Scan は sql.Scanner の実装。NULL はゼロ値になる。
func (j *JSON[T]) Scan(src any) error {
	var zero T
	j.V = zero
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		return json.Unmarshal([]byte(v), &j.V)
	case []byte:
		return json.Unmarshal(v, &j.V)
	}
	return fmt.Errorf("db: cannot scan %T into JSON", src)
}

// RawJSON は未解釈の JSON テキスト。NULL は nil。
type RawJSON json.RawMessage

// Value は driver.Valuer の実装。
func (r RawJSON) Value() (driver.Value, error) {
	if r == nil {
		return nil, nil
	}
	if !json.Valid(r) {
		return nil, fmt.Errorf("db: invalid json")
	}
	return string(r), nil
}

// Scan は sql.Scanner の実装。
func (r *RawJSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r = nil
	case string:
		*r = RawJSON(v)
	case []byte:
		*r = append(RawJSON(nil), v...)
	default:
		return fmt.Errorf("db: cannot scan %T into RawJSON", src)
	}
	return nil
}

// MarshalJSON は json.Marshaler の実装。
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return r, nil
}

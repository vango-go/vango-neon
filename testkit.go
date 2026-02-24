package neon

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotMocked is returned when a TestDB method is called without a
// corresponding Func field set.
var ErrNotMocked = errors.New("neon.TestDB: method not mocked — set the corresponding Func field")

// TestDB is a mock DB implementation for unit tests.
type TestDB struct {
	ExecFunc     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryFunc    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRowFunc func(ctx context.Context, sql string, args ...any) pgx.Row
	BeginFunc    func(ctx context.Context) (pgx.Tx, error)
	BeginTxFunc  func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	PingFunc     func(ctx context.Context) error
	CloseFunc    func()
}

var _ DB = (*TestDB)(nil)

func (t *TestDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if t.ExecFunc != nil {
		return t.ExecFunc(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, ErrNotMocked
}

func (t *TestDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if t.QueryFunc != nil {
		return t.QueryFunc(ctx, sql, args...)
	}
	return &ErrRows{ErrValue: ErrNotMocked}, ErrNotMocked
}

func (t *TestDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.QueryRowFunc != nil {
		return t.QueryRowFunc(ctx, sql, args...)
	}
	return &ErrRow{Err: ErrNotMocked}
}

func (t *TestDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if t.BeginFunc != nil {
		return t.BeginFunc(ctx)
	}
	return nil, ErrNotMocked
}

func (t *TestDB) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	if t.BeginTxFunc != nil {
		return t.BeginTxFunc(ctx, txOptions)
	}
	return nil, ErrNotMocked
}

func (t *TestDB) Ping(ctx context.Context) error {
	if t.PingFunc != nil {
		return t.PingFunc(ctx)
	}
	return nil
}

func (t *TestDB) Close() {
	if t.CloseFunc != nil {
		t.CloseFunc()
	}
}

// ErrRow implements pgx.Row. Its Scan always returns Err.
type ErrRow struct {
	Err error
}

func (r *ErrRow) Scan(dest ...any) error {
	return r.Err
}

// NewRow returns a pgx.Row backed by the provided values.
func NewRow(values ...any) pgx.Row {
	return &valueRow{values: values}
}

type valueRow struct {
	values []any
}

func (r *valueRow) Scan(dest ...any) error {
	if len(dest) != len(r.values) {
		return fmt.Errorf("neon.valueRow: scan dest count %d != column count %d", len(dest), len(r.values))
	}

	for i, val := range r.values {
		if err := assignScanValue("neon.valueRow", i, dest[i], val); err != nil {
			return err
		}
	}

	return nil
}

// ErrRows implements pgx.Rows and always returns the configured error.
type ErrRows struct {
	// ErrValue is returned by Err(), Scan(), and Values().
	ErrValue error
}

func (r *ErrRows) Close()                                       {}
func (r *ErrRows) Err() error                                   { return r.ErrValue }
func (r *ErrRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *ErrRows) Conn() *pgx.Conn                              { return nil }
func (r *ErrRows) RawValues() [][]byte                          { return nil }
func (r *ErrRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *ErrRows) Next() bool                                   { return false }
func (r *ErrRows) Values() ([]any, error)                       { return nil, r.ErrValue }

func (r *ErrRows) Scan(dest ...any) error {
	if r.ErrValue != nil {
		return r.ErrValue
	}
	return fmt.Errorf("neon.ErrRows: Scan called with nil ErrValue")
}

// RowsBuilder builds pgx.Rows backed by in-memory rows.
type RowsBuilder struct {
	columns []string
	rows    [][]any
}

// NewRows creates a new RowsBuilder.
func NewRows(columns []string) *RowsBuilder {
	return &RowsBuilder{columns: columns}
}

// AddRow appends a row. It panics on arity mismatch.
func (b *RowsBuilder) AddRow(values ...any) *RowsBuilder {
	if len(values) != len(b.columns) {
		panic("neon.RowsBuilder: column count mismatch")
	}
	b.rows = append(b.rows, values)
	return b
}

// Build returns a pgx.Rows cursor for the builder data.
func (b *RowsBuilder) Build() pgx.Rows {
	return &fakeRows{
		columns: b.columns,
		data:    b.rows,
		idx:     -1,
	}
}

type fakeRows struct {
	columns []string
	data    [][]any
	idx     int
	closed  bool
	scanErr error
}

func (r *fakeRows) Close() {
	r.closed = true
}

func (r *fakeRows) Err() error {
	return r.scanErr
}

func (r *fakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (r *fakeRows) Conn() *pgx.Conn {
	return nil
}

func (r *fakeRows) RawValues() [][]byte {
	return nil
}

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	fields := make([]pgconn.FieldDescription, len(r.columns))
	for i, col := range r.columns {
		fields[i] = pgconn.FieldDescription{Name: col}
	}
	return fields
}

func (r *fakeRows) Next() bool {
	if r.closed {
		return false
	}

	r.idx++
	if r.idx >= len(r.data) {
		r.closed = true
		return false
	}
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.data) {
		return pgx.ErrNoRows
	}

	row := r.data[r.idx]
	if len(dest) != len(row) {
		err := fmt.Errorf("neon.fakeRows: scan dest count %d != column count %d", len(dest), len(row))
		r.scanErr = err
		return err
	}

	for i, val := range row {
		if err := assignScanValue("neon.fakeRows", i, dest[i], val); err != nil {
			r.scanErr = err
			return err
		}
	}

	return nil
}

func (r *fakeRows) Values() ([]any, error) {
	if r.idx < 0 || r.idx >= len(r.data) {
		return nil, pgx.ErrNoRows
	}
	return r.data[r.idx], nil
}

func assignScanValue(prefix string, idx int, dest any, val any) error {
	switch d := dest.(type) {
	case *string:
		v, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s: expected string at column %d, got %T", prefix, idx, val)
		}
		*d = v
	case *int:
		v, ok := val.(int)
		if !ok {
			return fmt.Errorf("%s: expected int at column %d, got %T", prefix, idx, val)
		}
		*d = v
	case *int64:
		v, ok := val.(int64)
		if !ok {
			return fmt.Errorf("%s: expected int64 at column %d, got %T", prefix, idx, val)
		}
		*d = v
	case *bool:
		v, ok := val.(bool)
		if !ok {
			return fmt.Errorf("%s: expected bool at column %d, got %T", prefix, idx, val)
		}
		*d = v
	case *float64:
		v, ok := val.(float64)
		if !ok {
			return fmt.Errorf("%s: expected float64 at column %d, got %T", prefix, idx, val)
		}
		*d = v
	case *any:
		*d = val
	default:
		return fmt.Errorf("%s: unsupported scan target type %T at column %d", prefix, dest, idx)
	}

	return nil
}

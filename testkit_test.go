package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestTestDB_UnsetMethodsReturnErrNotMocked(t *testing.T) {
	t.Parallel()

	db := &TestDB{}

	tag, err := db.Exec(context.Background(), "UPDATE x SET y=1")
	if !errors.Is(err, ErrNotMocked) {
		t.Fatalf("Exec error=%v, want %v", err, ErrNotMocked)
	}
	if tag.String() != "" {
		t.Fatalf("Exec tag=%q, want empty", tag.String())
	}

	rows, err := db.Query(context.Background(), "SELECT 1")
	if !errors.Is(err, ErrNotMocked) {
		t.Fatalf("Query error=%v, want %v", err, ErrNotMocked)
	}
	if rows == nil {
		t.Fatal("Query returned nil rows")
	}
	if !errors.Is(rows.Err(), ErrNotMocked) {
		t.Fatalf("rows.Err()=%v, want %v", rows.Err(), ErrNotMocked)
	}
	if scanErr := rows.Scan(new(any)); !errors.Is(scanErr, ErrNotMocked) {
		t.Fatalf("rows.Scan error=%v, want %v", scanErr, ErrNotMocked)
	}

	row := db.QueryRow(context.Background(), "SELECT 1")
	if row == nil {
		t.Fatal("QueryRow returned nil")
	}
	if err := row.Scan(new(any)); !errors.Is(err, ErrNotMocked) {
		t.Fatalf("QueryRow.Scan error=%v, want %v", err, ErrNotMocked)
	}

	tx, err := db.Begin(context.Background())
	if tx != nil {
		t.Fatal("Begin returned non-nil tx")
	}
	if !errors.Is(err, ErrNotMocked) {
		t.Fatalf("Begin error=%v, want %v", err, ErrNotMocked)
	}

	tx, err = db.BeginTx(context.Background(), pgx.TxOptions{})
	if tx != nil {
		t.Fatal("BeginTx returned non-nil tx")
	}
	if !errors.Is(err, ErrNotMocked) {
		t.Fatalf("BeginTx error=%v, want %v", err, ErrNotMocked)
	}

	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping error=%v, want nil", err)
	}

	db.Close()
}

func TestTestDB_UsesConfiguredFuncs(t *testing.T) {
	t.Parallel()

	wantTag := pgconn.NewCommandTag("INSERT 0 1")
	wantRows := NewRows([]string{"value"}).AddRow("ok").Build()
	wantRow := NewRow("single")
	wantTx := &txStub{}
	pingErr := errors.New("ping boom")

	calledExec := false
	calledQuery := false
	calledQueryRow := false
	calledBegin := false
	calledBeginTx := false
	calledPing := false
	calledClose := false

	db := &TestDB{
		ExecFunc: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			calledExec = true
			if sql != "exec-sql" {
				t.Fatalf("Exec sql=%q, want %q", sql, "exec-sql")
			}
			if len(args) != 1 || args[0] != 7 {
				t.Fatalf("Exec args=%v, want [7]", args)
			}
			return wantTag, nil
		},
		QueryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			calledQuery = true
			if sql != "query-sql" {
				t.Fatalf("Query sql=%q, want %q", sql, "query-sql")
			}
			if len(args) != 1 || args[0] != "arg" {
				t.Fatalf("Query args=%v, want [arg]", args)
			}
			return wantRows, nil
		},
		QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
			calledQueryRow = true
			if sql != "queryrow-sql" {
				t.Fatalf("QueryRow sql=%q, want %q", sql, "queryrow-sql")
			}
			if len(args) != 1 || args[0] != true {
				t.Fatalf("QueryRow args=%v, want [true]", args)
			}
			return wantRow
		},
		BeginFunc: func(ctx context.Context) (pgx.Tx, error) {
			calledBegin = true
			return wantTx, nil
		},
		BeginTxFunc: func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
			calledBeginTx = true
			if opts.IsoLevel != pgx.Serializable {
				t.Fatalf("BeginTx IsoLevel=%v, want %v", opts.IsoLevel, pgx.Serializable)
			}
			return wantTx, nil
		},
		PingFunc: func(ctx context.Context) error {
			calledPing = true
			return pingErr
		},
		CloseFunc: func() {
			calledClose = true
		},
	}

	tag, err := db.Exec(context.Background(), "exec-sql", 7)
	if err != nil {
		t.Fatalf("Exec error=%v", err)
	}
	if tag.String() != wantTag.String() {
		t.Fatalf("Exec tag=%q, want %q", tag.String(), wantTag.String())
	}

	rows, err := db.Query(context.Background(), "query-sql", "arg")
	if err != nil {
		t.Fatalf("Query error=%v", err)
	}
	if rows != wantRows {
		t.Fatal("Query returned unexpected rows instance")
	}

	row := db.QueryRow(context.Background(), "queryrow-sql", true)
	if row != wantRow {
		t.Fatal("QueryRow returned unexpected row instance")
	}

	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin error=%v", err)
	}
	if tx != wantTx {
		t.Fatal("Begin returned unexpected tx")
	}

	tx, err = db.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("BeginTx error=%v", err)
	}
	if tx != wantTx {
		t.Fatal("BeginTx returned unexpected tx")
	}

	err = db.Ping(context.Background())
	if !errors.Is(err, pingErr) {
		t.Fatalf("Ping error=%v, want %v", err, pingErr)
	}

	db.Close()

	if !calledExec || !calledQuery || !calledQueryRow || !calledBegin || !calledBeginTx || !calledPing || !calledClose {
		t.Fatalf("expected all configured funcs to be called, got exec=%v query=%v queryRow=%v begin=%v beginTx=%v ping=%v close=%v",
			calledExec, calledQuery, calledQueryRow, calledBegin, calledBeginTx, calledPing, calledClose)
	}
}

func TestErrRow_ScanReturnsStoredError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("row error")
	err := (&ErrRow{Err: sentinel}).Scan(new(any))
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want %v", err, sentinel)
	}
}

func TestNewRow_ScanSupportedTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		row    pgx.Row
		assert func(t *testing.T, row pgx.Row)
	}{
		{
			name: "all-supported-types",
			row:  NewRow("str", int(3), int64(4), true, 5.5, "anything"),
			assert: func(t *testing.T, row pgx.Row) {
				t.Helper()
				var s string
				var i int
				var i64 int64
				var b bool
				var f float64
				var a any
				if err := row.Scan(&s, &i, &i64, &b, &f, &a); err != nil {
					t.Fatalf("Scan error=%v", err)
				}
				if s != "str" || i != 3 || i64 != 4 || !b || f != 5.5 || a != "anything" {
					t.Fatalf("unexpected scanned values: s=%q i=%d i64=%d b=%v f=%v a=%v", s, i, i64, b, f, a)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.row)
		})
	}
}

func TestNewRow_ScanArityMismatch(t *testing.T) {
	t.Parallel()

	err := NewRow("a", "b").Scan(new(string))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "scan dest count 1 != column count 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRow_ScanTypeMismatch(t *testing.T) {
	t.Parallel()

	var got int
	err := NewRow("not-int").Scan(&got)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "expected int at column 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRow_ScanUnsupportedDestType(t *testing.T) {
	t.Parallel()

	var got uint64
	err := NewRow(1).Scan(&got)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported scan target type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErrRows_MethodContract(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("rows error")
	r := &ErrRows{ErrValue: sentinel}

	r.Close()

	if !errors.Is(r.Err(), sentinel) {
		t.Fatalf("Err()=%v, want %v", r.Err(), sentinel)
	}
	if r.Next() {
		t.Fatal("Next()=true, want false")
	}
	vals, err := r.Values()
	if vals != nil {
		t.Fatalf("Values=%v, want nil", vals)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Values error=%v, want %v", err, sentinel)
	}
	if err := r.Scan(new(any)); !errors.Is(err, sentinel) {
		t.Fatalf("Scan error=%v, want %v", err, sentinel)
	}
	if fds := r.FieldDescriptions(); fds != nil {
		t.Fatalf("FieldDescriptions=%v, want nil", fds)
	}
	if raw := r.RawValues(); raw != nil {
		t.Fatalf("RawValues=%v, want nil", raw)
	}
	if conn := r.Conn(); conn != nil {
		t.Fatalf("Conn=%v, want nil", conn)
	}
	if tag := r.CommandTag(); tag.String() != "" {
		t.Fatalf("CommandTag=%q, want empty", tag.String())
	}
}

func TestErrRows_ScanNilErrValue(t *testing.T) {
	t.Parallel()

	err := (&ErrRows{}).Scan(new(any))
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "neon.ErrRows: Scan called with nil ErrValue"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
}

func TestRowsBuilder_BuildAndIterate(t *testing.T) {
	t.Parallel()

	rows := NewRows([]string{"id", "name", "active"}).
		AddRow(1, "Alice", true).
		AddRow(2, "Bob", false).
		Build()

	fds := rows.FieldDescriptions()
	if len(fds) != 3 {
		t.Fatalf("field descriptions len=%d, want 3", len(fds))
	}
	if fds[0].Name != "id" || fds[1].Name != "name" || fds[2].Name != "active" {
		t.Fatalf("unexpected field names: %q, %q, %q", fds[0].Name, fds[1].Name, fds[2].Name)
	}

	type gotRow struct {
		id     int
		name   string
		active bool
	}
	var got []gotRow

	for rows.Next() {
		var id int
		var name string
		var active bool
		if err := rows.Scan(&id, &name, &active); err != nil {
			t.Fatalf("Scan error=%v", err)
		}
		vals, err := rows.Values()
		if err != nil {
			t.Fatalf("Values error=%v", err)
		}
		if len(vals) != 3 {
			t.Fatalf("Values len=%d, want 3", len(vals))
		}
		got = append(got, gotRow{id: id, name: name, active: active})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Err()=%v, want nil", err)
	}

	if len(got) != 2 {
		t.Fatalf("rows read=%d, want 2", len(got))
	}
	if got[0] != (gotRow{id: 1, name: "Alice", active: true}) {
		t.Fatalf("row0=%+v, want %+v", got[0], gotRow{id: 1, name: "Alice", active: true})
	}
	if got[1] != (gotRow{id: 2, name: "Bob", active: false}) {
		t.Fatalf("row1=%+v, want %+v", got[1], gotRow{id: 2, name: "Bob", active: false})
	}
}

func TestRowsBuilder_AddRowPanicsOnColumnMismatch(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic type=%T, want string", r)
		}
		if got, want := msg, "neon.RowsBuilder: column count mismatch"; got != want {
			t.Fatalf("panic=%q, want %q", got, want)
		}
	}()

	NewRows([]string{"id", "name"}).AddRow(1)
}

func TestRowsBuilder_ScanInvalidCursorReturnsErrNoRows(t *testing.T) {
	t.Parallel()

	rows := NewRows([]string{"id"}).AddRow(1).Build()

	var id int
	if err := rows.Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Scan before Next error=%v, want %v", err, pgx.ErrNoRows)
	}

	if !rows.Next() {
		t.Fatal("expected first row")
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("Scan error=%v", err)
	}
	if rows.Next() {
		t.Fatal("unexpected extra row")
	}
	if err := rows.Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Scan after exhausted error=%v, want %v", err, pgx.ErrNoRows)
	}
}

func TestRowsBuilder_ValuesInvalidCursorReturnsErrNoRows(t *testing.T) {
	t.Parallel()

	rows := NewRows([]string{"id"}).AddRow(1).Build()

	if vals, err := rows.Values(); !errors.Is(err, pgx.ErrNoRows) || vals != nil {
		t.Fatalf("Values before Next vals=%v err=%v, want nil/%v", vals, err, pgx.ErrNoRows)
	}

	if !rows.Next() {
		t.Fatal("expected first row")
	}
	if vals, err := rows.Values(); err != nil || len(vals) != 1 || vals[0] != 1 {
		t.Fatalf("Values during row vals=%v err=%v, want [1]/nil", vals, err)
	}

	if rows.Next() {
		t.Fatal("unexpected extra row")
	}
	if vals, err := rows.Values(); !errors.Is(err, pgx.ErrNoRows) || vals != nil {
		t.Fatalf("Values after exhausted vals=%v err=%v, want nil/%v", vals, err, pgx.ErrNoRows)
	}
}

func TestRowsBuilder_CloseStopsIteration(t *testing.T) {
	t.Parallel()

	rows := NewRows([]string{"id"}).AddRow(1).AddRow(2).Build()
	rows.Close()
	if rows.Next() {
		t.Fatal("Next() after Close should be false")
	}
}

func TestRowsBuilder_ScanTypeMismatchAndUnsupportedDest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dest    func() []any
		wantMsg string
	}{
		{
			name: "type-mismatch",
			dest: func() []any {
				v := 0
				return []any{&v}
			},
			wantMsg: "expected int at column 0",
		},
		{
			name: "unsupported-dest",
			dest: func() []any {
				v := uint64(0)
				return []any{&v}
			},
			wantMsg: "unsupported scan target type",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows := NewRows([]string{"v"}).AddRow("abc").Build()
			if !rows.Next() {
				t.Fatal("expected first row")
			}
			dest := tc.dest()
			err := rows.Scan(dest...)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error=%q, want substring %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

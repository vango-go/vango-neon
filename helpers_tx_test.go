package neon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type txDBStub struct {
	beginTxFunc func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func (d *txDBStub) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec call")
}

func (d *txDBStub) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("unexpected Query call")
}

func (d *txDBStub) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	panic("unexpected QueryRow call")
}

func (d *txDBStub) Begin(_ context.Context) (pgx.Tx, error) {
	panic("unexpected Begin call")
}

func (d *txDBStub) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	if d.beginTxFunc == nil {
		panic("beginTxFunc not set")
	}
	return d.beginTxFunc(ctx, opts)
}

func (d *txDBStub) Ping(_ context.Context) error {
	panic("unexpected Ping call")
}

func (d *txDBStub) Close() {}

type txStub struct {
	commitCalls          int
	rollbackCalls        int
	rollbackCtx          context.Context
	rollbackCtxErrAtCall error
	commitErr            error
	rollbackErr          error
}

func (t *txStub) Begin(_ context.Context) (pgx.Tx, error) { panic("unexpected Begin call") }

func (t *txStub) Commit(_ context.Context) error {
	t.commitCalls++
	return t.commitErr
}

func (t *txStub) Rollback(ctx context.Context) error {
	t.rollbackCalls++
	t.rollbackCtx = ctx
	t.rollbackCtxErrAtCall = ctx.Err()
	return t.rollbackErr
}

func (t *txStub) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	panic("unexpected CopyFrom call")
}

func (t *txStub) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	panic("unexpected SendBatch call")
}

func (t *txStub) LargeObjects() pgx.LargeObjects { panic("unexpected LargeObjects call") }

func (t *txStub) Prepare(_ context.Context, _ string, _ string) (*pgconn.StatementDescription, error) {
	panic("unexpected Prepare call")
}

func (t *txStub) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec call")
}

func (t *txStub) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("unexpected Query call")
}

func (t *txStub) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	panic("unexpected QueryRow call")
}

func (t *txStub) Conn() *pgx.Conn { return nil }

func TestWithTx_CommitsOnSuccess(t *testing.T) {
	t.Parallel()

	tx := &txStub{}
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	err := WithTx(context.Background(), db, pgx.TxOptions{}, func(_ pgx.Tx) error {
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx() error = %v", err)
	}
	if tx.commitCalls != 1 {
		t.Fatalf("commitCalls=%d, want 1", tx.commitCalls)
	}
	if tx.rollbackCalls != 0 {
		t.Fatalf("rollbackCalls=%d, want 0", tx.rollbackCalls)
	}
}

func TestWithTx_RollsBackOnFunctionError(t *testing.T) {
	t.Parallel()

	const ctxKey = "request-id"
	inputCtx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey, "abc-123"))
	defer cancel()

	tx := &txStub{}
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	start := time.Now()
	appErr := errors.New("app failure")
	err := WithTx(inputCtx, db, pgx.TxOptions{}, func(_ pgx.Tx) error {
		cancel()
		return appErr
	})
	if !errors.Is(err, appErr) {
		t.Fatalf("error=%v, want %v", err, appErr)
	}
	if tx.commitCalls != 0 {
		t.Fatalf("commitCalls=%d, want 0", tx.commitCalls)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollbackCalls=%d, want 1", tx.rollbackCalls)
	}
	if tx.rollbackCtx == nil {
		t.Fatal("rollback context was not recorded")
	}
	if tx.rollbackCtx.Value(ctxKey) != nil {
		t.Fatal("rollback context unexpectedly inherited input context values")
	}
	if tx.rollbackCtxErrAtCall != nil {
		t.Fatalf("rollback context should not be canceled by input ctx at rollback time, got %v", tx.rollbackCtxErrAtCall)
	}
	deadline, ok := tx.rollbackCtx.Deadline()
	if !ok {
		t.Fatal("rollback context missing deadline")
	}
	min := start.Add(defaultRollbackTimeout - 2*time.Second)
	max := start.Add(defaultRollbackTimeout + 2*time.Second)
	if deadline.Before(min) || deadline.After(max) {
		t.Fatalf("rollback deadline=%v outside [%v, %v]", deadline, min, max)
	}
}

func TestWithTx_RollsBackAndRepanicsOnPanic(t *testing.T) {
	t.Parallel()

	tx := &txStub{}
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	panicValue := "boom"
	defer func() {
		r := recover()
		if r != panicValue {
			t.Fatalf("panic=%v, want %v", r, panicValue)
		}
		if tx.rollbackCalls != 1 {
			t.Fatalf("rollbackCalls=%d, want 1", tx.rollbackCalls)
		}
	}()

	_ = WithTx(context.Background(), db, pgx.TxOptions{}, func(_ pgx.Tx) error {
		panic(panicValue)
	})
}

func TestWithTx_WrapsBeginFailureAsSafeError(t *testing.T) {
	t.Parallel()

	beginErr := errors.New("begin failed for postgresql://user:supersecret@db.example.com/neondb")
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return nil, beginErr
		},
	}

	err := WithTx(context.Background(), db, pgx.TxOptions{}, func(_ pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	assertSafeErrorWraps(t, err, beginErr)
	if got, want := err.Error(), "neon: begin tx failed"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestWithTx_WrapsCommitFailureAsSafeError(t *testing.T) {
	t.Parallel()

	commitErr := errors.New("commit failed for postgresql://user:supersecret@db.example.com/neondb")
	tx := &txStub{commitErr: commitErr}
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	err := WithTx(context.Background(), db, pgx.TxOptions{}, func(_ pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	assertSafeErrorWraps(t, err, commitErr)
	if got, want := err.Error(), "neon: commit tx failed"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollbackCalls=%d, want 1", tx.rollbackCalls)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestWithTx_RollbackFailureDoesNotReplaceOriginalError(t *testing.T) {
	t.Parallel()

	rollbackErr := errors.New("rollback failed")
	appErr := errors.New("application error")
	tx := &txStub{rollbackErr: rollbackErr}
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	err := WithTx(context.Background(), db, pgx.TxOptions{}, func(_ pgx.Tx) error {
		return appErr
	})
	if !errors.Is(err, appErr) {
		t.Fatalf("error=%v, want %v", err, appErr)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollbackCalls=%d, want 1", tx.rollbackCalls)
	}
}

func TestWithTx_CommitFailureStillPreservesCommitErrorWhenRollbackFails(t *testing.T) {
	t.Parallel()

	commitErr := errors.New("commit failed")
	rollbackErr := errors.New("rollback failed")
	tx := &txStub{commitErr: commitErr, rollbackErr: rollbackErr}
	db := &txDBStub{
		beginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	err := WithTx(context.Background(), db, pgx.TxOptions{}, func(_ pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	assertSafeErrorWraps(t, err, commitErr)
	if got, want := err.Error(), "neon: commit tx failed"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollbackCalls=%d, want 1", tx.rollbackCalls)
	}
}

package neon

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB defines the contract for database access in Vango applications.
//
// All methods require context.Context. This ensures cancellation propagates
// through Vango Resources and Actions to in-flight database operations:
// when a component unmounts or an Action is superseded, its context is
// canceled and pgx will abort the active query/connection attempt when
// possible.
//
// Use this interface in route dependencies and service-layer constructors.
// Prefer depending on DB rather than *Pool so application code remains testable
// (via TestDB) and decoupled from pool operational concerns.
//
// Operational/pool management methods (Stats, Acquire, config knobs) are
// intentionally not part of this contract; they belong on the concrete Pool
// type. Close is included to support graceful shutdown through the interface.
type DB interface {
	// Exec executes a query that does not return rows.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)

	// Query executes a query that returns rows, typically a SELECT.
	// The caller must close the returned Rows when done (use defer rows.Close()).
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)

	// QueryRow executes a query expected to return at most one row.
	// If no rows match, row.Scan() returns pgx.ErrNoRows.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row

	// Begin starts a transaction with default options.
	// The caller must call tx.Commit() or tx.Rollback().
	// Prefer WithTx for rollback-on-error semantics.
	Begin(ctx context.Context) (pgx.Tx, error)

	// BeginTx starts a transaction with explicit options.
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)

	// Ping verifies connectivity.
	Ping(ctx context.Context) error

	// Close releases all pool resources. Call once during graceful shutdown.
	Close()
}

package neon

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB defines the contract for database access in Vango applications.
type DB interface {
	// Exec executes a query that does not return rows.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)

	// Query executes a query that returns rows. Callers must close the rows.
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)

	// QueryRow executes a query expected to return at most one row.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row

	// Begin starts a transaction with default options.
	Begin(ctx context.Context) (pgx.Tx, error)

	// BeginTx starts a transaction with explicit options.
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)

	// Ping verifies connectivity.
	Ping(ctx context.Context) error

	// Close releases pool resources.
	Close()
}

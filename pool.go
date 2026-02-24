package neon

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the concrete implementation of DB backed by pgxpool.
// It intentionally wraps (does not embed) *pgxpool.Pool.
type Pool struct {
	pool      *pgxpool.Pool
	directURL string
}

var _ DB = (*Pool)(nil)

// DirectURL returns the resolved direct (non-pooled) URL.
// It contains credentials and must be treated as secret material.
func (p *Pool) DirectURL() string {
	return p.directURL
}

// Stat returns a snapshot of pool statistics.
func (p *Pool) Stat() *pgxpool.Stat {
	return p.pool.Stat()
}

func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

func (p *Pool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

func (p *Pool) Begin(ctx context.Context) (pgx.Tx, error) {
	return p.pool.Begin(ctx)
}

func (p *Pool) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	return p.pool.BeginTx(ctx, txOptions)
}

func (p *Pool) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *Pool) Close() {
	p.pool.Close()
}

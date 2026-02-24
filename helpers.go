package neon

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultRollbackTimeout = 5 * time.Second

// HealthStatus is the response type for health check endpoints.
type HealthStatus struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// HealthCheck verifies database connectivity and returns a status suitable for
// health check API endpoints.
func HealthCheck(ctx context.Context, db DB) (*HealthStatus, error) {
	if err := db.Ping(ctx); err != nil {
		return nil, &SafeError{msg: "neon: health check failed", cause: err}
	}

	return &HealthStatus{Status: "ok", Database: "neon"}, nil
}

// WithTx executes fn within a transaction. If fn returns an error or panics,
// the transaction is rolled back. Otherwise, it is committed.
func WithTx(ctx context.Context, db DB, opts pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return &SafeError{msg: "neon: begin tx failed", cause: err}
	}

	rollbackCtx, cancelRollback := context.WithTimeout(context.Background(), defaultRollbackTimeout)
	defer cancelRollback()

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(rollbackCtx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	err = fn(tx)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return &SafeError{msg: "neon: commit tx failed", cause: err}
	}

	return nil
}

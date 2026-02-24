//go:build integration

package neon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestIntegration_NeonE2E(t *testing.T) {
	rootT := t
	pooledURL, directURL := requireIntegrationEnv(t)
	schema := integrationSchemaName(t)
	table := qualifiedTable(schema, "items")

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelSetup()

	directConn, err := pgx.Connect(setupCtx, directURL)
	mustNoErr(t, err, "connect direct setup")
	defer directConn.Close(context.Background())

	_, err = directConn.Exec(setupCtx, fmt.Sprintf("CREATE SCHEMA %s", quoteIdent(schema)))
	mustNoErr(t, err, "create schema")

	_, err = directConn.Exec(setupCtx, fmt.Sprintf(`
CREATE TABLE %s (
	id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	qty INTEGER NOT NULL DEFAULT 0,
	note TEXT
)`, table))
	mustNoErr(t, err, "create table")

	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()

		cleanupConn, err := pgx.Connect(cleanupCtx, directURL)
		if err != nil {
			t.Errorf("cleanup connect failed: %s", sanitizeErrorMessage(err))
			return
		}
		defer cleanupConn.Close(context.Background())

		if _, err := cleanupConn.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA %s CASCADE", quoteIdent(schema))); err != nil {
			t.Errorf("cleanup drop schema failed: %s", sanitizeErrorMessage(err))
		}
	})

	var pooledPool *Pool

	t.Run("connect_pooled_and_healthcheck", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		pool, err := Connect(ctx, Config{
			ConnectionString: pooledURL,
			DirectURL:        directURL,
			ConnectTimeout:   20 * time.Second,
		})
		mustNoErr(t, err, "connect pooled")
		pooledPool = pool
		rootT.Cleanup(func() {
			pool.Close()
		})

		mustNoErr(t, pool.Ping(ctx), "pool ping")

		status, err := HealthCheck(ctx, pool)
		mustNoErr(t, err, "health check")
		if status.Status != "ok" || status.Database != "neon" {
			t.Fatalf("unexpected health status: %+v", status)
		}

		if pool.Stat() == nil {
			t.Fatal("pool.Stat() returned nil")
		}

		if pool.DirectURL() != directURL {
			t.Fatal("pool.DirectURL() does not match explicit DATABASE_URL_DIRECT")
		}
	})

	t.Run("direct_url_derivation_live", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		pool, err := Connect(ctx, Config{
			ConnectionString: pooledURL,
			ConnectTimeout:   20 * time.Second,
		})
		mustNoErr(t, err, "connect pooled for direct-url derivation")
		defer pool.Close()

		u, err := url.Parse(pool.DirectURL())
		mustNoErr(t, err, "parse derived direct URL")
		host := u.Hostname()
		if host == "" {
			t.Fatal("derived direct URL has empty host")
		}
		if !strings.HasSuffix(host, ".neon.tech") {
			t.Fatalf("derived direct URL host is not neon.tech: %q", host)
		}
		firstLabel, _, ok := strings.Cut(host, ".")
		if !ok {
			t.Fatalf("derived direct URL host has unexpected shape: %q", host)
		}
		if strings.HasSuffix(firstLabel, "-pooler") {
			t.Fatalf("derived direct URL is still pooled: %q", host)
		}
	})

	t.Run("pooled_exec_query_queryrow", func(t *testing.T) {
		if pooledPool == nil {
			t.Fatal("pooled pool not initialized")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		alpha := fmt.Sprintf("alpha_%d", time.Now().UnixNano())
		beta := fmt.Sprintf("beta_%d", time.Now().UnixNano())

		tag, err := pooledPool.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (name, qty, note) VALUES ($1, $2, $3), ($4, $5, $6)", table),
			alpha, 10, "seed-a", beta, 20, "seed-b",
		)
		mustNoErr(t, err, "insert rows via pooled Exec")
		if tag.RowsAffected() != 2 {
			t.Fatalf("insert rows affected=%d, want 2", tag.RowsAffected())
		}

		var alphaQty int
		err = pooledPool.QueryRow(ctx,
			fmt.Sprintf("SELECT qty FROM %s WHERE name = $1", table),
			alpha,
		).Scan(&alphaQty)
		mustNoErr(t, err, "queryrow qty")
		if alphaQty != 10 {
			t.Fatalf("alpha qty=%d, want 10", alphaQty)
		}

		rows, err := pooledPool.Query(ctx,
			fmt.Sprintf("SELECT name, qty FROM %s WHERE name IN ($1, $2) ORDER BY name", table),
			alpha, beta,
		)
		mustNoErr(t, err, "query rows")
		defer rows.Close()

		got := map[string]int{}
		for rows.Next() {
			var name string
			var qty int
			mustNoErr(t, rows.Scan(&name, &qty), "scan row")
			got[name] = qty
		}
		mustNoErr(t, rows.Err(), "rows iteration")

		if len(got) != 2 {
			t.Fatalf("rows count=%d, want 2", len(got))
		}
		if got[alpha] != 10 || got[beta] != 20 {
			t.Fatalf("unexpected queried values: alpha=%d beta=%d", got[alpha], got[beta])
		}
	})

	t.Run("pooled_begin_commit_and_rollback", func(t *testing.T) {
		if pooledPool == nil {
			t.Fatal("pooled pool not initialized")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		commitName := fmt.Sprintf("commit_%d", time.Now().UnixNano())
		rollbackName := fmt.Sprintf("rollback_%d", time.Now().UnixNano())

		txCommit, err := pooledPool.BeginTx(ctx, pgx.TxOptions{})
		mustNoErr(t, err, "begin tx (commit path)")
		_, err = txCommit.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (name, qty, note) VALUES ($1, $2, $3)", table),
			commitName, 1, "commit-path",
		)
		mustNoErr(t, err, "insert in commit tx")
		mustNoErr(t, txCommit.Commit(ctx), "commit tx")

		var committedCount int
		err = pooledPool.QueryRow(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = $1", table),
			commitName,
		).Scan(&committedCount)
		mustNoErr(t, err, "verify committed row")
		if committedCount != 1 {
			t.Fatalf("committed row count=%d, want 1", committedCount)
		}

		txRollback, err := pooledPool.BeginTx(ctx, pgx.TxOptions{})
		mustNoErr(t, err, "begin tx (rollback path)")
		_, err = txRollback.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (name, qty, note) VALUES ($1, $2, $3)", table),
			rollbackName, 1, "rollback-path",
		)
		mustNoErr(t, err, "insert in rollback tx")
		mustNoErr(t, txRollback.Rollback(ctx), "rollback tx")

		var rolledBackCount int
		err = pooledPool.QueryRow(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = $1", table),
			rollbackName,
		).Scan(&rolledBackCount)
		mustNoErr(t, err, "verify rolled back row")
		if rolledBackCount != 0 {
			t.Fatalf("rolled-back row count=%d, want 0", rolledBackCount)
		}
	})

	t.Run("withtx_success_and_rollback_on_error", func(t *testing.T) {
		if pooledPool == nil {
			t.Fatal("pooled pool not initialized")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		name := fmt.Sprintf("withtx_%d", time.Now().UnixNano())
		_, err := pooledPool.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (name, qty, note) VALUES ($1, $2, $3)", table),
			name, 10, "withtx",
		)
		mustNoErr(t, err, "insert withtx seed row")

		err = WithTx(ctx, pooledPool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				fmt.Sprintf("UPDATE %s SET qty = qty + 5 WHERE name = $1", table),
				name,
			)
			return err
		})
		mustNoErr(t, err, "withtx success path")

		var qty int
		err = pooledPool.QueryRow(ctx,
			fmt.Sprintf("SELECT qty FROM %s WHERE name = $1", table),
			name,
		).Scan(&qty)
		mustNoErr(t, err, "verify withtx success qty")
		if qty != 15 {
			t.Fatalf("qty after withtx success=%d, want 15", qty)
		}

		sentinel := errors.New("withtx sentinel error")
		err = WithTx(ctx, pooledPool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				fmt.Sprintf("UPDATE %s SET qty = qty + 100 WHERE name = $1", table),
				name,
			)
			if err != nil {
				return err
			}
			return sentinel
		})
		mustIs(t, err, sentinel, "withtx rollback path should return sentinel")

		err = pooledPool.QueryRow(ctx,
			fmt.Sprintf("SELECT qty FROM %s WHERE name = $1", table),
			name,
		).Scan(&qty)
		mustNoErr(t, err, "verify withtx rollback qty")
		if qty != 15 {
			t.Fatalf("qty after withtx rollback=%d, want 15", qty)
		}
	})

	t.Run("direct_session_feature_advisory_lock", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		conn1, err := pgx.Connect(ctx, directURL)
		mustNoErr(t, err, "connect direct conn1")
		defer conn1.Close(context.Background())

		conn2, err := pgx.Connect(ctx, directURL)
		mustNoErr(t, err, "connect direct conn2")
		defer conn2.Close(context.Background())

		lockID := time.Now().UnixNano() & 0x7fffffffffffffff

		var unlocked bool
		defer func() {
			_ = conn1.QueryRow(context.Background(), "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked)
			_ = conn2.QueryRow(context.Background(), "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked)
		}()

		_, err = conn1.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID)
		mustNoErr(t, err, "acquire advisory lock on conn1")

		var conn2Acquired bool
		err = conn2.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&conn2Acquired)
		mustNoErr(t, err, "conn2 try lock while held")
		if conn2Acquired {
			t.Fatal("conn2 unexpectedly acquired lock while conn1 holds it")
		}

		err = conn1.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked)
		mustNoErr(t, err, "unlock advisory lock on conn1")
		if !unlocked {
			t.Fatal("conn1 unlock reported false")
		}

		err = conn2.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&conn2Acquired)
		mustNoErr(t, err, "conn2 try lock after unlock")
		if !conn2Acquired {
			t.Fatal("conn2 did not acquire lock after conn1 unlock")
		}

		err = conn2.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked)
		mustNoErr(t, err, "unlock advisory lock on conn2")
		if !unlocked {
			t.Fatal("conn2 unlock reported false")
		}
	})
}

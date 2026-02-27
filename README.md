# vango-neon

`vango-neon` is the Neon PostgreSQL integration package for Vango applications.

## Scope (What This Repo Does / Does Not Do)

This repository implements the **`github.com/vango-go/vango-neon` Go package**
only: a production-hardened Neon/pgx integration layer that is safe-by-default
around secrets and migrations.

Out of scope for `vango-neon` (even if mentioned in the spec as ecosystem
guidance or future work):

- **Vango runtime production logging policy** (for example, debug vs prod error
  chain printing / suppression): this belongs to the **Vango runtime** (the
  consumer of errors), not this package (the producer of safe outer errors).
- **Vango CLI work** (for example, `vango neon ...` subcommands, scaffolding,
  or `neonctl` integration): this belongs to the **Vango CLI/tooling repo**,
  not this library.

See `/Users/collinshill/Documents/vango/vango-neon/PHASED_BUILD_PLAN.md` for an
explicit in-scope/out-of-scope breakdown and `/Users/collinshill/Documents/vango/vango-neon/NEON_INTEGRATION_SPEC.md`
for the full contract reference.

It provides:
- a `DB` interface for dependency injection in services and route dependencies
- a concrete `Pool` implementation backed by `pgxpool`
- `Connect` defaults hardened for Neon usage
- safe-by-default outer errors (`SafeError`) that avoid DSN leakage
- direct URL resolution for migrations/session-level operations (`Pool.DirectURL()`)
- helper functions (`HealthCheck`, `WithTx`)
- a deterministic test kit (`TestDB`, `ErrRow`, `ErrRows`, `NewRow`, `RowsBuilder`)

## Install

```bash
go get github.com/vango-go/vango-neon
```

## Requirements

- Go 1.24+ (the current `github.com/jackc/pgx/v5` pin, `v5.8.0`, requires Go 1.24.0)

## Environment setup (dual URL)

Use two connection strings:

- `DATABASE_URL` for app query traffic (pooled endpoint, hostname includes `-pooler`)
- `DATABASE_URL_DIRECT` for migrations and session-level operations (direct endpoint, no `-pooler`)

```env
DATABASE_URL="postgresql://user:pass@ep-name-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require&channel_binding=require"
DATABASE_URL_DIRECT="postgresql://user:pass@ep-name.us-east-2.aws.neon.tech/neondb?sslmode=require&channel_binding=require"
```

If `DirectURL` is empty and `ConnectionString` is a Neon pooled URL in URL form,
`vango-neon` derives the direct URL by removing `-pooler` from the first hostname
label. Non-Neon hostnames are never rewritten.

Compatibility note: if you encounter connection issues in a constrained
environment, remove `channel_binding=require` first and keep
`sslmode=require` (or stricter). Channel binding is hardening; TLS is the
requirement.

For additional hardening in production, upgrade to `sslmode=verify-full`
(requires providing a trusted Neon CA certificate via libpq/pgx mechanisms).

## Quickstart (connect and inject)

```go
package db

import (
	"context"
	"os"

	neon "github.com/vango-go/vango-neon"
)

func ConnectPool(ctx context.Context) (*neon.Pool, error) {
	return neon.Connect(ctx, neon.Config{
		ConnectionString: os.Getenv("DATABASE_URL"),
		DirectURL:        os.Getenv("DATABASE_URL_DIRECT"),
	})
}
```

## Migration guidance (direct URL only)

Migrations must use a direct (non-pooled) URL.

```go
pool, err := neon.Connect(ctx, neon.Config{
	ConnectionString: os.Getenv("DATABASE_URL"),
	DirectURL:        os.Getenv("DATABASE_URL_DIRECT"),
})
if err != nil {
	return err
}
defer pool.Close()

// Use this with your migration tool (goose, etc.).
directURL := pool.DirectURL()
_ = directURL
```

`Pool.DirectURL()` contains credentials. Treat it as secret material and never
log it.

## Health checks

```go
status, err := neon.HealthCheck(ctx, db)
if err != nil {
	return err
}
// status: {Status:"ok", Database:"neon"}
_ = status
```

## Transactions with `WithTx`

```go
err := neon.WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "UPDATE projects SET owner_id = $1 WHERE id = $2", ownerID, projectID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO audit_log (project_id, action) VALUES ($1, $2)", projectID, "owner_changed")
	return err
})
```

`WithTx` handles begin/commit/rollback and re-panics after rollback when the
work function panics.

## Tracing and advanced pgx configuration

Use `WithPgxConfig` to attach pgx tracer hooks. Default posture should avoid
logging SQL text and args because they may contain sensitive data.

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

opt := neon.WithPgxConfig(func(c *pgxpool.Config) {
	c.ConnConfig.Tracer = &tracelog.TraceLog{
		Logger: tracelog.LoggerFunc(func(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
			safe := make(map[string]any, len(data))
			for k, v := range data {
				if k == "sql" || k == "args" {
					continue
				}
				safe[k] = v
			}
			logger.InfoContext(ctx, msg, "pgx_level", level.String(), "pgx", safe)
		}),
		LogLevel: tracelog.LogLevelInfo,
	}
})

_ = opt
```

## Test kit

`vango-neon` ships deterministic testing utilities in the same package:

- `TestDB` for dependency injection in unit tests
- `ErrRow` and `ErrRows` sentinels for error-path testing
- `NewRow` for single-row success cases in `QueryRow` tests
- `RowsBuilder` for fixed in-memory `pgx.Rows` in `Query` tests

## Integration tests

High-confidence live Neon integration tests are available behind the
`integration` build tag.

Local run:

```bash
DATABASE_URL="postgresql://...-pooler...neon.tech/...?sslmode=require" \
DATABASE_URL_DIRECT="postgresql://...neon.tech/...?sslmode=require" \
go test -tags=integration -race ./...
```

Behavior:
- integration tests fail fast when `DATABASE_URL` or `DATABASE_URL_DIRECT` is missing
- tests never intentionally print raw DSNs in failures
- tests create an isolated schema per run and clean it up

CI:
- `.github/workflows/integration.yml` runs integration tests on ephemeral Neon branches
- branch setup and teardown happen in the workflow
- connection strings are exported to environment variables without being echoed

## Security posture

- `Connect` rejects plaintext-capable TLS modes (`sslmode=allow` / `sslmode=prefer`).
- `Connect` requires TLS (`sslmode=require` or stricter).
- `channel_binding=require` is recommended hardening; remove it first if you
  hit connectivity issues, but keep `sslmode=require` (or stricter).
- Public outer errors are safe to log by default and avoid credential-bearing DSN output.
- `SafeError.Unwrap()` may contain sensitive upstream detail; avoid logging full chains in production defaults.
- Connection strings and `Pool.DirectURL()` are secrets. Do not print or commit them.

## Contract reference

See [NEON_INTEGRATION_SPEC.md](./NEON_INTEGRATION_SPEC.md) for the full
package contract and invariants.

For a step-by-step integration walkthrough (Vango scaffolding, dotenv behavior,
migrations, branch workflows, security posture), see
[DEVELOPER_GUIDE.md](./DEVELOPER_GUIDE.md).

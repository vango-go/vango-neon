# vango-neon Developer Guide

This guide explains how to use **Neon PostgreSQL** with:

- the `github.com/vango-go/vango-neon` Go package (this repository), and
- the Vango CLI integration (`vango create --with neon`, `vango dev` hints, `vango neon ...`) implemented in the Vango core repo (`github.com/vango-go/vango`).

The authoritative contract for the library lives in:

- `vango-neon/NEON_INTEGRATION_SPEC.md`

---

## Table of contents

- [Goals and mental model](#goals-and-mental-model)
- [Requirements](#requirements)
- [Environment configuration (dual DSNs)](#environment-configuration-dual-dsns)
- [Quickstart (library usage)](#quickstart-library-usage)
- [Using vango-neon with Vango (scaffolded integration)](#using-vango-neon-with-vango-scaffolded-integration)
- [Configuration and tuning](#configuration-and-tuning)
- [Migrations (direct URL only)](#migrations-direct-url-only)
- [Health checks](#health-checks)
- [Transactions with WithTx](#transactions-with-withtx)
- [Pooler mode behavior (pgx sharp edges)](#pooler-mode-behavior-pgx-sharp-edges)
- [Tracing and observability](#tracing-and-observability)
- [Safe secrets handling](#safe-secrets-handling)
- [Phase 3 developer workflow: branches + commands (Vango core)](#phase-3-developer-workflow-branches--commands-vango-core)
- [Testing](#testing)
- [Troubleshooting checklist](#troubleshooting-checklist)

## Goals and mental model

Neon provides **two endpoints per branch**:

- **Pooled** endpoint (app traffic): hostname includes `-pooler` (Neon proxy / pooler).
- **Direct** endpoint (migrations and session-level Postgres features): hostname does **not** include `-pooler`.

Vango applications should follow the dual-DSN model:

- `DATABASE_URL` → pooled DSN for runtime query traffic.
- `DATABASE_URL_DIRECT` → direct DSN for migrations and anything session-level (advisory locks, LISTEN/NOTIFY, some migration tooling behaviors).

`vango-neon` enforces this model and hardens the sharp edges:

- TLS-only (rejects plaintext-capable `sslmode` fallbacks).
- Pooler detection (forces pgx “simple protocol” + disables statement/description caches).
- Safe outer errors (no credential-bearing DSN leakage in `err.Error()`).
- Direct URL resolution (`Pool.DirectURL()`), including safe pooled→direct derivation for Neon hostnames when possible.

---

## Requirements

- Go **1.24+** for `vango-neon` (due to the `pgx/v5` pin).
- A Neon project + branch (created in the Neon console, `neon` CLI, or via `vango neon ...` commands in Vango core).

---

## Environment configuration (dual DSNs)

**Pooled DSN** (recommended for app query traffic):

```env
DATABASE_URL="postgresql://user:pass@ep-name-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require&channel_binding=require"
```

**Direct DSN** (required for migrations and session-level operations):

```env
DATABASE_URL_DIRECT="postgresql://user:pass@ep-name.us-east-2.aws.neon.tech/neondb?sslmode=require&channel_binding=require"
```

### TLS defaults + compatibility fallback

- `vango-neon` **rejects** any connection configuration that can fall back to plaintext (`sslmode=allow` / `sslmode=prefer` semantics).
- Minimum accepted posture is `sslmode=require` (or stricter).
- Recommended hardening is `channel_binding=require`.

If you encounter connection issues in a constrained environment:

1. **Remove `channel_binding=require` first**, and
2. **keep `sslmode=require`** (or stricter).

`channel_binding` is hardening; TLS is the requirement.

### Direct URL resolution behavior

If `DATABASE_URL_DIRECT` (or `Config.DirectURL`) is empty:

- When `DATABASE_URL` is a Neon pooled URL **in URL form** (`postgres://` or `postgresql://`) and the hostname is `*.neon.tech` with a first label ending in `-pooler`, `vango-neon` derives a direct URL by removing the `-pooler` suffix from the first label.
- If `DATABASE_URL` is a pooler hostname but is **not** URL-form parseable (e.g. keyword/value DSN format), `Connect` fails fast and requires an explicit direct URL to uphold the “direct-only migrations” invariant.
- For non-pooler URLs, the pooled DSN is used as direct as well.

---

## Quickstart (library usage)

### Connect once at startup

Create a pool once, near `main()`, then inject `neon.DB` into the rest of your app.

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

### Prefer depending on the `neon.DB` interface

`neon.DB` is intentionally minimal: it’s the contract your app code should accept so it stays testable (with `neon.TestDB`) and decoupled from pool operational concerns.

Use `*neon.Pool` only where you need pool-specific operations like `pool.Stat()` or `pool.DirectURL()`.

---

## Using vango-neon with Vango (scaffolded integration)

If you’re using the Vango CLI integration (in the Vango core repo), the canonical path is:

```bash
vango create myapp --with neon
cd myapp
vango dev
```

### Non-interactive scaffolding (CI/agents)

To scaffold without calling the Neon CLI (and without printing DSNs), provide both flags:

```bash
vango create myapp --with neon \
  --neon-connection-string "$DATABASE_URL" \
  --neon-direct-url "$DATABASE_URL_DIRECT"
```

If either flag is provided, Vango requires **both** (fail-closed).

### Optional Neon CLI auto-fill (developer convenience)

If the Neon CLI is installed (`neon` or `neonctl` in `PATH`), Vango can try to auto-fill `.env` during `vango create --with neon`.

Inputs that affect auto-fill:

- `NEON_PROJECT_ID` (preferred): use an existing Neon project id.
- If unset: Vango attempts to create (or reuse) a project named `vango-<appname>`.
- `NEON_ROLE_NAME` (optional): role name for connection strings (default `neondb_owner`).

Whether auto-fill succeeds or not, the scaffolded code is generated; the `.env` step is best-effort.

What you get (conceptually):

- `.env` written safely (permissions `0600` when created by Vango tooling) with:
  - `DATABASE_URL`
  - `DATABASE_URL_DIRECT`
- `vango.json` includes a `neon` block (for Neon-aware dev features):
  - `"neon": { "enabled": true, "project_id": "...", "branch_detection": true }`
- a Neon-first DB layer using `github.com/vango-go/vango-neon`
- a direct-only migration runner (Goose) in `cmd/migrate`
- a DB health endpoint (typed) that calls `neon.HealthCheck(ctx, db)`

### Dotenv loading

The Vango CLI loads dotenv files from the project root:

- `.env` then `.env.local`

Default behavior preserves already-set environment variables; use `--dotenv=override` to overwrite, or `--dotenv=off` to disable dotenv loading.

---

## Configuration and tuning

`vango-neon` exposes a small set of pool knobs via `neon.Config` with Neon-safe defaults:

| Field | Default | Notes |
|---|---:|---|
| `MaxConns` | `10` | Conservative default for serverless compute + proxy multiplexing. |
| `MinConns` | `0` | Allows scale-to-zero. |
| `HealthChecksDisabled` | `false` | Disable only if you have strong reasons. |
| `HealthCheckPeriod` | `30s` | Ignored if health checks disabled. |
| `MaxConnLifetime` | `30m` | Rotates connections periodically. |
| `MaxConnIdleTime` | `5m` | Closes long-idle connections. |
| `ConnectTimeout` | `10s` | Helps with cold starts / scale-from-zero. |
| `ForcePoolerMode` | `false` | Forces pooler clamps regardless of hostname detection. |

For pooler behavior, see [Pooler mode behavior](#pooler-mode-behavior-pgx-sharp-edges).

## Migrations (direct URL only)

### Why direct-only?

Most migration tools (including Goose) rely on session-level behavior and may use advisory locks. Neon pooler endpoints do not support all session-level operations reliably.

Use `DATABASE_URL_DIRECT` (or `pool.DirectURL()`).

### Recommended: Goose (`pressly/goose` v3+)

Typical patterns:

- **Local development:** run `go run ./cmd/migrate` manually when you change schema.
- **CI/production:** run migrations as a dedicated step before rollout, using direct DSN.

Example runner shape (simplified):

```go
directURL := os.Getenv("DATABASE_URL_DIRECT")
if directURL == "" {
	// fail closed
}
// run goose up using a *sql.DB opened via pgx stdlib adapter
```

Never run migrations against the pooled DSN.

---

## Health checks

`HealthCheck` is a free function so it works against the `neon.DB` interface (and `neon.TestDB` in tests):

```go
status, err := neon.HealthCheck(ctx, db)
if err != nil {
	return err
}
// status: {Status:"ok", Database:"neon"}
```

Errors returned by `vango-neon` are safe to log at the outer layer (no DSNs/credentials).

---

## Transactions with `WithTx`

Use `WithTx` to avoid common transaction footguns (missing rollback, rollback-after-commit, panic paths):

```go
err := neon.WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "UPDATE accounts SET balance = balance - $1 WHERE id = $2", amount, fromID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE accounts SET balance = balance + $1 WHERE id = $2", amount, toID)
	return err
})
```

---

## Pooler mode behavior (pgx sharp edges)

When `Connect` determines you’re using a Neon pooler endpoint (or you set `ForcePoolerMode=true`), it enforces:

- `DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol`
- `StatementCacheCapacity = 0`
- `DescriptionCacheCapacity = 0`

This prevents prepared-statement and statement-cache behavior from being proxy-dependent.

If you need to attach tracing/logging, use `WithPgxConfig`—but avoid compatibility-breaking overrides unless you fully understand the consequences.

---

## Tracing and observability

Attach pgx tracing/logging via `WithPgxConfig`. Default posture should avoid logging:

- raw SQL text (may contain sensitive information), and
- args/params (often contain user data).

Example pattern (redact SQL/args):

```go
pool, err := neon.Connect(ctx, neon.Config{ConnectionString: pooled, DirectURL: direct}, neon.WithPgxConfig(func(c *pgxpool.Config) {
	// Attach tracer/logger here. Ensure your logger does not emit SQL or args by default.
}))
```

For operational visibility, the concrete `*neon.Pool` exposes `pool.Stat()` (not part of `neon.DB`).

## Safe secrets handling

### Connection strings are secrets

The following values contain credentials and must be treated as secret material:

- `DATABASE_URL`
- `DATABASE_URL_DIRECT`
- `Pool.DirectURL()`

Do not print them. Do not commit them. Do not include them in error messages.

### Error safety

`vango-neon` returns safe outer errors (e.g. `neon.SafeError`) whose `Error()` string is safe to log by default.

The wrapped cause (`Unwrap()`) may contain sensitive upstream details; do not stringify full error chains in production logs unless you explicitly opt into debug behavior.

---

## Phase 3 developer workflow: branches + commands (Vango core)

These features are implemented in the **Vango CLI** (not in this library), but they are the canonical workflow when using Neon with Vango.

### Neon-aware `vango dev` branch detection (informational only)

When `vango.json` has `"neon": { "enabled": true }`, `vango dev` can perform an informational check:

- Detect current git branch.
- Detect whether a matching Neon branch exists.
- Print hints only; **never changes connection automatically**.

It uses:

- `NEON_API_KEY` (preferred) to call Neon’s HTTP API, or
- falls back to the `neon` CLI if installed.

### `vango dev --neon-branch=<name>`

Fetches pooled+direct DSNs for a Neon branch and injects them into the dev app process **for this run only** (no `.env` writes).

### Native `vango neon ...` commands (no neonctl dependency)

Native commands require:

- `NEON_API_KEY`
- a project root (must be in a directory with `vango.json`)
- a project id via `NEON_PROJECT_ID` or `vango.json` `neon.project_id`

Optional environment variables used by the CLI:

- `NEON_ROLE_NAME` (default `neondb_owner`)
- `NEON_DATABASE_NAME` (default `neondb`)

Common commands:

```bash
vango neon branch list
vango neon branch create feature/login
vango neon branch delete feature/login
vango neon connect --branch main
```

`vango neon connect` writes pooled+direct DSNs to `.env` and prints **redacted** host/db summaries only.

---

## Testing

### Unit tests with `neon.TestDB`

`vango-neon` ships a deterministic test kit in the main package:

- `TestDB`
- `ErrRow`, `ErrRows`
- `NewRow`
- `RowsBuilder` (`NewRows(...).AddRow(...).Build()`)

This is designed so application code can depend on `neon.DB` and unit tests can provide a safe mock that behaves well with typical pgx patterns (`defer rows.Close()`, etc.).

### Live integration tests

This repository includes high-confidence Neon integration tests behind the `integration` build tag.

Run:

```bash
DATABASE_URL="postgresql://...-pooler...neon.tech/...?sslmode=require" \
DATABASE_URL_DIRECT="postgresql://...neon.tech/...?sslmode=require" \
go test -tags=integration -race ./...
```

---

## Troubleshooting checklist

- **TLS rejected (`neon: insecure connection rejected`)**
  - Ensure your DSN includes `sslmode=require` (or stricter).
  - Do not use `sslmode=allow` or `sslmode=prefer`.
- **Connectivity issues with `channel_binding=require`**
  - Remove `channel_binding=require` first; keep `sslmode=require`.
- **Migrations hang / advisory lock errors**
  - You are likely using the pooled DSN. Use `DATABASE_URL_DIRECT` (direct).
- **Unexpected prepared statement / statement cache errors**
  - Ensure your app is using the pooled endpoint for `DATABASE_URL` (pooler mode clamps are applied).
  - If you are using a non-Neon pooler/proxy, consider `ForcePoolerMode=true`.

---

## Reference

- Full library contract: `vango-neon/NEON_INTEGRATION_SPEC.md`
- Package README: `vango-neon/README.md`

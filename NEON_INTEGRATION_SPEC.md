# Vango + Neon: Final Integration Specification v2.0

**Status:** Final Implementation Reference (Revised)
**Date:** 2026-02-15
**Supersedes:** v1.0 (2026-02-15)
**Audience:** Vango Core Team, AI Coding Agents, Go Developers

---

## Revision Summary (v1.0 → v2.0)

| Area | v1.0 | v2.0 | Source |
|---|---|---|---|
| Pooler detection | Hostname + port 6543 | Hostname only (`-pooler`); port heuristic removed; pooler mode clamps pgx caches | Neon docs: port is 5432 for both modes |
| Migration connection | Single URL | Dual URL: pooled (app) + direct (migrations) | Neon docs: migrations require direct connections |
| SSL defaults | `sslmode=require` minimum | `sslmode=require&channel_binding=require` default; `verify-full` documented as upgrade | Neon security docs |
| Health check | Type assertion `deps.DB.(*neon.Pool)` | Free function `neon.HealthCheck(ctx, db)` | Facade consistency |
| Secrets handling | Connection string in CLI output | Redacted output; `--write-env` for safe file writes | Neon CLI returns passwords in connection strings |
| Pool lifecycle | MaxConns/MinConns only | Added HealthCheckPeriod (+HealthChecksDisabled), MaxConnLifetime, MaxConnIdleTime, ConnectTimeout | Neon pooling guidance |
| Transaction helper | Manual Begin/Rollback/Commit | Added `neon.WithTx` ergonomic helper | Agent footgun reduction |
| Test kit | `errRow` unexported | `ErrRow` / `ErrRows` exported; `NewRow` + `RowsBuilder` helpers added | Usability for agents |
| Observability | Placeholder comment | Trace-first via pgx tracer hooks; `Pool.Stat()` read-only method | Vango trace propagation model (§45.3) |
| CLI architecture | Hard dependency on `neonctl` | Optional `neonctl` shelling; native `vango neon` subcommands via HTTP API (Phase 3+) | Single-binary philosophy |

---

## Document Structure

1. **The `vango-neon` Package Specification** — complete API, configuration, implementation, test kit, helpers.
2. **Developer Guide Sections** — §37.6 (Database Layer), §37.7 (Migrations), §37.8 (Testing).
3. **Appendix G** — Branching Workflow, CLI Reference, Operational Guardrails, Troubleshooting.

All code targets `pgx` v5 types. AI agents can treat all type signatures as authoritative.

---

## Design Invariants (v2.0, Non-Negotiable)

These invariants are the acceptance criteria for implementation. If an
implementation cannot uphold an invariant, the spec must be revised explicitly
before code ships.

### I1. I/O Boundary Invariant (Vango Contract)

All database I/O MUST occur only inside **Resource loaders** and **Action work
functions** (service-layer helpers called from there are fine).

Database I/O MUST NOT occur from:

- Setup callbacks
- Render closures
- Event handlers
- Lifecycle callbacks (`OnMount`, `Effect`, `OnChange`)

This is required to uphold Vango’s session loop “single-writer, non-blocking”
runtime contract.

### I2. Direct-Only Migrations Invariant (Neon Contract)

Migrations (and any session-level Postgres features such as advisory locks,
prepared statements, and LISTEN/NOTIFY) MUST use a **direct (non-pooler)**
connection string.

`vango-neon` MUST make this hard to violate:

- `Pool.DirectURL()` returns the resolved direct URL for migrations.
- If `Config.DirectURL` is empty, pooled→direct auto-derivation MAY occur only
  when it is provably safe (Neon hostname pattern, URL-form DSN).

### I3. Pooler-Mode Determinism Invariant (PgBouncer Contract)

If the application connection string is a Neon pooler endpoint (or
`ForcePoolerMode=true`), `vango-neon` MUST enforce the following pgx knobs:

- `DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol`
- `StatementCacheCapacity = 0`
- `DescriptionCacheCapacity = 0`

This invariant exists to prevent prepared-statement / cache behavior from
becoming ambiguous or proxy-dependent.

### I4. Secrets & Error-Safety Invariant

No public API in `vango-neon` may return/log/format credential-bearing DSNs by
default.

Additionally:

- Errors returned by `vango-neon` MUST be safe to log by default (contain at
  most hostname + high-level context).
- Wrapped upstream causes MUST be treated as potentially sensitive. Production
  logging MUST NOT stringify wrapped causes by default unless explicitly opted
  into debug logging.

#### I4.1 Production Logging Policy (Defense-in-Depth)

This invariant is enforced at two layers:

1. **`vango-neon` layer (producer): safe outer errors**

   - `vango-neon` MUST return errors whose `Error()` string is safe to log in
     production (no DSNs, no credentials).
   - If the error wraps an underlying cause (`Unwrap()`), that cause MUST be
     treated as potentially sensitive and MUST NOT be stringified in
     production logs by default.
   - The primary mechanism is a “safe outer error” wrapper (e.g. `neon.SafeError`)
     whose `Error()` is safe and whose `Unwrap()` retains the real cause for
     `errors.Is/As` and debug-only inspection.

2. **Vango runtime layer (consumer): safe-by-default production logging**

   - When `DebugMode=false`, Vango MUST treat any `error` payloads as
     potentially sensitive:
     - log `error="[SUPPRESSED]"` (or a safe error message if explicitly
       provided),
     - include stable metadata like `error_type` and `error_fingerprint` (and
       optional `error_code` if present),
     - avoid printing the full error chain (unwrap) unless explicitly enabled.
   - When `DebugMode=true`, Vango MAY include full error strings/chains to
     accelerate debugging.

This “producer + consumer” posture ensures:

- application and runtime logs remain safe even if upstream libraries include
  sensitive strings inside error messages, and
- debug workflows remain possible without weakening production defaults.

#### I4.2 Tests (Required)

`vango-neon` MUST include tests that assert:

- `err.Error()` from common failure paths never contains DSNs or credentials
  (for example `postgresql://`, `postgres://`, `password=`, `@` in URL
  authority context),
- safe errors remain matchable via `errors.Is/As` against underlying causes.

### I5. TLS-Only Invariant

`vango-neon` MUST reject any connection configuration that can fall back to
plaintext (including `sslmode=allow` / `sslmode=prefer` semantics).

Minimum accepted posture is TLS-required (`sslmode=require` or stricter). Any
additional hardening parameters (for example `channel_binding=require`) MUST
have a documented fallback path.

---

# Part 1: The `vango-neon` Package Specification

**Import Path:** `github.com/vango-go/vango-neon`

**Dependencies:**
- `github.com/jackc/pgx/v5`
- `github.com/jackc/pgx/v5/pgconn`
- `github.com/jackc/pgx/v5/pgxpool`

**Toolchain:** Go 1.24+ (current `pgx/v5` dependency pin requires Go 1.24.0)

---

## 1.1 The `DB` Interface

The source-of-truth contract for all Vango data access. Application code, route dependencies, and test mocks all target this interface.

The interface deliberately excludes pool management operations (`Stats`, `Acquire`, lifecycle configuration). These belong on the concrete `Pool` type, not the data access contract.

```go
package neon

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB defines the contract for database access in Vango applications.
//
// All methods require context.Context. This ensures that Vango's Resource
// and Action cancellation propagates to in-flight database operations:
// when a component unmounts, its context is cancelled, and any in-flight
// query is terminated.
//
// Use this interface in your route dependencies (routes.Deps) and service
// layer constructors. Never accept *neon.Pool directly in application code.
type DB interface {
	// Exec executes a query that doesn't return rows (INSERT, UPDATE, DELETE, DDL).
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)

	// Query executes a query that returns rows, typically a SELECT.
	// The caller MUST close the returned Rows when done (use defer rows.Close()).
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)

	// QueryRow executes a query expected to return at most one row.
	// If no rows match, row.Scan() returns pgx.ErrNoRows.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row

	// Begin starts a transaction with default options.
	// The caller MUST call tx.Commit() or tx.Rollback().
	// Prefer WithTx() for automatic rollback-on-error semantics.
	Begin(ctx context.Context) (pgx.Tx, error)

	// BeginTx starts a transaction with explicit options (isolation level,
	// read-only mode, deferrable).
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)

	// Ping verifies the database connection is alive.
	Ping(ctx context.Context) error

	// Close releases all pool resources. Call once during graceful shutdown.
	Close()
}
```

---

## 1.2 Configuration

```go
package neon

import "time"

// Config controls the behavior of the Neon connection pool.
//
// All fields except ConnectionString have safe defaults optimized for
// Neon's serverless architecture.
type Config struct {
	// ConnectionString is the Neon database URL used for application queries.
	//
	// This may be a pooled endpoint (recommended for application traffic).
	// Format:
	//   postgresql://user:pass@ep-name-pooler.region.aws.neon.tech/db?sslmode=require&channel_binding=require
	//
	// MUST include sslmode=require (or stricter). The package rejects
	// connection strings that would result in plaintext connections (including
	// sslmode=allow/prefer which can fall back to plaintext).
	//
	// Obtain from: Neon Console → Project → Dashboard → Connect.
	ConnectionString string

	// DirectURL is the Neon database URL used for operations that require
	// a direct (non-pooled) connection: schema migrations, LISTEN/NOTIFY,
	// advisory locks, and other session-level features.
	//
	// Format:
	//   postgresql://user:pass@ep-name.region.aws.neon.tech/db?sslmode=require&channel_binding=require
	//
	// If empty AND ConnectionString is a Neon pooler URL (first hostname label
	// ends with "-pooler" and suffix is ".neon.tech"), the package derives the
	// direct URL by removing the "-pooler" suffix from the first label. This
	// derivation is applied ONLY for Neon hostnames; non-Neon hostnames are
	// never rewritten.
	//
	// NOTE: Derivation requires a URL-form connection string (postgresql://...).
	// If you use a non-URL DSN format and you connect to a pooler endpoint,
	// Connect fails fast and requires DirectURL explicitly (to preserve the
	// "direct-only migrations" invariant).
	//
	// If empty AND ConnectionString is NOT a pooler URL, ConnectionString
	// is used for both pooled and direct operations.
	DirectURL string

	// MaxConns limits the maximum number of connections in the pool.
	// Default: 10
	//
	// Neon compute endpoints have connection limits that vary by compute
	// size (e.g., ~100 for 0.25 CU). Neon's proxy handles multiplexing;
	// keep the local pool conservative.
	//
	// For multi-instance deployments:
	//   MaxConns = Neon connection limit / number of app instances
	MaxConns int32

	// MinConns controls the minimum idle connections maintained.
	// Default: 0 (allows Neon compute to scale to zero)
	MinConns int32

	// ForcePoolerMode forces simple protocol (no prepared statements),
	// regardless of hostname detection.
	// Default: false (auto-detection via hostname)
	ForcePoolerMode bool

	// --- Pool Lifecycle (optional, with Neon-optimized defaults) ---

	// HealthChecksDisabled disables background health checks of idle
	// connections.
	// Default: false
	//
	// If true, HealthCheckPeriod is ignored.
	HealthChecksDisabled bool

	// HealthCheckPeriod controls how often idle connections are health-checked.
	// Default: 30s
	//
	// To disable health checks, set HealthChecksDisabled=true.
	// Neon generally recommends periodic checks for resilience.
	HealthCheckPeriod time.Duration

	// MaxConnLifetime is the maximum lifetime of a connection before it is
	// closed and replaced. Helps cycle connections after Neon maintenance.
	// Default: 30m
	MaxConnLifetime time.Duration

	// MaxConnIdleTime is the maximum time a connection can sit idle before
	// it is closed. Reduces stale connections after traffic drops.
	// Default: 5m
	MaxConnIdleTime time.Duration

	// ConnectTimeout is the maximum time to wait for a new connection.
	// This is especially relevant for Neon cold starts (scale-from-zero).
	// Default: 10s
	// Set higher (30s+) if your Neon compute frequently suspends.
	ConnectTimeout time.Duration
}
```

---

## 1.3 The `Pool` Implementation

```go
package neon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option configures Connect for advanced use cases (tracing, type
// registrations, hooks).
type Option func(*connectOptions)

type connectOptions struct {
	pgxConfigModifier func(*pgxpool.Config)
}

// WithPgxConfig allows low-level pgxpool configuration for advanced use
// cases: custom tracers, custom type registrations, AfterConnect hooks.
//
// The modifier runs AFTER all standard configuration (TLS, pooler mode,
// limits, lifecycle) has been applied, so it can override any default.
//
// Use sparingly. The modifier receives the full pgxpool.Config; changes
// are your responsibility and may break Neon compatibility guarantees.
func WithPgxConfig(fn func(*pgxpool.Config)) Option {
	return func(o *connectOptions) {
		o.pgxConfigModifier = fn
	}
}

// Pool is the concrete implementation of DB backed by Neon.
// It does NOT embed *pgxpool.Pool.
type Pool struct {
	pool      *pgxpool.Pool
	directURL string // resolved direct URL for migrations
}

var _ DB = (*Pool)(nil)

// SafeError is an error wrapper whose Error() string is guaranteed not to
// include secret material (connection strings, passwords, etc.).
//
// The underlying cause is available via errors.Unwrap / errors.Is / errors.As.
// WARNING: the cause may contain sensitive strings depending on upstream
// libraries and should not be logged verbatim in production.
type SafeError struct {
	msg   string
	cause error
}

func (e *SafeError) Error() string { return e.msg }
func (e *SafeError) Unwrap() error { return e.cause }

func isNeonPoolerHost(host string) bool {
	if !strings.HasSuffix(host, ".neon.tech") {
		return false
	}
	firstLabel, _, ok := strings.Cut(host, ".")
	if !ok {
		return false
	}
	return strings.HasSuffix(firstLabel, "-pooler")
}

// Connect creates a production-hardened connection pool for Neon.
//
// Validations and adjustments performed:
//  1. Rejects empty connection strings.
//  2. Rejects insecure connections (TLS must be configured).
//  3. Auto-detects Neon pooler mode via hostname and configures simple
//     protocol to avoid prepared statement conflicts with PgBouncer.
//  4. Resolves the direct URL for migration use.
//  5. Applies conservative connection limits and lifecycle defaults.
//  6. Verifies connectivity with an initial ping.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*Pool, error) {
	if cfg.ConnectionString == "" {
		return nil, errors.New("neon: ConnectionString is required")
	}

	// --- Parse ---
	pgxCfg, err := pgxpool.ParseConfig(cfg.ConnectionString)
	if err != nil {
		// SECURITY: Connection strings contain passwords. Do not wrap or
		// forward parse errors that may echo the raw DSN.
		return nil, errors.New(
			"neon: invalid connection string (expected URL form: postgresql://user:pass@host/db?... )",
		)
	}

	// --- SSL Enforcement ---
	//
	// SECURITY: Reject any configuration that can fall back to plaintext.
	// In pgx/libpq semantics, sslmode=allow/prefer are implemented via
	// plaintext fallbacks (Fallbacks with nil TLSConfig).
	if pgxCfg.ConnConfig.TLSConfig == nil {
		return nil, errors.New(
			"neon: insecure connection rejected. " +
				"Connection string must include sslmode=require (or stricter). " +
				"Recommended: sslmode=require&channel_binding=require",
		)
	}
	for _, fb := range pgxCfg.ConnConfig.Fallbacks {
		if fb.TLSConfig == nil {
			return nil, errors.New(
				"neon: insecure connection rejected. " +
					"sslmode=allow/prefer is not permitted (plaintext fallback). " +
					"Use sslmode=require, sslmode=verify-ca, or sslmode=verify-full.",
			)
		}
	}

	// --- Pooler Detection ---
	// Neon pooler endpoints use "-pooler" in the hostname.
	// Port is 5432 for both pooled and direct; do NOT use port for detection.
	host := pgxCfg.ConnConfig.Host
	isPooler := cfg.ForcePoolerMode || isNeonPoolerHost(host)

	if isPooler {
		pgxCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		pgxCfg.ConnConfig.StatementCacheCapacity = 0
		pgxCfg.ConnConfig.DescriptionCacheCapacity = 0
	}

	// --- Direct URL Resolution ---
	directURL, err := resolveDirectURL(cfg, host)
	if err != nil {
		return nil, err
	}

	// --- Connection Limits ---
	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	} else {
		pgxCfg.MaxConns = 10
	}
	pgxCfg.MinConns = cfg.MinConns

	// --- Pool Lifecycle ---
	if cfg.HealthChecksDisabled {
		pgxCfg.HealthCheckPeriod = 0
	} else if cfg.HealthCheckPeriod > 0 {
		pgxCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	} else {
		pgxCfg.HealthCheckPeriod = 30 * time.Second
	}

	if cfg.MaxConnLifetime > 0 {
		pgxCfg.MaxConnLifetime = cfg.MaxConnLifetime
	} else {
		pgxCfg.MaxConnLifetime = 30 * time.Minute
	}

	if cfg.MaxConnIdleTime > 0 {
		pgxCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	} else {
		pgxCfg.MaxConnIdleTime = 5 * time.Minute
	}

	if cfg.ConnectTimeout > 0 {
		pgxCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	} else {
		pgxCfg.ConnConfig.ConnectTimeout = 10 * time.Second
	}

	// --- Apply Options (Advanced) ---
	var o connectOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.pgxConfigModifier != nil {
		o.pgxConfigModifier(pgxCfg)
	}

	// --- Create Pool ---
	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		// SECURITY: host is safe; do not include connection string.
		return nil, &SafeError{
			msg:   fmt.Sprintf("neon: failed to create pool (host=%s)", host),
			cause: err,
		}
	}

	// --- Verify Connectivity ---
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, &SafeError{
			msg:   fmt.Sprintf("neon: initial ping failed (host=%s, is your Neon compute active?)", host),
			cause: err,
		}
	}

	return &Pool{pool: pool, directURL: directURL}, nil
}

// resolveDirectURL determines the direct (non-pooled) URL for migrations.
//
// Resolution order:
//  1. If cfg.DirectURL is set, use it verbatim.
//  2. If ConnectionString hostname matches Neon's pooled naming convention
//     (first hostname label ends with "-pooler" and suffix is ".neon.tech"),
//     derive direct URL by removing the "-pooler" suffix from the first label.
//  3. Otherwise, use ConnectionString as-is (assumed to be direct already).
//
// Non-Neon hostnames are NEVER rewritten.
//
// IMPORTANT: if ConnectionString is a Neon pooler host but cannot be safely
// rewritten (for example it is not URL-form parseable), resolveDirectURL
// returns an error. This prevents Pool.DirectURL() from ever returning a
// pooled DSN (which would violate the "direct-only migrations" invariant).
func resolveDirectURL(cfg Config, parsedHost string) (string, error) {
	if cfg.DirectURL != "" {
		return cfg.DirectURL, nil
	}

	if isNeonPoolerHost(parsedHost) {
		u, err := url.Parse(cfg.ConnectionString)
		if err != nil {
			return "", errors.New(
				"neon: ConnectionString is a Neon pooler URL, but is not URL-form parseable. " +
					"Set Config.DirectURL (direct/non-pooled URL) explicitly for migrations.",
			)
		}

		host := u.Hostname()
		port := u.Port()

		firstLabel, rest, ok := strings.Cut(host, ".")
		if !ok || !strings.HasSuffix(firstLabel, "-pooler") {
			return "", errors.New("neon: unexpected pooler hostname format; set Config.DirectURL explicitly")
		}
		directFirstLabel := strings.TrimSuffix(firstLabel, "-pooler")
		if directFirstLabel == "" {
			return "", errors.New("neon: unexpected pooler hostname format; set Config.DirectURL explicitly")
		}
		directHost := directFirstLabel + "." + rest

		if port != "" {
			u.Host = net.JoinHostPort(directHost, port)
		} else {
			u.Host = directHost
		}

		return u.String(), nil
	}

	return cfg.ConnectionString, nil
}

// DirectURL returns the resolved direct (non-pooled) connection string
// for use in migration tooling. This URL bypasses PgBouncer and supports
// session-level features (prepared statements, advisory locks, LISTEN/NOTIFY).
//
// The returned string contains credentials. Treat it as secret material:
// do not log it, print it to stdout, or include it in error messages.
func (p *Pool) DirectURL() string {
	return p.directURL
}

// Stat returns a snapshot of pool statistics (acquired, idle, total
// connections, etc.). Use for monitoring and capacity planning.
//
// Stat is on the concrete Pool type, not the DB interface, because it
// is an operational concern, not a data access concern.
func (p *Pool) Stat() *pgxpool.Stat {
	return p.pool.Stat()
}

// --- DB Interface Implementation ---

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
```

---

## 1.4 Free Functions (Helpers)

These functions work against the `DB` interface, preserving facade integrity.

### 1.4.1 Health Check

```go
package neon

import (
	"context"
)

// HealthStatus is the response type for health check endpoints.
type HealthStatus struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// HealthCheck verifies database connectivity and returns a status
// suitable for Vango typed API endpoints.
//
// This is a free function (not a method on Pool) so it works with
// the DB interface, including TestDB in tests.
//
// Registration example:
//
//	app.API("GET", "/api/health/db", func(ctx vango.Ctx) (*neon.HealthStatus, error) {
//	    return neon.HealthCheck(ctx.StdContext(), routes.GetDeps().DB)
//	})
func HealthCheck(ctx context.Context, db DB) (*HealthStatus, error) {
	if err := db.Ping(ctx); err != nil {
		return nil, &SafeError{msg: "neon: health check failed", cause: err}
	}
	return &HealthStatus{Status: "ok", Database: "neon"}, nil
}
```

### 1.4.2 WithTx (Transaction Helper)

```go
package neon

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultRollbackTimeout = 5 * time.Second

// WithTx executes fn within a transaction. If fn returns an error or panics,
// the transaction is rolled back. Otherwise, it is committed.
//
// This eliminates the most common transaction footgun: forgetting to call
// Rollback on the error path, or calling Rollback after Commit.
//
// Use BeginTx options for isolation levels or read-only transactions:
//
//	err := neon.WithTx(ctx, db, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
//	    _, err := tx.Exec(ctx, "UPDATE accounts SET balance = balance - $1 WHERE id = $2", amount, from)
//	    if err != nil {
//	        return err
//	    }
//	    _, err = tx.Exec(ctx, "UPDATE accounts SET balance = balance + $1 WHERE id = $2", amount, to)
//	    return err
//	})
//
// Pass pgx.TxOptions{} for default transaction options.
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
			panic(p) // re-panic after rollback
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
```

---

## 1.5 The Test Kit

All test utilities live in `github.com/vango-go/vango-neon`. No subpackage, no build tags.

### 1.5.1 `TestDB`

```go
package neon

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotMocked is returned when a TestDB method is called without a
// corresponding Func field set. This prevents nil-pointer panics in
// standard pgx patterns (e.g., defer rows.Close() on nil Rows).
var ErrNotMocked = errors.New("neon.TestDB: method not mocked — set the corresponding Func field")

// TestDB is a mock implementation of DB for use in vtest and standard
// Go tests. Assign function fields to control per-test behavior.
//
// Design contract:
//   - Unset methods return ErrNotMocked.
//   - QueryRow returns *ErrRow when unset (never nil pgx.Row).
//   - Query returns *ErrRows when unset (never nil pgx.Rows).
//   - Begin/BeginTx return (nil, ErrNotMocked) when unset; callers MUST
//     check error before using the returned pgx.Tx.
//   - Close is a no-op by default (safe for tests that don't test shutdown).
//   - Ping succeeds by default (returns nil).
//
// TestDB lives in the main neon package (github.com/vango-go/vango-neon).
// Import it in test files alongside the DB interface.
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
	return nil // Ping succeeds by default
}

func (t *TestDB) Close() {
	if t.CloseFunc != nil {
		t.CloseFunc()
	}
	// No-op by default
}
```

### 1.5.2 `ErrRow` (Exported Sentinel)

```go
package neon

// ErrRow implements pgx.Row. Its Scan method always returns the stored
// error. Use this as the return value for unmocked QueryRow calls and
// for testing error paths.
//
// Example:
//
//	mock := &neon.TestDB{
//	    QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
//	        return &neon.ErrRow{Err: pgx.ErrNoRows}
//	    },
//	}
type ErrRow struct {
	Err error
}

func (r *ErrRow) Scan(dest ...any) error {
	return r.Err
}
```

### 1.5.3 `NewRow` (Single-Row Helper)

`pgx.Row` is an interface, which makes it awkward to return successful rows
from `TestDB.QueryRowFunc`. `NewRow` is a minimal helper for the common case:
returning a single row with basic scalar values.

```go
package neon

import (
	"fmt"

	"github.com/jackc/pgx/v5"
)

// NewRow returns a pgx.Row that scans from the provided values.
//
// Supported scan targets:
//   - *string, *int, *int64, *bool, *float64, *any
//
// For complex types (custom scanners, json, arrays), prefer using a real
// database in integration tests or write a dedicated mock row.
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
		switch d := dest[i].(type) {
		case *string:
			v, ok := val.(string)
			if !ok {
				return fmt.Errorf("neon.valueRow: expected string at column %d, got %T", i, val)
			}
			*d = v
		case *int:
			v, ok := val.(int)
			if !ok {
				return fmt.Errorf("neon.valueRow: expected int at column %d, got %T", i, val)
			}
			*d = v
		case *int64:
			v, ok := val.(int64)
			if !ok {
				return fmt.Errorf("neon.valueRow: expected int64 at column %d, got %T", i, val)
			}
			*d = v
		case *bool:
			v, ok := val.(bool)
			if !ok {
				return fmt.Errorf("neon.valueRow: expected bool at column %d, got %T", i, val)
			}
			*d = v
		case *float64:
			v, ok := val.(float64)
			if !ok {
				return fmt.Errorf("neon.valueRow: expected float64 at column %d, got %T", i, val)
			}
			*d = v
		case *any:
			*d = val
		default:
			return fmt.Errorf("neon.valueRow: unsupported scan target type %T at column %d", dest[i], i)
		}
	}
	return nil
}
```

### 1.5.4 `ErrRows` (Exported Sentinel)

`pgx.Rows` is commonly used with `defer rows.Close()`. `ErrRows` is a safe
sentinel that implements `pgx.Rows` and always returns the stored error.

```go
package neon

import (
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrRows implements pgx.Rows. It is safe to Close, safe to call Next, and
// returns the stored error from Err().
type ErrRows struct {
	// ErrValue is the error returned by Err(), Scan(), and Values().
	//
	// NOTE: this field cannot be named "Err" because pgx.Rows requires an Err()
	// method, and Go does not allow a field and method with the same name on a
	// single type.
	ErrValue error
}

func (r *ErrRows) Close()                        {}
func (r *ErrRows) Err() error                    { return r.ErrValue }
func (r *ErrRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *ErrRows) Conn() *pgx.Conn               { return nil }
func (r *ErrRows) RawValues() [][]byte           { return nil }

func (r *ErrRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *ErrRows) Next() bool                                  { return false }
func (r *ErrRows) Values() ([]any, error)                       { return nil, r.ErrValue }
func (r *ErrRows) Scan(dest ...any) error {
	if r.ErrValue != nil {
		return r.ErrValue
	}
	return fmt.Errorf("neon.ErrRows: Scan called with nil ErrValue")
}
```

### 1.5.5 `RowsBuilder` (Test Row Construction)

Building fake `pgx.Rows` implementations is tedious and error-prone. `RowsBuilder` provides a minimal helper for the most common case: returning a fixed set of rows with known columns.

```go
package neon

import (
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RowsBuilder constructs a fake pgx.Rows from in-memory data.
// Use in tests to avoid implementing the full pgx.Rows interface manually.
//
// Example:
//
//	rows := neon.NewRows([]string{"id", "name"}).
//	    AddRow(1, "Alice").
//	    AddRow(2, "Bob").
//	    Build()
//
//	mock := &neon.TestDB{
//	    QueryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
//	        return rows, nil
//	    },
//	}
type RowsBuilder struct {
	columns []string
	rows    [][]any
}

func NewRows(columns []string) *RowsBuilder {
	return &RowsBuilder{columns: columns}
}

func (b *RowsBuilder) AddRow(values ...any) *RowsBuilder {
	if len(values) != len(b.columns) {
		panic("neon.RowsBuilder: column count mismatch")
	}
	b.rows = append(b.rows, values)
	return b
}

// Build returns a pgx.Rows implementation backed by the in-memory data.
// The returned Rows is safe for a single iteration (not reusable).
func (b *RowsBuilder) Build() pgx.Rows {
	return &fakeRows{
		columns: b.columns,
		data:    b.rows,
		idx:     -1,
	}
}

// --- fakeRows implements pgx.Rows ---

type fakeRows struct {
	columns []string
	data    [][]any
	idx     int
	closed  bool
	scanErr error
}

func (r *fakeRows) Close()                        { r.closed = true }
func (r *fakeRows) Err() error                    { return r.scanErr }
func (r *fakeRows) CommandTag() pgconn.CommandTag  { return pgconn.CommandTag{} }
func (r *fakeRows) Conn() *pgx.Conn               { return nil }
func (r *fakeRows) RawValues() [][]byte            { return nil }

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(r.columns))
	for i, col := range r.columns {
		fds[i] = pgconn.FieldDescription{Name: col}
	}
	return fds
}

func (r *fakeRows) Next() bool {
	if r.closed {
		return false
	}
	r.idx++
	return r.idx < len(r.data)
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.data) {
		return pgx.ErrNoRows
	}
	row := r.data[r.idx]
	if len(dest) != len(row) {
		return fmt.Errorf("neon.fakeRows: scan dest count %d != column count %d", len(dest), len(row))
	}
	for i, val := range row {
		// Simple assignment; works for basic types. Complex types
		// (custom scanners, json, arrays) need real pgx or manual mocks.
		switch d := dest[i].(type) {
		case *string:
			v, ok := val.(string)
			if !ok {
				return fmt.Errorf("neon.fakeRows: expected string at column %d, got %T", i, val)
			}
			*d = v
		case *int:
			v, ok := val.(int)
			if !ok {
				return fmt.Errorf("neon.fakeRows: expected int at column %d, got %T", i, val)
			}
			*d = v
		case *int64:
			v, ok := val.(int64)
			if !ok {
				return fmt.Errorf("neon.fakeRows: expected int64 at column %d, got %T", i, val)
			}
			*d = v
		case *bool:
			v, ok := val.(bool)
			if !ok {
				return fmt.Errorf("neon.fakeRows: expected bool at column %d, got %T", i, val)
			}
			*d = v
		case *float64:
			v, ok := val.(float64)
			if !ok {
				return fmt.Errorf("neon.fakeRows: expected float64 at column %d, got %T", i, val)
			}
			*d = v
		case *any:
			*d = val
		default:
			return fmt.Errorf("neon.fakeRows: unsupported scan target type %T at column %d", dest[i], i)
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
```

---

## 1.6 Observability (Trace-First)

The package integrates with `pgx`'s built-in tracer interface to emit query spans. This aligns with Vango's trace propagation model (§45.3): the trace context flows from event → handler → Resource/Action work function → database query.

```go
package db

import (
	"context"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	neon "github.com/vango-go/vango-neon"
)

// pgx supports tracing by setting ConnConfig.Tracer (pgx.QueryTracer).
// `github.com/jackc/pgx/v5/tracelog` provides TraceLog: a ready-made tracer
// that logs query start/end events to your logger.
//
// Configure tracing via neon.WithPgxConfig so the rest of the application
// continues to depend only on neon.DB (not *pgxpool.Pool).
func ConnectWithTracing(ctx context.Context, logger *slog.Logger) (*neon.Pool, error) {
	return neon.Connect(ctx, neon.Config{
		ConnectionString: os.Getenv("DATABASE_URL"),
		DirectURL:        os.Getenv("DATABASE_URL_DIRECT"),
	}, neon.WithPgxConfig(func(c *pgxpool.Config) {
		c.ConnConfig.Tracer = &tracelog.TraceLog{
			Logger: tracelog.LoggerFunc(func(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
				// IMPORTANT: Decide what to log. SQL text and args may contain PII.
				// Default posture: drop `sql` and `args`.
				safe := make(map[string]any, len(data))
				for k, v := range data {
					if k == "sql" || k == "args" {
						continue
					}
					safe[k] = v
				}
				logger.Log(ctx, slog.LevelInfo, msg, "pgx_level", level.String(), "pgx", safe)
			}),
			LogLevel: tracelog.LogLevelInfo,
		}
	}))
}
```

---

# Part 2: Developer Guide Sections

---

## §37.6 Database Layer (Neon)

Vango recommends **Neon** as the default PostgreSQL provider. Neon's serverless architecture aligns with Vango's operational model in three specific ways:

**Single binary deployment.** Vango apps deploy as a single Go binary. Neon eliminates provisioning and managing a separate database server.

**Branch-per-PR workflows.** Neon's instant database branching maps onto Vango's warm/cold deploy classification, enabling isolated schema changes per feature branch.

**Connection proxy with multiplexing.** Neon's proxy sits between your application and the database compute, handling connection multiplexing at the infrastructure level. This complements Vango's conservative per-instance pool defaults.

The `vango-neon` package (`github.com/vango-go/vango-neon`) provides a production-hardened connection pool that enforces TLS, auto-detects pooler mode, resolves direct URLs for migrations, and ships a test kit for deterministic mocking.

### 37.6.1 The Access Rule (MUST)

> **All database I/O MUST occur inside Resource loaders or Action work functions.**
>
> Never access the database from Setup callbacks, render closures, event handlers, or lifecycle callbacks (OnMount, Effect, OnChange).

This rule is a direct consequence of Vango's session loop model (§2, §4). The session loop is single-threaded and must remain non-blocking. Database queries are blocking I/O. Resources and Actions exist to perform blocking I/O off-loop while keeping state mutations serialized.

### 37.6.2 Environment Configuration (MUST)

Neon provides two connection endpoints per branch. Both are required for a complete setup:

**Pooled connection** (for application queries):
```
DATABASE_URL="postgresql://user:pass@ep-name-pooler.region.aws.neon.tech/db?sslmode=require&channel_binding=require"
```

**Direct connection** (for migrations, LISTEN/NOTIFY, advisory locks):
```
DATABASE_URL_DIRECT="postgresql://user:pass@ep-name.region.aws.neon.tech/db?sslmode=require&channel_binding=require"
```

Note the difference: the pooled URL has `-pooler` in the hostname. The direct URL does not.

Obtain both from: **Neon Console → Project → Dashboard → Connect** (select "Pooled" and "Direct" tabs).

If you provide only `DATABASE_URL` with a pooled connection string, the `vango-neon` package auto-derives the direct URL by removing `-pooler` from the hostname. This derivation is applied only for Neon hostnames (`*.neon.tech`); non-Neon hostnames are never rewritten.

**Security defaults:** the recommended connection string includes `sslmode=require&channel_binding=require`. For additional hardening in production, upgrade to `sslmode=verify-full` (requires the Neon CA certificate). The package rejects any connection string that would result in plaintext connections.

**Compatibility note:** if you encounter connection issues in a constrained environment, remove `channel_binding=require` first and keep `sslmode=require` (or stricter). `channel_binding` is hardening, not the TLS requirement.

**Secrets handling:** connection strings contain passwords. Never commit them to version control. Never log them. Store in `.env` (development, in `.gitignore`), environment variables (production), or a secrets manager.

**URL derivation note:** automatic pooled→direct URL derivation requires URL-form connection strings (the `postgresql://...` form). If you use non-URL DSN formats, set `DirectURL` explicitly.

### 37.6.3 Dependency Injection Pattern (Canonical)

**`internal/db/db.go`** — Pool initialization:

```go
package db

import (
	"context"
	"os"

	neon "github.com/vango-go/vango-neon"
)

// Connect creates a Neon-optimized connection pool.
// Call once in cmd/server/main.go during startup.
//
// Returns neon.DB (the interface), not *neon.Pool, to ensure the rest
// of the application depends only on the contract.
func Connect(ctx context.Context) (neon.DB, error) {
	return neon.Connect(ctx, neon.Config{
		ConnectionString: os.Getenv("DATABASE_URL"),
		DirectURL:        os.Getenv("DATABASE_URL_DIRECT"), // empty is OK; auto-derived
	})
}

// ConnectPool creates the pool and returns the concrete type.
// Use when you need access to Pool-specific methods (Stat, DirectURL).
// Pass the pool as neon.DB to route dependencies.
func ConnectPool(ctx context.Context) (*neon.Pool, error) {
	return neon.Connect(ctx, neon.Config{
		ConnectionString: os.Getenv("DATABASE_URL"),
		DirectURL:        os.Getenv("DATABASE_URL_DIRECT"),
	})
}
```

**`app/routes/deps.go`** — Dependency struct:

```go
package routes

import neon "github.com/vango-go/vango-neon"

type Deps struct {
	DB neon.DB
}

var deps Deps

// SetDeps must be called exactly once at process startup, before the server
// begins handling requests.
//
// Tests that mutate deps MUST NOT run in parallel.
func SetDeps(d Deps) { deps = d }
func GetDeps() Deps  { return deps }
```

**`cmd/server/main.go`** — Wiring:

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/vango-go/vango"
	neon "github.com/vango-go/vango-neon"
	"myapp/app/routes"
	"myapp/internal/db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx := context.Background()

	// Create pool (concrete type for Stat/DirectURL access at startup).
	pool, err := db.ConnectPool(ctx)
	if err != nil {
		logger.Error("failed to connect to Neon", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Inject as interface into route dependencies.
	routes.SetDeps(routes.Deps{DB: pool})

	app, err := vango.New(vango.Config{Logger: logger})
	if err != nil {
		logger.Error("invalid vango config", "error", err)
		os.Exit(1)
	}

	// Health check endpoint uses the free function (works with DB interface).
	app.API("GET", "/api/health/db", func(ctx vango.Ctx) (*neon.HealthStatus, error) {
		return neon.HealthCheck(ctx.StdContext(), routes.GetDeps().DB)
	})

	routes.Register(app)

	stop, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting server", "addr", ":8080")
	if err := app.Run(stop, ":8080"); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
```

### 37.6.4 Using the Database in Resources and Actions

The database is consumed exclusively through the service layer, called from Resource loaders and Action work functions.

**Service layer** (`internal/services/projects.go`):

```go
package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	neon "github.com/vango-go/vango-neon"
)

type Project struct {
	ID   int
	Name string
}

type ProjectService struct {
	db neon.DB
}

func NewProjectService(db neon.DB) *ProjectService {
	return &ProjectService{db: db}
}

func (s *ProjectService) GetByID(ctx context.Context, id int) (*Project, error) {
	row := s.db.QueryRow(ctx,
		"SELECT id, name FROM projects WHERE id = $1", id)

	var p Project
	if err := row.Scan(&p.ID, &p.Name); err != nil {
		return nil, fmt.Errorf("project %d: %w", id, err)
	}
	return &p, nil
}
```

**Component** (Resource consuming the service):

```go
func ProjectPage(p ProjectPageProps) vango.Component {
	return vango.Setup(p, func(s vango.SetupCtx[ProjectPageProps]) vango.RenderFn {
		props := s.Props()
		projects := services.NewProjectService(routes.GetDeps().DB)

		project := setup.ResourceKeyed(&s,
			func() int { return props.Get().ProjectID },
			func(ctx context.Context, id int) (*services.Project, error) {
				// Off-loop. Blocking I/O is safe. Context carries cancellation.
				return projects.GetByID(ctx, id)
			},
		)

		return func() *vango.VNode {
			return project.Match(
				vango.OnLoading(func() *vango.VNode {
					return Div(Text("Loading…"))
				}),
				vango.OnError(func(err error) *vango.VNode {
					return Div(Class("text-red-600"), Text(err.Error()))
				}),
				vango.OnReady(func(pr *services.Project) *vango.VNode {
					return H1(Text(pr.Name))
				}),
			)
		}
	})
}
```

### 37.6.5 Transactions in Service Code

Use `neon.WithTx` for automatic rollback-on-error semantics:

```go
func (s *ProjectService) TransferOwnership(
	ctx context.Context, projectID, newOwnerID int,
) error {
	return neon.WithTx(ctx, s.db, pgx.TxOptions{
		IsoLevel: pgx.Serializable,
	}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"UPDATE projects SET owner_id = $1 WHERE id = $2",
			newOwnerID, projectID)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx,
			"INSERT INTO audit_log (project_id, action, actor_id) VALUES ($1, $2, $3)",
			projectID, "transfer_ownership", newOwnerID)
		return err
	})
}
```

`WithTx` handles Begin, Commit, and Rollback. If `fn` returns an error or panics, the transaction is rolled back automatically. If `fn` returns nil, the transaction is committed.

**Important:** Vango's `vango.TxNamed` (§18) batches reactive signal writes on the session loop. Database transactions (`pgx.Tx` / `neon.WithTx`) are for atomic SQL operations in your service layer. They serve different purposes at different layers.

---

## §37.7 Database Migrations

Vango recommends `pressly/goose` (v3+) for database migrations. Goose supports embedding SQL migrations into the Go binary, aligning with Vango's single-binary deployment model.

### 37.7.1 Critical Constraint: Migrations Require Direct Connections

Neon's pooled endpoints use PgBouncer in transaction mode, which does not support session-level features required by migration tools (advisory locks, prepared statements, temporary tables).

**Migrations MUST use the direct (non-pooled) connection string.**

The `vango-neon` package resolves the direct URL automatically (via `Config.DirectURL` or hostname derivation). Access it via `pool.DirectURL()`.

### 37.7.2 Project Structure

```
myapp/
├── cmd/
│   ├── server/
│   │   └── main.go
│   └── migrate/
│       └── main.go          # Standalone migration runner
├── internal/
│   └── db/
│       ├── db.go             # Pool initialization
│       ├── migrate.go        # Migration runner
│       └── migrations/
│           ├── 001_create_projects.sql
│           └── ...
```

### 37.7.3 Migration Runner

```go
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// MigrateUp runs all pending migrations using a DIRECT connection.
//
// directURL must be a non-pooled Neon connection string. Using a pooled
// connection will cause errors (advisory locks, prepared statements).
//
// Goose uses Postgres advisory locks to prevent concurrent migration
// attempts, making this safe for multi-instance startup (though pipeline
// migration is still preferred for production).
func MigrateUp(ctx context.Context, directURL string) error {
	if directURL == "" {
		return errors.New("migration: directURL is required (set DATABASE_URL_DIRECT)")
	}

	// Parse connection string to get pgx config.
	pgxCfg, err := pgxpool.ParseConfig(directURL)
	if err != nil {
		// SECURITY: Connection strings contain passwords. Do not wrap or
		// forward parse errors that may echo the raw DSN.
		return errors.New("migration: invalid direct connection string (DATABASE_URL_DIRECT)")
	}

	// Use pgx's stdlib adapter to create a *sql.DB for goose.
	// This opens a minimal connection specifically for migrations,
	// separate from the application pool.
	sqlDB := stdlib.OpenDB(*pgxCfg.ConnConfig)
	defer sqlDB.Close()

	goose.SetBaseFS(migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migration: set dialect: %w", err)
	}

	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("migration: up failed: %w", err)
	}

	return nil
}
```

### 37.7.4 When to Run Migrations

**Pattern A: Startup Migration (Development)**

Run migrations in `main.go` before creating the application pool. The `DirectURL` is resolved by the `vango-neon` package.

```go
func main() {
	// Resolve direct URL for migrations.
	// Option 1: Use explicit env var
	directURL := os.Getenv("DATABASE_URL_DIRECT")
	// Option 2: Use vango-neon's resolution (requires creating a Pool first)

	if err := db.MigrateUp(context.Background(), directURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Then create pool and start app...
}
```

Goose uses advisory locks, so concurrent startup of multiple instances is safe. However, a failing migration prevents the app from starting, which may cause boot loops in orchestration systems.

**Pattern B: Pipeline Migration (Production Recommended)**

Run migrations as a dedicated CI/CD step before the new binary rolls out.

`cmd/migrate/main.go`:

```go
package main

import (
	"context"
	"log/slog"
	"os"

	"myapp/internal/db"
)

func main() {
	directURL := os.Getenv("DATABASE_URL_DIRECT")
	if directURL == "" {
		slog.Error("DATABASE_URL_DIRECT is required for migrations")
		os.Exit(1)
	}

	if err := db.MigrateUp(context.Background(), directURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations complete")
}
```

```yaml
# CI step (runs BEFORE deploy)
- name: Run database migrations
  env:
    DATABASE_URL_DIRECT: ${{ secrets.DATABASE_URL_DIRECT }}
  run: go run ./cmd/migrate
```

**Recommendation:** Use Pattern A during local development. Use Pattern B for staging and production.

### 37.7.5 Migration Files

```sql
-- internal/db/migrations/001_create_projects.sql

-- +goose Up
CREATE TABLE projects (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    owner_id INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_projects_owner ON projects(owner_id);

-- +goose Down
DROP TABLE IF EXISTS projects;
```

Migration SQL should be static and deterministic. Do not interpolate untrusted input into migration files. In application code (Resources/Actions and service layer), always use parameterized queries.

---

## §37.8 Database Testing

### 37.8.1 Unit Testing with `neon.TestDB`

`neon.TestDB` ships in `github.com/vango-go/vango-neon`. No separate package, no build tags.

**Example: Testing a Resource-driven component:**

```go
func TestProjectPage_LoadsFromDB(t *testing.T) {
	mock := &neon.TestDB{
		QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if args[0].(int) == 42 {
				return neon.NewRow(42, "My Project")
			}
			return &neon.ErrRow{Err: pgx.ErrNoRows}
		},
	}

	routes.SetDeps(routes.Deps{DB: mock})

	h := vtest.New(t)
	m := vtest.Mount(h, ProjectPage, ProjectPageProps{ProjectID: 42})

	h.AwaitResource(m, "project")
	h.AssertTextByTestID(m, "project-name", "My Project")
}
```

**Example: Testing error handling:**

```go
func TestProjectPage_NotFound(t *testing.T) {
	mock := &neon.TestDB{
		QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return &neon.ErrRow{Err: pgx.ErrNoRows}
		},
	}

	routes.SetDeps(routes.Deps{DB: mock})

	h := vtest.New(t)
	m := vtest.Mount(h, ProjectPage, ProjectPageProps{ProjectID: 999})

	h.AwaitResource(m, "project")
	h.AssertExistsByTestID(m, "error-message")
}
```

### 37.8.2 Service Layer Unit Testing

Service functions accept `neon.DB` and `context.Context`. Test them directly without the Vango harness:

```go
func TestProjectService_GetByID(t *testing.T) {
	mock := &neon.TestDB{
		QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
			// Verify correct SQL and args
			if !strings.Contains(sql, "WHERE id = $1") {
				t.Errorf("unexpected SQL: %s", sql)
			}
			if args[0].(int) != 42 {
				return &neon.ErrRow{Err: pgx.ErrNoRows}
			}
			return neon.NewRow(42, "Test Project")
		},
	}

	svc := services.NewProjectService(mock)

	p, err := svc.GetByID(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "Test Project" {
		t.Errorf("got %q, want %q", p.Name, "Test Project")
	}
}
```

### 37.8.3 Integration Testing with Neon Branches

For tests requiring a real database (complex queries, constraint validation, transaction semantics), use ephemeral Neon branches:

```bash
# Create branch
neon branches create ci-run-$(date +%s) --project-id $NEON_PROJECT_ID --output json

# Run migrations against the branch (direct URL)
DATABASE_URL_DIRECT=$BRANCH_DIRECT_URL go run ./cmd/migrate

# Run integration tests
DATABASE_URL=$BRANCH_POOLED_URL go test -tags=integration ./...

# Delete branch
neon branches delete $BRANCH_ID --project-id $NEON_PROJECT_ID
```

Gate integration tests behind a build tag:

```go
//go:build integration

package services_test
// ...
```

They should not run during `vango test` or standard `go test ./...`.

---

# Part 3: Appendix G — Neon Integration & Branching

---

## G.1 Why Neon is the Recommended Default

Neon is a serverless PostgreSQL provider whose architecture complements Vango's operational model:

- **Scale-to-zero compute** matches Vango's development workflow (frequent start/stop) and variable production traffic.
- **Instant database branching** enables isolated schema changes per feature branch, complementing Vango's warm/cold deploy classification.
- **Connection proxy** handles multiplexing at the infrastructure level, complementing Vango's conservative per-instance pool defaults.

---

## G.2 The Branching Workflow

### G.2.1 The Standard Flow

```
1. Create git branch
   └── git checkout -b feature/user-profiles

2. Create Neon branch (from main)
   └── neon branches create feature/user-profiles --project-id $NEON_PROJECT_ID

3. Update local .env with BOTH URLs for the new branch
   └── DATABASE_URL=postgresql://...@ep-branch-xyz-pooler.region.aws.neon.tech/db?sslmode=require&channel_binding=require
   └── DATABASE_URL_DIRECT=postgresql://...@ep-branch-xyz.region.aws.neon.tech/db?sslmode=require&channel_binding=require

4. Run migrations on the branch (uses direct URL)
   └── go run ./cmd/migrate

5. Develop normally
   └── vango dev
   └── Schema changes are isolated to the Neon branch

6. Before merge: verify compatibility
   └── neon branches schema-diff feature/user-profiles --project-id $NEON_PROJECT_ID
   └── vango state plan (checks Vango state schema: warm or cold?)

7. Merge PR
   └── CI runs migrations against main (direct URL)
   └── CI runs vango state plan and enforces cold deploy gate if needed
   └── Deploy

8. Cleanup
   └── neon branches delete feature/user-profiles --project-id $NEON_PROJECT_ID
```

### G.2.2 Deploy Classification Matrix

Database schema and Vango state schema are independent but often change together:

| Database change | Vango state change | Deploy classification | Action required |
|---|---|---|---|
| Additive (new tables/columns) | None | Warm | Standard deploy |
| Additive | Additive (new persisted signals) | Warm | Standard deploy |
| Additive | Breaking (removed persisted ID) | Cold (Vango) | Cold deploy ack |
| Breaking (dropped column) | None | Warm (Vango) but migration required | Run migration before deploy; deploy new code first if column was referenced |
| Breaking | Breaking | Cold | Both migration and cold deploy ack |

**Zero-downtime column removal pattern:**
1. Deploy new code that no longer references the column (warm deploy if Vango state unchanged).
2. Run migration that drops the column.
3. This ordering ensures no running binary references a dropped column.

### G.2.3 CI Integration

```yaml
# .github/workflows/ci.yml
jobs:
  test:
    runs-on: ubuntu-latest
    env:
      NEON_API_KEY: ${{ secrets.NEON_API_KEY }}
      NEON_PROJECT_ID: ${{ secrets.NEON_PROJECT_ID }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24.x"

      - name: Install tools
        run: |
          go install github.com/vango-go/vango/cmd/vango@v1.0.0
          npm i -g neonctl

      - name: Create ephemeral Neon branch
        id: neon
        run: |
          BRANCH="ci-${{ github.run_id }}-${{ github.run_attempt }}"
          # Create branch (output is JSON, contains connection info)
          neon branches create $BRANCH \
            --project-id $NEON_PROJECT_ID \
            --output json > /tmp/branch.json

          # Extract branch ID for cleanup
          echo "branch_id=$(jq -r '.id' /tmp/branch.json)" >> $GITHUB_OUTPUT

          # Get connection strings (SECRETS: pipe to env, never echo)
          CS=$(neon connection-string $BRANCH \
            --project-id $NEON_PROJECT_ID \
            --role-name neondb_owner \
            --pooled 2>/dev/null)
          echo "DATABASE_URL=$CS" >> $GITHUB_ENV

          CS_DIRECT=$(neon connection-string $BRANCH \
            --project-id $NEON_PROJECT_ID \
            --role-name neondb_owner 2>/dev/null)
          echo "DATABASE_URL_DIRECT=$CS_DIRECT" >> $GITHUB_ENV

      - name: Run migrations (direct URL)
        run: go run ./cmd/migrate

      - name: Vango state plan
        run: vango state plan --json > state_plan.json

      - name: Schema gate
        run: |
          IMPACT=$(jq -r '.schemaImpact.classification' state_plan.json)
          if [ "$IMPACT" = "cold_deploy" ] && [ ! -f vango_cold_deploy_ack.json ]; then
            echo "::error::Cold deploy detected without acknowledgment"
            exit 1
          fi

      - name: Run tests
        run: |
          vango gen bindings
          go test -race ./...

      - name: Run integration tests
        run: go test -tags=integration -race ./...

      - name: Cleanup Neon branch
        if: always()
        run: |
          neon branches delete ${{ steps.neon.outputs.branch_id }} \
            --project-id $NEON_PROJECT_ID 2>/dev/null || true
```

**Secrets hygiene:** Connection strings are piped directly to `$GITHUB_ENV` without echoing. `2>/dev/null` suppresses stderr that might contain connection details. The branch creation JSON is written to `/tmp`, not logged.

---

## G.3 CLI Integration

### G.3.1 `vango create --with neon` (Phase 2)

**If `neonctl` is installed and authenticated:**
1. Creates Neon project `vango-<appname>`.
2. Fetches pooled and direct connection strings for `main`.
3. Writes both to `.env` (with `.env` in `.gitignore`). Uses `--write-env` mode: values written to file, only redacted summary printed to stdout.
4. Generates `internal/db/db.go`, `internal/db/migrate.go`, `internal/db/migrations/`.
5. Updates `cmd/server/main.go` with pool initialization.
6. Updates `app/routes/deps.go` with `DB neon.DB`.
7. Creates `cmd/migrate/main.go`.

**If `neonctl` is not available:**
1. Generates code files as above.
2. Creates `.env` with placeholders.
3. Prints setup instructions.

**Non-interactive mode** (AI agents): accepts `--neon-connection-string` and `--neon-direct-url` flags.

**Stdout output (redacted):**
```
✅ Neon project created: vango-myapp
   Host: ep-cool-name-123456-pooler.us-east-2.aws.neon.tech
   Database: neondb
   Pooled: ✓  Direct: ✓
   Connection strings written to .env (not displayed for security)
```

### G.3.2 `vango dev` Branch Detection (Phase 3)

When `vango.json` has `"neon": { "enabled": true }`, `vango dev` performs an informational branch check at startup. This check **never** changes the database connection automatically.

**Scenario A: Matching branch exists, connected to main**
```
🟡  Neon branch detected
    Git: feature/user-profiles
    DB:  main (via DATABASE_URL)
    A matching Neon branch exists.

    To use isolated branch:
      vango dev --neon-branch=feature/user-profiles
    Or update .env manually.
```

**Scenario B: No matching branch**
```
ℹ️  No matching Neon branch for "feature/user-profiles"
    To create: neon branches create feature/user-profiles
```

**Scenario C: Already connected** — No message.

**Scenario D: `neonctl` not available** — Silently skipped.

### G.3.3 Long-Term CLI Architecture (Phase 3+)

To eliminate the hard dependency on `neonctl` (Node/npm), Vango will add native `vango neon` subcommands implemented in Go using Neon's HTTP API:

```bash
vango neon branch create feature/login    # Creates branch, outputs redacted summary
vango neon branch list                     # Lists branches
vango neon branch delete feature/login     # Deletes branch
vango neon connect --branch main --pooled  # Outputs connection string to .env
```

These commands use `NEON_API_KEY` for authentication and require no external dependencies. `neonctl` remains documented as an alternative, but Vango's critical path becomes a single Go binary.

---

## G.4 Operational Guardrails

### G.4.1 Connection Pooling

**The constraint chain:**
```
Vango sessions → Resource/Action work functions → pgxpool → Neon proxy → Neon compute
```

**Sizing guidance:**

| Deployment | Instances | Neon CU | Neon limit | Recommended MaxConns |
|---|---|---|---|---|
| Development | 1 | 0.25 | ~100 | 10 (default) |
| Small prod | 2 | 0.5 | ~200 | 20/instance |
| Medium prod | 5 | 1 | ~400 | 40/instance |
| Large prod | 10+ | 2+ | ~800+ | Measure and tune |

Monitor `pool.Stat()` (acquired/idle/total) and alert when utilization exceeds 80%.

### G.4.2 Cold Start Latency

Neon compute can scale to zero. First connection after suspension: 300ms–2000ms.

**Mitigation:**
1. **Always use `OnLoading`** in Resources (required regardless of cold starts).
2. **Increase auto-suspend delay** in Neon dashboard for production.
3. **Set `MinConns > 0`** to prevent suspension (trades cost for latency).
4. **Use readiness probes** pointing at `/api/health/db` to wake compute before user traffic.

### G.4.3 Pooler Mode and Prepared Statements

| Mode | Hostname pattern | Prepared statements | Use for |
|---|---|---|---|
| **Pooled** | `*-pooler.*.neon.tech` | No (SimpleProtocol auto-configured) | Application queries |
| **Direct** | `*.neon.tech` (no `-pooler`) | Yes | Migrations, LISTEN/NOTIFY, advisory locks |

The `vango-neon` package auto-detects pooler mode via hostname. If detection fails, set `ForcePoolerMode: true`.

In pooler mode, `vango-neon` also disables pgx statement and description caches (`StatementCacheCapacity=0`, `DescriptionCacheCapacity=0`) to keep behavior legible and avoid implying prepared-statement benefits.

**Migrations MUST always use the direct URL.** Using a pooled connection for migrations will cause errors with advisory locks and temporary tables.

### G.4.4 SSL and Channel Binding

| Level | Connection string params | Protection |
|---|---|---|
| Minimum (required) | `sslmode=require` | Encrypts traffic; no server identity verification |
| Recommended | `sslmode=require&channel_binding=require` | Encrypts + binds auth to TLS channel (MITM protection) |
| Maximum | `sslmode=verify-full&channel_binding=require` | Encrypts + verifies server certificate + channel binding |

The `vango-neon` package rejects connections without TLS. Scaffold templates default to `sslmode=require&channel_binding=require`.

---

## G.5 Neon CLI Reference (Agent-Oriented)

All commands support `--output json` for non-interactive, machine-parseable output. Connection strings returned by the CLI contain passwords — treat all output as secret material.

### G.5.1 Authentication

```bash
# Interactive
neon auth

# Non-interactive (CI/agents)
export NEON_API_KEY=<key>
```

### G.5.2 Projects

```bash
neon projects list --output json
neon projects create --name "my-vango-app" --output json
neon projects get <project-id> --output json
```

### G.5.3 Branches

```bash
neon branches list --project-id <pid> --output json
neon branches create <name> --project-id <pid> --output json
neon branches create <name> --project-id <pid> --parent <parent> --output json
neon branches schema-diff <name> --project-id <pid> --output json
neon branches delete <branch-id> --project-id <pid>
```

### G.5.4 Connection Strings

```bash
# Direct (for migrations)
neon connection-string <branch> --project-id <pid> --role-name <role>

# Pooled (for application)
neon connection-string <branch> --project-id <pid> --role-name <role> --pooled
```

**Security:** pipe output directly to environment variables or files. Never echo to logs.

### G.5.5 Context

```bash
neon set-context --project-id <pid>  # Avoids --project-id on every command
```

---

## G.6 Troubleshooting

### G.6.1 Symptom Table

| Scenario | Symptom | Root Cause | Resolution |
|---|---|---|---|
| **Cold start** | First Resource load: 500ms–2s; subsequent fast | Neon compute suspended | Expected. Use `OnLoading` states. Increase auto-suspend delay. Consider `MinConns > 0`. |
| **Prepared statement error** | `prepared statement "stmtcache_..." already exists` | Pooled connection without simple protocol | Use `vango-neon` (auto-detects). Set `ForcePoolerMode: true` if needed. |
| **Migration lock error** | `cannot obtain advisory lock` or migration hangs | Running migration through pooled connection | Use `DATABASE_URL_DIRECT` for migrations. Pooler does not support advisory locks. |
| **SSL rejection** | `neon: insecure connection rejected` | Missing `sslmode=require` (or using `sslmode=allow/prefer`) | Set `sslmode=require` (or stricter) and avoid plaintext fallback. Recommended: `?sslmode=require&channel_binding=require`. |
| **TLS handshake fail** | Timeout or certificate error | Proxy/firewall stripping TLS | Check network path. Verify no MITM proxy. |
| **Schema drift** | `column "x" does not exist` | Migrations not run on connected branch | Run `go run ./cmd/migrate` with correct `DATABASE_URL_DIRECT`. Use `neon branches schema-diff`. |
| **Orphaned branch** | `vango dev` warns; Neon branch deleted externally | Git branch exists but Neon branch gone | Create new: `neon branches create <n>`. Or switch `.env` to `main`. |
| **Connection exhaustion** | Timeouts; `pgxpool: too many connections` | Pool too large, or connections leaked | Check `MaxConns`. Ensure `defer rows.Close()` everywhere. Ensure transactions committed/rolled back. Check Neon compute limit. |
| **Connection refused** | `dial tcp: connection refused` | Neon endpoint inactive or misconfigured | Check `DATABASE_URL`. Verify compute status in Neon dashboard. |
| **Slow queries** | Consistent high latency on specific Resources | Missing indexes, bad query plans | Not Neon-specific. `EXPLAIN ANALYZE`. Add indexes. Paginate. Check Neon query insights. |

### G.6.2 Diagnostic Checklist

**Application:**
- [ ] `DATABASE_URL` set? Includes `sslmode=require`?
- [ ] `DATABASE_URL_DIRECT` set (for migrations)?
- [ ] Using `vango-neon` package (not raw `pgx`)?
- [ ] All DB calls in Resource/Action work functions?
- [ ] All `Query()` results closed with `defer rows.Close()`?
- [ ] All transactions committed or rolled back? (Or using `neon.WithTx`?)

**Pool:**
- [ ] `MaxConns` appropriate for instance count?
- [ ] `pool.Stat()` — utilization healthy?

**Neon:**
- [ ] Compute endpoint active?
- [ ] Correct branch connected?
- [ ] Migrations up to date on this branch?
- [ ] Connection limit not exceeded?
- [ ] Using pooled URL for app, direct URL for migrations?

**Network:**
- [ ] Host reachable on port 5432?
- [ ] No proxy intercepting TLS?

---

## G.7 `vango.json` Neon Configuration

```json
{
  "neon": {
    "enabled": true,
    "project_id": "project-abc-123",
    "branch_detection": true
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enables Neon-aware CLI features. |
| `project_id` | string | `""` | Neon project ID. Overridden by `NEON_PROJECT_ID` env var. |
| `branch_detection` | bool | `true` | `vango dev` checks for matching Neon branches at startup. |

---

## G.8 Security

### Connection Strings
- Never commit to version control. Never log. Never print to stdout.
- `vango-neon` never includes credentials in error messages. Errors are safe to log and include at most the hostname when available.
- `Pool.DirectURL()` returns a credential-bearing URL. Treat it as secret material: never log it.
- `vango` CLI uses `--write-env` (writes to file, prints redacted summary) and never echoes credentials.

### API Keys
- Store `NEON_API_KEY` in CI secrets, not in code or config files.
- Use organization-scoped keys with minimal permissions.
- Rotate periodically via Neon Console.

### Branch Isolation
- Branches are copy-on-write snapshots: isolated data, shared compute.
- Deleting a branch permanently removes its data.
- For production-critical isolation, use separate Neon projects.

---

## G.9 Phased Rollout

| Phase | Deliverable | Enables |
|---|---|---|
| **1** (Now) | `vango-neon` package + Guide §37.6–§37.8 + Appendix G | Correct, idiomatic Neon usage with manual setup. Deterministic testing via `TestDB`. |
| **2** | `vango create --with neon` | New projects get correct wiring (dual URLs, pool, migrations, deps) out of the box. |
| **3** | `vango dev` branch detection + native `vango neon` subcommands | Branch-aware dev experience. Single-binary CLI (no `neonctl` dependency for critical path). |

Phase 1 is implementation-ready. All type signatures compile against `pgx` v5. All patterns are consistent with the Vango Developer Guide (v1).

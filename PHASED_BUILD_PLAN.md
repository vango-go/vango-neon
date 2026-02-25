# vango-neon: Phased Build Plan

**Package goal:** ship `github.com/vango-go/vango-neon`, a production-hardened Neon (Postgres) integration for Vango apps built on `pgx` v5, including a safe-by-default pool, migration ergonomics (direct URL resolution), and a deterministic test kit.

**Primary spec (source of truth):** `/Users/collinshill/Documents/vango/vango-neon/NEON_INTEGRATION_SPEC.md` (copy of `/Users/collinshill/Documents/vango/vango/NEON_INTEGRATION.md`).

This plan is intentionally “maximally thorough”: it includes repository scaffolding, implementation sequencing, test strategy, CI/release steps, and explicit acceptance criteria mapped to the spec’s **Design Invariants I1–I5**.

---

## 0) Scope, Non-Goals, and Constraints

### Glossary (terms used throughout this plan)
- **Pooled URL / pooler endpoint:** Neon connection string whose hostname first label ends with `-pooler` (e.g. `ep-foo-pooler.us-east-2.aws.neon.tech`). Intended for application traffic via PgBouncer/proxy multiplexing.
- **Direct URL / direct endpoint:** Neon connection string without `-pooler` (e.g. `ep-foo.us-east-2.aws.neon.tech`). Required for migrations and session-level Postgres features.
- **DSN:** database connection string. In this plan, “DSN leakage” means any public error string/log output containing credentials or the full URL.
- **Safe error:** an `error` whose `Error()` string is safe to log in production by default (no credentials / full DSNs), while still allowing `errors.Is/As` against the underlying cause via `Unwrap()`.

### In scope (this repo/package)
- Implement the **full public API** specified in `NEON_INTEGRATION_SPEC.md`:
  - `neon.DB` interface
  - `neon.Config`
  - `neon.Pool` + `neon.Connect` + options (`WithPgxConfig`)
  - `neon.SafeError`
  - Helpers: `neon.HealthCheck`, `neon.WithTx`
  - Test kit: `neon.TestDB`, `neon.ErrRow`, `neon.ErrRows`, `neon.NewRow`, `neon.RowsBuilder` (+ backing fake rows)
- Enforce security + operational guardrails:
  - TLS-only enforcement
  - pooler mode determinism (simple protocol + cache clamps)
  - safe-to-log errors by default (no credential-bearing DSNs)
  - direct-only migration URL derivation and access via `Pool.DirectURL()`
- Provide package-level documentation and runnable examples where feasible.

### Out of scope (belongs to Vango core repo)
- `vango create --with neon` scaffolding, `vango dev` branch detection, and native `vango neon` CLI subcommands (these are Phase 2/3 items in the spec but live in `/Users/collinshill/Documents/vango/vango`).

### Hard constraints (must not regress)
- **Spec invariants I1–I5 are acceptance criteria**, not suggestions.
- **No DSN/credential leakage** in error strings or default logs (I4).
- API must compile against **pgx v5** types exactly.
- The package should remain **Vango-independent** (no import of `github.com/vango-go/vango`).

---

## 1) Repository & Module Setup (Phase 0)

### Deliverables
- Go module initialized at import path `github.com/vango-go/vango-neon`.
- Minimal project hygiene: `README.md`, CI entrypoints, and a small example section.

### Tasks
1. Create `go.mod`:
   - `module github.com/vango-go/vango-neon`
   - Require `github.com/jackc/pgx/v5` (and submodules as needed via `go mod tidy`).
   - Pin a specific pgx v5 minor version initially and only upgrade intentionally.
2. Add a `README.md`:
   - Purpose + safety posture summary
   - Quickstart snippet for Vango dependency injection (from spec §37.6.3)
   - Explicit warning that `Pool.DirectURL()` returns credentials (treat as secret)
   - Link to `NEON_INTEGRATION_SPEC.md` for full contract
3. Add package docs:
   - `doc.go` with package-level invariants summary (I1-I5)
   - explicit statement that `vango-neon` is framework-adjacent but framework-independent
4. Decide/standardize file layout (see §2.2).
5. Add baseline CI workflow (if this repo is published independently):
   - `go test ./...`
   - `go vet ./...`
   - (Optional) `-race` if runtime budget allows
6. Add editor/build hygiene files (minimal):
   - `.gitignore` for local build artifacts only
   - optional `Makefile` targets (`test`, `test-race`, `vet`) if the team wants a stable command surface
7. Define branch/PR conventions for this repo:
   - every PR references invariant(s) touched
   - every PR includes test evidence section
   - no credential-bearing logs in CI output
8. Declare toolchain support:
   - set `go` directive in `go.mod` to the minimum supported version for this module (currently Go 1.24, constrained by the pinned `pgx/v5` dependency)
   - ensure CI uses the same Go version(s)
9. (Optional, but recommended if publishing) Add governance files:
   - `LICENSE` (align with Vango org policy)
   - `SECURITY.md` (DSN/secret handling expectations, disclosure path)
   - `.github/pull_request_template.md` with invariant + test checklist

### Exit criteria
- `go test ./...` passes locally.
- `go vet ./...` passes.
- `README.md` correctly describes dual-URL usage and error/secret posture.
- `doc.go` exists and codifies invariants + non-goals.

### Phase 0 quality gate (merge checklist)
- Module path and import path are correct and stable.
- No Vango runtime dependency introduced.
- Documentation includes pooled vs direct endpoint distinction with examples.
- CI entrypoint validates package on clean checkout.

### Phase 0.1 Spec parity reconciliation (do before writing “real” code)

The spec is authoritative on behavior, but some snippets can diverge from compile reality (Go naming collisions; upstream type shapes). Before implementing, do a short “spec parity” pass:

1. Create a tiny scratch file that mirrors the public types and compile-check against the pinned `pgx/v5`.
2. If a snippet is not literally compilable but intent is clear, implement the intent and record the canonical contract in this repo’s docs (`README.md` and/or `doc.go`).

Known “watch items” (verify with the pinned pgx version):
- `pgconn.FieldDescription.Name` type: ensure `RowsBuilder.FieldDescriptions()` uses the correct conversion (often `[]byte(col)`).
- `ErrRows` field naming: Go cannot have a field and method with the same name; if `pgx.Rows` requires `Err() error`, the backing field must not be named `Err`.
- URL parsing: `url.Parse` does not error for non-URL DSN formats; pooled-to-direct derivation must explicitly require `postgres://` or `postgresql://` scheme when `DirectURL` is absent.

Output of this reconciliation:
- Add a short `SPEC_NOTES.md` capturing any compile-level corrections to spec snippets (without changing the behavioral intent).

---

## 2) Core Package Implementation (Phase 1)

### 2.1 Public API surface (must match spec exactly)

Implement the following in package `neon`:
- `type DB interface { Exec; Query; QueryRow; Begin; BeginTx; Ping; Close }`
- `type Config struct { ConnectionString; DirectURL; MaxConns; MinConns; ForcePoolerMode; HealthChecksDisabled; HealthCheckPeriod; MaxConnLifetime; MaxConnIdleTime; ConnectTimeout }`
- `type Pool struct { /* wraps *pgxpool.Pool, does not embed */ }`
  - `DirectURL() string`
  - `Stat() *pgxpool.Stat`
  - implements `DB` methods
- `type SafeError struct { msg string; cause error }` with `Error()` safe and `Unwrap()` preserving cause
- `Connect(ctx, cfg, ...Option) (*Pool, error)`
  - Options include `WithPgxConfig(func(*pgxpool.Config))`
- Helpers:
  - `HealthCheck(ctx, db) (*HealthStatus, error)`
  - `WithTx(ctx, db, opts, fn) error` with rollback-on-error/panic semantics

### 2.1.1 Recommended implementation sequencing (PR-sized increments)

This is the highest-leverage way to avoid “big bang” integration bugs and keep review crisp:

1. **PR A — Module + API skeleton**
   - `go.mod` / `go.sum`
   - `DB` interface + `Config` type with doc comments
   - No networking yet; just compile and minimal docs
2. **PR B — Connect core + invariants I2/I3/I5**
   - Implement `Connect`, pooler detection, TLS enforcement, direct URL resolution
   - Unit tests for:
     - invalid DSN safe error
     - sslmode rejection cases
     - pooled→direct derivation behavior (no rewriting for non-Neon)
     - pooler mode config clamps (observed via `WithPgxConfig`)
3. **PR C — SafeError + error redaction proofs (invariant I4)**
   - Ensure all returned errors are safe to log by default
   - Add tests explicitly scanning `err.Error()` for DSN substrings/heuristics
4. **PR D — Helpers**
   - `HealthCheck` (+ tests)
   - `WithTx` (+ tests with a controlled `pgx.Tx` stub)
5. **PR E — Test kit**
   - `TestDB`, `ErrRow`, `ErrRows`, `NewRow`, `RowsBuilder` (+ tests)
6. **PR F — Documentation + examples**
   - README quickstart, migration guidance, tracing snippet
   - Optional `example_test.go` examples
7. **PR G — Optional integration tests**
   - `//go:build integration` suite gated by env vars

### 2.2 Recommended file layout (implementation-oriented)

Keep files small and “single-purpose”; suggested structure:
- `db.go` — `DB` interface
- `config.go` — `Config` defaults documentation (struct + comments)
- `connect.go` — `Connect`, `Option`, pooler detection, TLS enforcement, lifecycle knobs, direct URL resolution helpers
- `pool.go` — `Pool` type and DB method forwarding
- `errors.go` — `SafeError` (and any safe-message helpers)
- `helpers.go` — `HealthCheck`, `WithTx`
- `testkit.go` — `TestDB`, `ErrRow`, `ErrRows`, `NewRow`, `RowsBuilder`, `fakeRows`

(Exact filenames can differ; the key is keeping the responsibilities separated and testable.)

### 2.2.1 Symbol-level implementation checklist (authoritative to-do)

`db.go`
- `type DB interface`

`config.go`
- `type Config struct`
- comment defaults for each tunable and when to override

`connect.go`
- `type Option`
- `type connectOptions`
- `func WithPgxConfig(fn func(*pgxpool.Config)) Option`
- `func Connect(ctx context.Context, cfg Config, opts ...Option) (*Pool, error)`
- `func resolveDirectURL(cfg Config, parsedHost string) (string, error)`
- `func isNeonPoolerHost(host string) bool`

`pool.go`
- `type Pool struct`
- compile-time assertion `var _ DB = (*Pool)(nil)`
- DB forwarding methods: `Exec`, `Query`, `QueryRow`, `Begin`, `BeginTx`, `Ping`, `Close`
- operational methods: `DirectURL`, `Stat`

`errors.go`
- `type SafeError struct`
- `func (e *SafeError) Error() string`
- `func (e *SafeError) Unwrap() error`

`helpers.go`
- `type HealthStatus struct`
- `func HealthCheck(ctx context.Context, db DB) (*HealthStatus, error)`
- `const defaultRollbackTimeout`
- `func WithTx(ctx context.Context, db DB, opts pgx.TxOptions, fn func(pgx.Tx) error) (err error)`

`testkit.go`
- `var ErrNotMocked`
- `type TestDB struct` + DB methods
- `type ErrRow struct` + `Scan`
- `func NewRow(values ...any) pgx.Row`
- `type ErrRows struct` + `pgx.Rows` methods
- `type RowsBuilder struct` + `NewRows`, `AddRow`, `Build`
- internal `type fakeRows` + `pgx.Rows` methods

### 2.2.2 Internal helper guidance (private surface)

Recommended private helpers to keep `Connect` readable and testable:
- `validateTLS(cfg *pgxpool.Config) error`
- `applyPoolerMode(cfg *pgxpool.Config, force bool)`
- `applyPoolLimits(cfg *pgxpool.Config, c Config)`
- `applyLifecycleDefaults(cfg *pgxpool.Config, c Config)`
- `applyAdvancedOptions(cfg *pgxpool.Config, opts []Option)`

If these helpers are added, they should remain package-private and be tested through `Connect` behavior unless direct unit tests add clarity.

### 2.3 Design invariant mapping (implementation checklist)

**I1 (I/O boundary invariant):** cannot be enforced mechanically by this package, but must be:
- documented prominently in `README.md` + package doc comments (godoc)
- reinforced by making the “right” path easy (a clean `neon.DB` facade that’s used from Resource/Action work functions)

**I2 (direct-only migrations):**
- implement `resolveDirectURL(cfg, parsedHost)` exactly as specified:
  - if `DirectURL` set: use verbatim
  - else if pooler hostname: derive direct by stripping `-pooler` from first label **only when URL-form parseable**
  - else: directURL = `ConnectionString`
- expose resolved value as `Pool.DirectURL()` and document it as secret material
- URL-form parseable MUST mean:
  - `url.Parse` succeeds, and
  - parsed scheme is `postgres` or `postgresql`, and
  - parsed hostname is non-empty

**I3 (pooler-mode determinism):**
- pooler detection uses hostname only:
  - host suffix `.neon.tech`
  - first label ends with `-pooler`
- if pooler mode (or `ForcePoolerMode`):
  - `DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol`
  - `StatementCacheCapacity = 0`
  - `DescriptionCacheCapacity = 0`

**I4 (secrets & error-safety):**
- no error message should include credential-bearing DSNs
- any upstream errors that may contain secrets must remain only as `Unwrap()` causes
- error strings should include only safe context (e.g., `host=...`)
- **tests must prove** common failure paths do not include:
  - `postgres://` / `postgresql://`
  - `password=`
  - an `@` in a URL authority context (heuristic)

**I5 (TLS-only):**
- reject any configuration that could fall back to plaintext:
  - TLSConfig must be non-nil after parsing
  - all `Fallbacks` must have non-nil TLSConfig (reject sslmode=allow/prefer)
- document recommended hardening (`channel_binding=require`, and optional `verify-full`)

### 2.4 Operational defaults (must match spec)
- `MaxConns` default 10, `MinConns` default 0
- health checks default enabled, `HealthCheckPeriod` default 30s (unless disabled)
- `MaxConnLifetime` default 30m
- `MaxConnIdleTime` default 5m
- `ConnectTimeout` default 10s (tests should override to keep them fast)
- `Connect` performs an initial `Ping` and returns a safe error if it fails (and closes the pool)

### 2.5 Edge-case and failure-mode matrix (implementation detail)

- **Empty `ConnectionString`**:
  - Return deterministic validation error.
  - Do not attempt parse/connect.
- **Malformed URL DSN**:
  - Return generic safe parse error.
  - Never include raw parse error if it may echo DSN.
- **`sslmode=disable`**:
  - Reject as insecure.
- **`sslmode=prefer`/`allow`**:
  - Reject because fallbacks may be plaintext.
- **Non-Neon hostname with `-pooler` in some label**:
  - Must not trigger Neon-specific rewriting unless host suffix is `.neon.tech`.
- **Hostname with unexpected shape during derivation**:
  - Fail safe and require explicit `DirectURL`.
- **`DirectURL` provided but invalid**:
  - Connect may still succeed for app pool creation; migration failures are surfaced where direct URL is consumed.
  - Document this explicitly to avoid false confidence.
- **`WithPgxConfig` overrides safe defaults**:
  - Allowed by design.
  - README must call out that override responsibility transfers to caller.
- **Ping failure after pool creation**:
  - Always close pool before returning error.
  - Error remains safe; underlying cause preserved.

### 2.6 Observability integration contract

- `vango-neon` does not ship its own logging framework.
- Tracing is injected through pgx tracer hooks using `WithPgxConfig`.
- Examples must demonstrate redaction posture:
  - drop `sql` and `args` by default
  - include safe metadata (duration, command tag class, etc.)
- `Pool.Stat()` is read-only and intentionally outside `DB` interface to avoid leaking operational concerns into domain/service code.

### Exit criteria (Phase 1)
- Package compiles and exports the exact contract described in the spec.
- All invariants I2–I5 are implemented and unit-tested (I1 documented + examples).
- `go test ./...` passes.

---

## 3) Testing Strategy (Phase 1.5)

### 3.0 Test architecture and file map

Suggested test file split (keeps failures localized and understandable):
- `connect_tls_test.go` for TLS and sslmode behavior
- `connect_pooler_test.go` for pooler detection and protocol/cache clamps
- `connect_direct_url_test.go` for direct URL resolution matrix
- `errors_safe_test.go` for safe error redaction and unwrap behavior
- `helpers_tx_test.go` for `WithTx` behavior (commit/rollback/panic)
- `helpers_health_test.go` for `HealthCheck`
- `testkit_test.go` for `TestDB`, `ErrRow`, `ErrRows`, `NewRow`, `RowsBuilder`

### 3.0.1 Shared test helpers (recommended)

Add a small internal-only helper in tests (not exported) to keep the I4 tests consistent:
- `assertNoDSNLeak(t, err)` scans `err.Error()` for:
  - `postgres://`, `postgresql://`
  - `password=`
  - an `@` that plausibly indicates URL userinfo/authority (heuristic)

Also add:
- `assertSafeErrorWraps(t, err, want)` to prove `errors.Is/As` works through `SafeError` without forcing the outer string to contain sensitive info.

### 3.1 Unit tests (required; run by default)

Add focused, behavior-driven tests that do not require a real database:

**Config parsing / TLS enforcement (I5)**
- Missing/empty connection string returns the specified error.
- Invalid DSN returns a **generic safe error** (no DSN echo).
- `sslmode=disable` rejected.
- `sslmode=prefer` / `allow` rejected due to plaintext fallback.

**Pooler detection + clamped pgx knobs (I3)**
- For host `ep-foo-pooler....neon.tech`, the pgx config is modified:
  - `DefaultQueryExecMode` simple protocol
  - caches set to 0
- For non-pooler hosts, defaults remain untouched unless `ForcePoolerMode=true`.
- Implementation detail for testability: use `WithPgxConfig` in tests to *observe* final `pgxpool.Config` just before pool creation.
- Add a test proving ordering: `WithPgxConfig` runs after defaults and can override the default pooler-mode clamps (caller responsibility).

**Direct URL resolution (I2)**
- If `DirectURL` provided, use verbatim.
- If `ConnectionString` is Neon pooler URL and URL-form parseable, derived host removes `-pooler` only from the *first label*.
- If `ConnectionString` is Neon pooler host but not URL-form parseable (or parse fails), `Connect` fails fast asking for `DirectURL`.
- For non-Neon hosts: never rewrite.

**Safe errors (I4)**
- `Connect` failures that wrap upstream errors return safe messages that do not contain DSNs/credentials.
- `errors.Is/As` works through `SafeError` to match the underlying cause type.
- Cover both plain `error` causes and typed causes for `errors.As`.

**WithTx semantics**
- When `fn` returns nil: `Commit` is called; `Rollback` is not.
- When `fn` returns error: `Rollback` is called; `Commit` is not.
- When `fn` panics: `Rollback` is called and the panic propagates.
- Rollback uses a bounded background timeout (as specified) to avoid hanging shutdown paths.
- If commit fails, `WithTx` returns safe error wrapping commit cause.
- If begin fails, `WithTx` returns safe error wrapping begin cause.
- If rollback itself fails, function still returns original app/commit error (rollback failure is best-effort cleanup).

**Test kit contract**
- Unset `TestDB` methods return `ErrNotMocked` (and never return nil `pgx.Row`/`pgx.Rows`).
- `ErrRow.Scan` returns its stored error.
- `ErrRows` is safe to `Close`, `Next`, `Scan`, and reports stored error.
- `NewRow` scans supported scalar types correctly and errors on mismatched arity or unsupported scan targets.
- `RowsBuilder` builds `pgx.Rows` that iterates correctly, returns accurate field descriptions, and scans supported types.
- `RowsBuilder.AddRow` panics on column count mismatch (explicitly test panic message).
- Confirm `fakeRows.Values` and `fakeRows.Scan` return `pgx.ErrNoRows` when cursor invalid.

### 3.2 Integration tests (optional; behind build tag)

Add `//go:build integration` tests that require:
- `DATABASE_URL` (pooled)
- `DATABASE_URL_DIRECT` (direct)

Suggested integration coverage:
- `Connect` + `Ping` succeeds against real Neon.
- `pool.DirectURL()` works and (optionally) can be used for a direct-only operation in a migration-like context (kept minimal).

These tests should:
- be skipped automatically when env vars are missing
- never print DSNs
- use conservative timeouts to avoid CI flakiness
- clean up any branch-local objects created by tests

### Exit criteria (Phase 1.5)
- Default test suite is fast and deterministic (no network required).
- Integration suite runs only when explicitly enabled.
- All exported symbols have at least one direct behavior test.

### 3.3 Named test-case catalog (execution checklist)

Recommended baseline test names:
- `TestConnect_RequiresConnectionString`
- `TestConnect_InvalidConnectionString_IsSanitized`
- `TestConnect_RejectsInsecureTLSConfig`
- `TestConnect_RejectsPlaintextFallbackModes`
- `TestConnect_AppliesPoolerModeByHostname`
- `TestConnect_AppliesForcePoolerMode`
- `TestConnect_DoesNotUsePortHeuristic`
- `TestResolveDirectURL_UsesExplicitDirectURL`
- `TestResolveDirectURL_DerivesFromNeonPoolerURL`
- `TestResolveDirectURL_DoesNotRewriteNonNeonHost`
- `TestResolveDirectURL_FailsForUnrewritablePoolerHost`
- `TestConnect_PingFailureClosesPoolAndReturnsSafeError`
- `TestSafeError_UnwrapSupportsErrorsIsAs`
- `TestHealthCheck_ReturnsStatusOK`
- `TestHealthCheck_ReturnsSafeErrorOnPingFailure`
- `TestWithTx_CommitsOnSuccess`
- `TestWithTx_RollsBackOnFunctionError`
- `TestWithTx_RollsBackAndRepanicsOnPanic`
- `TestWithTx_WrapsBeginFailureAsSafeError`
- `TestWithTx_WrapsCommitFailureAsSafeError`
- `TestTestDB_UnsetMethodsReturnErrNotMocked`
- `TestErrRow_ScanReturnsStoredError`
- `TestErrRows_MethodContract`
- `TestNewRow_ScanSupportedTypes`
- `TestRowsBuilder_BuildAndIterate`
- `TestRowsBuilder_AddRowPanicsOnColumnMismatch`

---

## 3.4 Acceptance Criteria Matrix (Spec → Tests)

This section is the “audit trail”: it should be possible to read the spec invariants and immediately find the tests that prove them.

- **I2 Direct-only migrations**
  - Tests: direct URL resolution table tests (explicit DirectURL; pooled auto-derivation; non-Neon never rewritten; non-URL pooler connection rejected)
  - Public API proof: `Pool.DirectURL()` exists and returns the resolved direct DSN (documented as secret)
- **I3 Pooler-mode determinism**
  - Tests: pooler detection by hostname (no port heuristics), `ForcePoolerMode` override
  - Observation mechanism: assert final `pgxpool.Config` values via `WithPgxConfig`
- **I4 Secrets & error-safety**
  - Tests:
    - invalid DSN parse path returns generic safe error message
    - pool creation failure path returns safe message (host only)
    - ping failure path returns safe message and closes pool
    - string scanning for DSN/credential substrings across representative failure cases
    - `errors.Is/As` works through `SafeError`
- **I5 TLS-only**
  - Tests:
    - `sslmode=disable` rejected
    - `sslmode=prefer/allow` rejected due to plaintext fallback detection
    - “good” DSNs with `sslmode=require` accepted far enough to attempt connect (but can fail ping in unit tests with short timeout)

---

## 4) Documentation & Examples (Phase 2)

### Deliverables
- `README.md` includes:
  - dual-URL setup (`DATABASE_URL` + `DATABASE_URL_DIRECT`)
  - connection-string security guidance
  - “pooler mode” explanation and what the package enforces
  - migration runner guidance (goose + direct URL)
- Godoc comments are complete and match the spec language.
- Small examples (either in README or `example_test.go` style) for:
  - wiring `neon.DB` into app dependencies
  - `neon.HealthCheck`
  - `neon.WithTx`
  - using `WithPgxConfig` for tracing while suppressing SQL/args

### Exit criteria
- A developer can adopt the package by following the README without reading the full spec (but the spec remains authoritative).
- Godoc for every exported symbol explains safe usage and caveats.

### 4.1 Documentation artifact checklist

- `README.md`
  - Quickstart: connect + inject as `neon.DB`
  - Security posture and DSN handling
  - Migration guidance using direct URL
  - Test-kit quick reference
- `doc.go`
  - Invariants summary (I1-I5)
  - Scope/non-scope and integration philosophy
- `example_test.go` (or README examples)
  - `ExampleConnect`
  - `ExampleHealthCheck`
  - `ExampleWithTx`
  - `ExampleTestDB`
- `CHANGELOG.md` (if release process expects it)
  - include explicit callouts for behavior changes impacting safety defaults

---

## 5) Release Readiness (Phase 3)

### Versioning
- Start at `v0.x` until API stabilizes, or `v1.0.0` if we consider this contract locked by spec.
- Tag releases in Git with SemVer.

### CI/CD
- Add GitHub Actions workflow(s) (if publishing separately):
  - `go test ./...`
  - `go test -race ./...` (optional)
  - `go vet ./...`
- (Optional) `staticcheck` if the org standardizes on it.
- Add optional integration workflow trigger requiring explicit secrets/environment.
- Ensure CI redacts secrets and never prints DSNs.

### Security posture
- Add a short “Security” section in README:
  - explicit statement: no DSN printing/logging
  - guidance on `SafeError` unwrap handling
  - warning: query tracing must avoid logging SQL/args by default (PII)

### Exit criteria
- Release tags produce reproducible builds.
- CI is green on main for the default suite.
- A release candidate pass confirms all spec invariants and their tests are present.

### 5.1 Release gate checklist (must pass before tagging)

- All invariants I1-I5 documented and tested as applicable.
- `go test ./...` and `go vet ./...` pass on clean runner.
- No known DSN leakage in error-path snapshots.
- README examples compile (or are validated through example tests).
- Integration tests (if enabled) pass against a disposable Neon branch.
- Version tag and release notes summarize:
  - API surface
  - security posture
  - migration/direct URL behavior

---

## 5.2 Risk Register (What can go wrong, and how we prevent it)

- **R1: DSN leakage via upstream error messages**
  - Mitigation: never include upstream error strings in the outer `Error()`; keep them only in `Unwrap()`.
  - Mitigation: test suite asserts representative error strings do not contain DSN markers.
- **R2: Pooler detection false negatives/positives**
  - Mitigation: detection uses only `-pooler` + `.neon.tech` suffix per spec (no port heuristic).
  - Mitigation: escape hatch `ForcePoolerMode`.
  - Mitigation: unit tests cover hostname edge cases (`no dot`, missing suffix, empty first label after trim).
- **R3: Unit tests become slow/flaky due to real network**
  - Mitigation: default tests never require a real DB; use short `ConnectTimeout` when forcing a ping failure path.
  - Mitigation: integration suite behind build tag + env gates.
- **R4: pgx interface drift breaks `RowsBuilder`/`pgx.Tx` stubs**
  - Mitigation: pin/track pgx v5 in `go.mod`.
  - Mitigation: keep stubs minimal but complete; compile errors immediately reveal upstream API changes.
- **R5: Tracing accidentally logs PII**
  - Mitigation: examples suppress `sql` and `args` keys by default; document that logging SQL/args is an explicit choice.
- **R6: Interface drift between spec snippets and implementation**
  - Mitigation: maintain a “spec parity checklist” in PR template.
  - Mitigation: when spec text and compile reality diverge (for example field/method naming collisions), document the reason and codify one canonical contract in this repo README.

### 5.3 Operational readiness checks (post-release)

- Smoke test against a real Neon project:
  - pooled connect path
  - direct URL retrieval path
  - migration runner compatibility
- Validate troubleshooting playbook from Appendix G against observed errors.
- Confirm default metrics/trace wiring guidance works in a sample Vango app without leaking query text/args by default.

---

## 6) Follow-on Work in Vango Core (Spec Phase 2/3 Alignment)

These are explicitly **not** in the `vango-neon` package, but this package’s release should unblock them:

1. `/Users/collinshill/Documents/vango/vango`:
   - `vango create --with neon` scaffolding (writes `.env` safely; generates db + migration runner)
2. `/Users/collinshill/Documents/vango/vango`:
   - `vango dev` Neon branch detection (informational only; no auto-mutation)
3. `/Users/collinshill/Documents/vango/vango`:
   - native `vango neon ...` subcommands via Neon HTTP API (remove hard dependency on `neonctl`)

Success metric: new Vango apps can choose Neon in one command, are wired correctly (dual URLs + safe defaults), and never accidentally run migrations through pooled endpoints.

---

## 7) Execution Model (How to run this plan end-to-end)

Use this strict sequencing to reduce rework:
1. Complete Phase 0 and merge.
2. Implement Phase 1 in PR A-G slices (from §2.1.1), merging each with tests.
3. Complete Phase 1.5 test matrix and lock acceptance criteria.
4. Complete Phase 2 docs/examples only after behavior is stable.
5. Run Phase 3 release gate and tag.

### 7.1 Done definition for the package

`vango-neon` is “done” for Phase 1 when:
- every exported symbol from Part 1 of the spec exists and is tested,
- invariants I2-I5 are mechanically enforced by code,
- invariant I1 is clearly enforced through documentation and usage patterns,
- failures are safe to log by default, and
- Vango app developers can adopt it with dual URL setup and migration-safe defaults.

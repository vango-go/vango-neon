// Package neon provides a production-hardened Neon Postgres integration for
// Vango applications using pgx v5.
//
// The package is framework-adjacent but framework-independent: it does not
// import github.com/vango-go/vango and can be used in plain Go services.
//
// Scope note:
//   - This module is the *library layer* (producer): it returns safe-by-default
//     outer errors and provides Neon-oriented connection/migration ergonomics.
//   - Vango runtime production logging policy (consumer-side suppression of
//     error chains) and any Vango CLI work (scaffolding / `vango neon ...`
//     commands) are intentionally out of scope for this package, even if they
//     are referenced in the integration spec as ecosystem guidance or future
//     work.
//
// Core API:
//   - DB: application-facing data access contract
//   - Config + Connect: Neon-oriented connection and pool setup
//   - Pool: concrete DB implementation with Stat() and DirectURL()
//   - SafeError: safe outer error wrapper for production logging defaults
//   - HealthCheck and WithTx: helper functions over the DB interface
//   - Test kit: TestDB, ErrRow, ErrRows, NewRow, RowsBuilder
//
// Invariants:
//   - I1: database I/O belongs in Resource loaders and Action work functions.
//   - I2: migration/session-level operations must use a direct (non-pooler) URL.
//   - I3: pooler mode is deterministic (simple protocol, caches disabled).
//   - I4: public outer errors are safe to log by default.
//   - I5: TLS is mandatory; plaintext fallback modes are rejected.
//
// Security notes:
//   - Connection strings and Pool.DirectURL() contain credentials.
//   - SafeError.Unwrap() may include sensitive upstream detail.
//   - Production logs should prefer safe outer error strings and metadata.
package neon

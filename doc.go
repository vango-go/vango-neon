// Package neon provides a production-hardened Neon Postgres integration for
// Vango applications using pgx v5.
//
// Invariants:
//
//   - I1: database I/O belongs in Resource loaders and Action work functions.
//   - I2: migration/session-level operations must use a direct (non-pooler) URL.
//   - I3: pooler mode is deterministic (simple protocol, caches disabled).
//   - I4: connect-path errors are safe to log by default.
//   - I5: TLS is mandatory; plaintext fallback modes are rejected.
//
// This package is framework-adjacent but framework-independent. It does not
// import github.com/vango-go/vango.
package neon

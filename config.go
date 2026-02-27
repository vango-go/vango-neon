package neon

import "time"

// Config controls the behavior of the Neon connection pool.
//
// All fields except ConnectionString have safe defaults optimized for Neon's
// serverless architecture (conservative pooling, scale-to-zero friendly).
type Config struct {
	// ConnectionString is the Neon database URL used for application queries.
	//
	// This may be a pooled endpoint (recommended for application traffic).
	// Format:
	//   postgresql://user:pass@ep-name-pooler.region.aws.neon.tech/db?sslmode=require&channel_binding=require
	//
	// TLS is mandatory. ConnectionString must include sslmode=require (or
	// stricter). The package rejects any configuration that can fall back to
	// plaintext (including sslmode=allow/prefer semantics via plaintext
	// fallbacks).
	//
	// Recommended hardening: channel_binding=require. If you encounter
	// connectivity issues in constrained environments, remove channel_binding
	// first and keep sslmode=require (or stricter).
	ConnectionString string

	// DirectURL is the Neon database URL used for operations that require a
	// direct (non-pooled) connection: schema migrations, LISTEN/NOTIFY,
	// advisory locks, and other session-level features.
	//
	// Format:
	//   postgresql://user:pass@ep-name.region.aws.neon.tech/db?sslmode=require&channel_binding=require
	//
	// If empty and ConnectionString is a Neon pooler URL (first hostname label
	// ends with "-pooler" and suffix is ".neon.tech"), the package derives the
	// direct URL by removing the "-pooler" suffix from the first label.
	//
	// Derivation is applied only for Neon hostnames and only when the
	// ConnectionString is provably URL-form parseable (postgres:// or
	// postgresql:// with a non-empty hostname). If ConnectionString is a Neon
	// pooler host but is not URL-form parseable (for example keyword/value DSN
	// format), Connect fails fast and requires DirectURL explicitly to uphold
	// the "direct-only migrations" invariant.
	//
	// If empty and ConnectionString is not a pooler URL, ConnectionString is
	// used for both pooled and direct operations.
	DirectURL string

	// MaxConns limits the maximum number of connections in the pool.
	// Default: 10.
	MaxConns int32

	// MinConns controls the minimum idle connections maintained.
	// Default: 0 (allows Neon compute to scale to zero).
	MinConns int32

	// ForcePoolerMode forces simple protocol (no prepared statements) and
	// disables pgx statement/description caches, regardless of hostname
	// detection.
	// Default: false.
	ForcePoolerMode bool

	// HealthChecksDisabled disables background health checks of idle
	// connections.
	// Default: false.
	//
	// If true, HealthCheckPeriod is ignored.
	HealthChecksDisabled bool

	// HealthCheckPeriod controls how often idle connections are health-checked.
	// Default: 30s.
	//
	// To disable health checks, set HealthChecksDisabled=true.
	HealthCheckPeriod time.Duration

	// MaxConnLifetime is the maximum lifetime of a connection before it is
	// closed and replaced.
	// Default: 30m.
	MaxConnLifetime time.Duration

	// MaxConnIdleTime is the maximum time a connection can sit idle before it
	// is closed.
	// Default: 5m.
	MaxConnIdleTime time.Duration

	// ConnectTimeout is the maximum time to wait for a new connection.
	// This is especially relevant for Neon cold starts (scale-from-zero).
	// Default: 10s.
	ConnectTimeout time.Duration
}

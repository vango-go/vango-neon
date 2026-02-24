package neon

import "time"

// Config controls the behavior of the Neon connection pool.
type Config struct {
	// ConnectionString is the application query URL.
	ConnectionString string

	// DirectURL is used for migrations/session-level operations.
	DirectURL string

	// MaxConns defaults to 10.
	MaxConns int32

	// MinConns defaults to 0.
	MinConns int32

	// ForcePoolerMode forces simple protocol + cache clamps.
	ForcePoolerMode bool

	// HealthChecksDisabled disables idle-connection health checks.
	HealthChecksDisabled bool

	// HealthCheckPeriod defaults to 30s when health checks are enabled.
	HealthCheckPeriod time.Duration

	// MaxConnLifetime defaults to 30m.
	MaxConnLifetime time.Duration

	// MaxConnIdleTime defaults to 5m.
	MaxConnIdleTime time.Duration

	// ConnectTimeout defaults to 10s.
	ConnectTimeout time.Duration
}

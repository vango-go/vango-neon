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
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option configures Connect for advanced use cases.
type Option func(*connectOptions)

type connectOptions struct {
	pgxConfigModifier func(*pgxpool.Config)
}

// newPoolWithConfig is a package-private seam used by tests to force
// deterministic pool-construction failures without network dependencies.
var newPoolWithConfig = pgxpool.NewWithConfig

// WithPgxConfig allows low-level pgxpool configuration.
//
// The modifier runs after standard vango-neon configuration is applied.
func WithPgxConfig(fn func(*pgxpool.Config)) Option {
	return func(o *connectOptions) {
		o.pgxConfigModifier = fn
	}
}

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

// Connect creates a production-hardened Neon connection pool.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*Pool, error) {
	if cfg.ConnectionString == "" {
		return nil, errors.New("neon: ConnectionString is required")
	}

	pgxCfg, err := pgxpool.ParseConfig(cfg.ConnectionString)
	if err != nil {
		// SECURITY: parse errors from upstream may contain DSN content.
		// Keep the outer error message sanitized.
		return nil, errors.New("neon: invalid connection string (expected URL form: postgresql://user:pass@host/db?... )")
	}

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

	host := pgxCfg.ConnConfig.Host
	isPooler := cfg.ForcePoolerMode || isNeonPoolerHost(host)
	if isPooler {
		pgxCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		pgxCfg.ConnConfig.StatementCacheCapacity = 0
		pgxCfg.ConnConfig.DescriptionCacheCapacity = 0
	}

	directURL, err := resolveDirectURL(cfg, host)
	if err != nil {
		return nil, err
	}

	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	} else {
		pgxCfg.MaxConns = 10
	}
	pgxCfg.MinConns = cfg.MinConns

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

	var o connectOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&o)
	}
	if o.pgxConfigModifier != nil {
		o.pgxConfigModifier(pgxCfg)
	}

	pool, err := newPoolWithConfig(ctx, pgxCfg)
	if err != nil {
		// SECURITY: cause may include sensitive details; keep outer error safe.
		return nil, &SafeError{
			msg:   fmt.Sprintf("neon: failed to create pool (host=%s)", host),
			cause: err,
		}
	}

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
func resolveDirectURL(cfg Config, parsedHost string) (string, error) {
	if cfg.DirectURL != "" {
		return cfg.DirectURL, nil
	}

	if isNeonPoolerHost(parsedHost) {
		u, err := url.Parse(cfg.ConnectionString)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
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

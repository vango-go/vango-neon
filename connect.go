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

const (
	defaultHealthCheckPeriod  = 30 * time.Second
	defaultMaxConnLifetime    = 30 * time.Minute
	defaultMaxConnIdleTime    = 5 * time.Minute
	defaultConnectTimeout     = 10 * time.Second
	disabledHealthCheckPeriod = 100 * 365 * 24 * time.Hour
)

// Option configures Connect for advanced use cases.
type Option func(*connectOptions)

type connectOptions struct {
	tracer        pgx.QueryTracer
	afterConnects []func(context.Context, *pgx.Conn) error
}

// newPoolWithConfig is a package-private seam used by tests to force
// deterministic pool-construction failures without network dependencies.
var newPoolWithConfig = pgxpool.NewWithConfig

// poolPing is a package-private seam used by tests to force deterministic
// ping failures without opening a real database connection.
var poolPing = func(ctx context.Context, pool *pgxpool.Pool) error {
	return pool.Ping(ctx)
}

// closePool is a package-private seam used by tests to avoid relying on the
// behavior of a zero-value pgxpool.Pool during ping-failure cleanup.
var closePool = func(pool *pgxpool.Pool) {
	pool.Close()
}

// WithTracer attaches a pgx tracer to all new connections created by the pool.
//
// The tracer is applied after vango-neon has configured the connection and
// cannot override TLS or pooler-mode invariants. When multiple tracers are
// provided, the last one wins.
func WithTracer(tracer pgx.QueryTracer) Option {
	return func(o *connectOptions) {
		if tracer == nil {
			return
		}
		o.tracer = tracer
	}
}

// WithAfterConnect registers a callback that runs after each new connection is
// established and before it is added to the pool.
//
// Use this for safe connection setup such as type registration. Callbacks run
// in registration order and stop on the first error. Nil callbacks are ignored.
func WithAfterConnect(fn func(context.Context, *pgx.Conn) error) Option {
	return func(o *connectOptions) {
		if fn == nil {
			return
		}
		o.afterConnects = append(o.afterConnects, fn)
	}
}

func applyPoolerModeInvariants(connCfg *pgx.ConnConfig) {
	connCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connCfg.StatementCacheCapacity = 0
	connCfg.DescriptionCacheCapacity = 0
}

func applyConnectOptions(pgxCfg *pgxpool.Config, opts connectOptions) {
	if opts.tracer != nil {
		pgxCfg.ConnConfig.Tracer = opts.tracer
	}

	if len(opts.afterConnects) == 0 {
		return
	}

	existingAfterConnect := pgxCfg.AfterConnect
	pgxCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if existingAfterConnect != nil {
			if err := existingAfterConnect(ctx, conn); err != nil {
				return err
			}
		}
		for _, fn := range opts.afterConnects {
			if err := fn(ctx, conn); err != nil {
				return err
			}
		}
		return nil
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

func enforceConnectionInvariants(connCfg *pgx.ConnConfig, isPooler bool) error {
	if err := validateTLSPosture("ConnectionString", connCfg); err != nil {
		return err
	}
	if isPooler {
		applyPoolerModeInvariants(connCfg)
	}
	return nil
}

func validateTLSPosture(source string, connCfg *pgx.ConnConfig) error {
	if connCfg.TLSConfig == nil {
		return fmt.Errorf(
			"neon: insecure connection rejected. %s must include sslmode=require (or stricter). "+
				"Recommended: sslmode=require&channel_binding=require",
			source,
		)
	}

	for _, fb := range connCfg.Fallbacks {
		if fb.TLSConfig == nil {
			return fmt.Errorf(
				"neon: insecure connection rejected. %s uses sslmode=allow/prefer semantics, "+
					"which are not permitted (plaintext fallback). "+
					"Use sslmode=require, sslmode=verify-ca, or sslmode=verify-full.",
				source,
			)
		}
	}

	return nil
}

func validateResolvedDirectURL(directURL string) error {
	directCfg, err := pgxpool.ParseConfig(directURL)
	if err != nil {
		// SECURITY: do not forward parse errors that may include the direct DSN.
		return errors.New(
			"neon: invalid DirectURL (must be a valid pgx DSN with sslmode=require or stricter)",
		)
	}

	if isNeonPoolerHost(directCfg.ConnConfig.Host) {
		return errors.New(
			`neon: DirectURL must be a direct (non-pooled) Neon endpoint (hostname must not include "-pooler")`,
		)
	}

	return validateTLSPosture("DirectURL", directCfg.ConnConfig)
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

	if err := validateTLSPosture("ConnectionString", pgxCfg.ConnConfig); err != nil {
		return nil, err
	}

	parsedHost := pgxCfg.ConnConfig.Host
	isPooler := cfg.ForcePoolerMode || isNeonPoolerHost(parsedHost)

	directURL, err := resolveDirectURL(cfg, parsedHost)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedDirectURL(directURL); err != nil {
		return nil, err
	}

	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	} else {
		pgxCfg.MaxConns = 10
	}
	pgxCfg.MinConns = cfg.MinConns

	if cfg.HealthChecksDisabled {
		// pgxpool requires a strictly positive ticker interval; a zero period
		// panics in backgroundHealthCheck. Use a very large positive duration to
		// effectively disable periodic health checks without risking process crash.
		pgxCfg.HealthCheckPeriod = disabledHealthCheckPeriod
	} else if cfg.HealthCheckPeriod > 0 {
		pgxCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	} else {
		pgxCfg.HealthCheckPeriod = defaultHealthCheckPeriod
	}

	if cfg.MaxConnLifetime > 0 {
		pgxCfg.MaxConnLifetime = cfg.MaxConnLifetime
	} else {
		pgxCfg.MaxConnLifetime = defaultMaxConnLifetime
	}

	if cfg.MaxConnIdleTime > 0 {
		pgxCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	} else {
		pgxCfg.MaxConnIdleTime = defaultMaxConnIdleTime
	}

	if cfg.ConnectTimeout > 0 {
		pgxCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	} else {
		pgxCfg.ConnConfig.ConnectTimeout = defaultConnectTimeout
	}

	if isPooler {
		applyPoolerModeInvariants(pgxCfg.ConnConfig)
	}

	var o connectOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&o)
	}

	applyConnectOptions(pgxCfg, o)

	if err := enforceConnectionInvariants(pgxCfg.ConnConfig, isPooler); err != nil {
		return nil, err
	}

	effectiveHost := pgxCfg.ConnConfig.Host

	pool, err := newPoolWithConfig(ctx, pgxCfg)
	if err != nil {
		// SECURITY: cause may include sensitive details; keep outer error safe.
		return nil, &SafeError{
			msg:   fmt.Sprintf("neon: failed to create pool (host=%s)", effectiveHost),
			cause: err,
		}
	}

	if err := poolPing(ctx, pool); err != nil {
		closePool(pool)
		return nil, &SafeError{
			msg:   fmt.Sprintf("neon: initial ping failed (host=%s, is your Neon compute active?)", effectiveHost),
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

	if cfg.ForcePoolerMode {
		return "", errors.New(
			"neon: ForcePoolerMode=true requires Config.DirectURL unless ConnectionString is a derivable Neon pooled URL",
		)
	}

	return cfg.ConnectionString, nil
}

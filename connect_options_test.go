package neon

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type noopTracer struct{}

func (noopTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (noopTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestConnect_WithTracerAttachesTracerWithoutChangingPoolerInvariants(t *testing.T) {
	errStop := errors.New("stop-before-pool")
	tracer := noopTracer{}
	var gotTracer pgx.QueryTracer
	var gotMode pgx.QueryExecMode
	var gotStmtCache int
	var gotDescCache int

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		gotTracer = cfg.ConnConfig.Tracer
		gotMode = cfg.ConnConfig.DefaultQueryExecMode
		gotStmtCache = cfg.ConnConfig.StatementCacheCapacity
		gotDescCache = cfg.ConnConfig.DescriptionCacheCapacity
		return nil, errStop
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, WithTracer(tracer))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errStop) {
		t.Fatalf("expected constructor error, got %v", err)
	}
	if gotTracer != tracer {
		t.Fatalf("tracer=%#v, want %#v", gotTracer, tracer)
	}
	if gotMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatalf("mode=%v, want %v", gotMode, pgx.QueryExecModeSimpleProtocol)
	}
	if gotStmtCache != 0 {
		t.Fatalf("statement cache=%d, want 0", gotStmtCache)
	}
	if gotDescCache != 0 {
		t.Fatalf("description cache=%d, want 0", gotDescCache)
	}
}

func TestConnect_WithAfterConnect_ComposesCallbacksInOrder(t *testing.T) {
	errStop := errors.New("stop-before-pool")
	var order []string
	var afterConnectErr error

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		if cfg.AfterConnect == nil {
			t.Fatal("expected AfterConnect to be configured")
		}
		afterConnectErr = cfg.AfterConnect(context.Background(), nil)
		return nil, errStop
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	secondErr := errors.New("second after connect failed")

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, WithAfterConnect(func(context.Context, *pgx.Conn) error {
		order = append(order, "first")
		return nil
	}), WithAfterConnect(func(context.Context, *pgx.Conn) error {
		order = append(order, "second")
		return secondErr
	}), WithAfterConnect(func(context.Context, *pgx.Conn) error {
		order = append(order, "third")
		return nil
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errStop) {
		t.Fatalf("expected constructor error, got %v", err)
	}
	if !errors.Is(afterConnectErr, secondErr) {
		t.Fatalf("after connect err=%v, want %v", afterConnectErr, secondErr)
	}
	if got, want := len(order), 2; got != want {
		t.Fatalf("callback count=%d, want %d", got, want)
	}
	if order[0] != "first" || order[1] != "second" {
		t.Fatalf("callback order=%v, want [first second]", order)
	}
}

func TestEnforceConnectionInvariants_RejectsMutatedTLSAndReappliesPoolerMode(t *testing.T) {
	t.Parallel()

	cfg, err := pgxpool.ParseConfig("postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	cfg.ConnConfig.StatementCacheCapacity = 41
	cfg.ConnConfig.DescriptionCacheCapacity = 42
	cfg.ConnConfig.TLSConfig = nil
	cfg.ConnConfig.Fallbacks = append(cfg.ConnConfig.Fallbacks, &pgconn.FallbackConfig{
		TLSConfig: &tls.Config{},
	})

	if err := enforceConnectionInvariants(cfg.ConnConfig, true); err == nil {
		t.Fatal("expected TLS validation failure")
	}
}

func TestEnforceConnectionInvariants_ReappliesPoolerMode(t *testing.T) {
	t.Parallel()

	cfg, err := pgxpool.ParseConfig("postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	cfg.ConnConfig.StatementCacheCapacity = 41
	cfg.ConnConfig.DescriptionCacheCapacity = 42

	if err := enforceConnectionInvariants(cfg.ConnConfig, true); err != nil {
		t.Fatalf("unexpected invariant enforcement error: %v", err)
	}
	if cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatalf("mode=%v, want %v", cfg.ConnConfig.DefaultQueryExecMode, pgx.QueryExecModeSimpleProtocol)
	}
	if cfg.ConnConfig.StatementCacheCapacity != 0 {
		t.Fatalf("statement cache=%d, want 0", cfg.ConnConfig.StatementCacheCapacity)
	}
	if cfg.ConnConfig.DescriptionCacheCapacity != 0 {
		t.Fatalf("description cache=%d, want 0", cfg.ConnConfig.DescriptionCacheCapacity)
	}
}

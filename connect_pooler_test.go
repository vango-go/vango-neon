package neon

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnect_AppliesPoolerModeByHostname(t *testing.T) {
	errStop := errors.New("stop-before-pool")
	var gotMode pgx.QueryExecMode
	var gotStmtCache int
	var gotDescCache int
	var called bool

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		called = true
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
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errStop) {
		t.Fatalf("expected constructor error, got %v", err)
	}
	if !called {
		t.Fatal("expected pool constructor to be called")
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

func TestConnect_AppliesForcePoolerMode(t *testing.T) {
	errStop := errors.New("stop-before-pool")
	var gotMode pgx.QueryExecMode
	var called bool

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		called = true
		gotMode = cfg.ConnConfig.DefaultQueryExecMode
		return nil, errStop
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		ForcePoolerMode:  true,
		DirectURL:        "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errStop) {
		t.Fatalf("expected constructor error, got %v", err)
	}
	if !called {
		t.Fatal("expected pool constructor to be called")
	}
	if gotMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatalf("mode=%v, want %v", gotMode, pgx.QueryExecModeSimpleProtocol)
	}
}

func TestConnect_DoesNotUsePortHeuristic(t *testing.T) {
	conn := "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech:6543/neondb?sslmode=require"
	baseline, err := pgxpool.ParseConfig(conn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	errStop := errors.New("stop-before-pool")
	var gotMode pgx.QueryExecMode
	var gotStmtCache int
	var gotDescCache int
	var called bool

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		called = true
		gotMode = cfg.ConnConfig.DefaultQueryExecMode
		gotStmtCache = cfg.ConnConfig.StatementCacheCapacity
		gotDescCache = cfg.ConnConfig.DescriptionCacheCapacity
		return nil, errStop
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err = Connect(context.Background(), Config{
		ConnectionString: conn,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errStop) {
		t.Fatalf("expected constructor error, got %v", err)
	}
	if !called {
		t.Fatal("expected pool constructor to be called")
	}
	if gotMode != baseline.ConnConfig.DefaultQueryExecMode {
		t.Fatalf("mode=%v, want baseline %v", gotMode, baseline.ConnConfig.DefaultQueryExecMode)
	}
	if gotStmtCache != baseline.ConnConfig.StatementCacheCapacity {
		t.Fatalf("statement cache=%d, want baseline %d", gotStmtCache, baseline.ConnConfig.StatementCacheCapacity)
	}
	if gotDescCache != baseline.ConnConfig.DescriptionCacheCapacity {
		t.Fatalf("description cache=%d, want baseline %d", gotDescCache, baseline.ConnConfig.DescriptionCacheCapacity)
	}
}

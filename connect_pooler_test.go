package neon

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnect_AppliesPoolerModeByHostname(t *testing.T) {
	t.Parallel()

	errStop := errors.New("stop-before-connect")
	var gotMode pgx.QueryExecMode
	var gotStmtCache int
	var gotDescCache int
	var called bool

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, WithPgxConfig(func(c *pgxpool.Config) {
		c.BeforeConnect = func(_ context.Context, cc *pgx.ConnConfig) error {
			called = true
			gotMode = cc.DefaultQueryExecMode
			gotStmtCache = cc.StatementCacheCapacity
			gotDescCache = cc.DescriptionCacheCapacity
			return errStop
		}
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Fatal("expected BeforeConnect to be called")
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
	t.Parallel()

	errStop := errors.New("stop-before-connect")
	var gotMode pgx.QueryExecMode
	var called bool

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		ForcePoolerMode:  true,
	}, WithPgxConfig(func(c *pgxpool.Config) {
		c.BeforeConnect = func(_ context.Context, cc *pgx.ConnConfig) error {
			called = true
			gotMode = cc.DefaultQueryExecMode
			return errStop
		}
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Fatal("expected BeforeConnect to be called")
	}
	if gotMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatalf("mode=%v, want %v", gotMode, pgx.QueryExecModeSimpleProtocol)
	}
}

func TestConnect_DoesNotUsePortHeuristic(t *testing.T) {
	t.Parallel()

	conn := "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech:6543/neondb?sslmode=require"
	baseline, err := pgxpool.ParseConfig(conn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	errStop := errors.New("stop-before-connect")
	var gotMode pgx.QueryExecMode
	var gotStmtCache int
	var gotDescCache int
	var called bool

	_, err = Connect(context.Background(), Config{
		ConnectionString: conn,
	}, WithPgxConfig(func(c *pgxpool.Config) {
		c.BeforeConnect = func(_ context.Context, cc *pgx.ConnConfig) error {
			called = true
			gotMode = cc.DefaultQueryExecMode
			gotStmtCache = cc.StatementCacheCapacity
			gotDescCache = cc.DescriptionCacheCapacity
			return errStop
		}
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Fatal("expected BeforeConnect to be called")
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

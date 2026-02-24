package neon

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnect_WithPgxConfigRunsAfterDefaultsAndCanOverride(t *testing.T) {
	t.Parallel()

	errStop := errors.New("stop-before-connect")
	var sawDefaults bool
	var gotMode pgx.QueryExecMode
	var gotStmtCache int
	var gotDescCache int
	var beforeConnectCalled bool

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, WithPgxConfig(func(c *pgxpool.Config) {
		if c.ConnConfig.DefaultQueryExecMode == pgx.QueryExecModeSimpleProtocol && c.ConnConfig.StatementCacheCapacity == 0 && c.ConnConfig.DescriptionCacheCapacity == 0 {
			sawDefaults = true
		}

		c.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
		c.ConnConfig.StatementCacheCapacity = 41
		c.ConnConfig.DescriptionCacheCapacity = 42
		c.BeforeConnect = func(_ context.Context, cc *pgx.ConnConfig) error {
			beforeConnectCalled = true
			gotMode = cc.DefaultQueryExecMode
			gotStmtCache = cc.StatementCacheCapacity
			gotDescCache = cc.DescriptionCacheCapacity
			return errStop
		}
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !sawDefaults {
		t.Fatal("expected WithPgxConfig to run after package defaults")
	}
	if !beforeConnectCalled {
		t.Fatal("expected BeforeConnect callback to run")
	}
	if gotMode != pgx.QueryExecModeCacheStatement {
		t.Fatalf("mode=%v, want %v", gotMode, pgx.QueryExecModeCacheStatement)
	}
	if gotStmtCache != 41 {
		t.Fatalf("statement cache=%d, want 41", gotStmtCache)
	}
	if gotDescCache != 42 {
		t.Fatalf("description cache=%d, want 42", gotDescCache)
	}
}

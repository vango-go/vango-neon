package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type typedCause struct{}

func (e *typedCause) Error() string { return "typed cause" }

func TestSafeError_UnwrapSupportsErrorsIsAs(t *testing.T) {
	t.Parallel()

	sentinel := &typedCause{}
	err := &SafeError{msg: "safe message", cause: sentinel}

	assertSafeErrorWraps(t, err, sentinel)

	var got *typedCause
	if !errors.As(err, &got) {
		t.Fatal("expected errors.As to extract typed wrapped cause")
	}
}

func TestConnect_PoolCreationFailure_ReturnsSafeErrorAndWrapsCause(t *testing.T) {
	cause := errors.New("constructor failed for postgresql://user:supersecret@bad.example.com/neondb")

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, _ *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, cause
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "neon: failed to create pool (host=ep-demo.us-east-2.aws.neon.tech)") {
		t.Fatalf("unexpected outer error: %q", err.Error())
	}
	assertSafeErrorWraps(t, err, cause)
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_PingFailure_ReturnsSafeErrorAndWrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("dial failed for postgresql://user:supersecret@bad.example.com/neondb")

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:supersecret@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, WithPgxConfig(func(c *pgxpool.Config) {
		c.BeforeConnect = func(_ context.Context, _ *pgx.ConnConfig) error {
			return cause
		}
	}))
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "neon: initial ping failed") {
		t.Fatalf("unexpected outer error: %q", err.Error())
	}
	assertSafeErrorWraps(t, err, cause)
	assertNoDSNLeak(t, err.Error())
}

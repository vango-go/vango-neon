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

	if !errors.Is(err, sentinel) {
		t.Fatal("expected errors.Is to match wrapped cause")
	}

	var got *typedCause
	if !errors.As(err, &got) {
		t.Fatal("expected errors.As to extract wrapped cause")
	}
}

func TestConnect_PingFailureClosesPoolAndReturnsSafeError(t *testing.T) {
	t.Parallel()

	errStop := errors.New("stop-before-connect")

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:supersecret@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, WithPgxConfig(func(c *pgxpool.Config) {
		c.BeforeConnect = func(_ context.Context, _ *pgx.ConnConfig) error {
			return errStop
		}
	}))
	if err == nil {
		t.Fatal("expected error")
	}

	var se *SafeError
	if !errors.As(err, &se) {
		t.Fatalf("expected SafeError, got %T", err)
	}
	if !strings.Contains(err.Error(), "neon: initial ping failed") {
		t.Fatalf("unexpected outer error: %q", err.Error())
	}
	if !errors.Is(err, errStop) {
		t.Fatal("expected wrapped cause to match sentinel")
	}
	assertNoSensitiveConnectError(t, err.Error())
}

func TestConnect_InvalidConnectionStringErrorIsSafe(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:supersecret@%zz/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	assertNoSensitiveConnectError(t, err.Error())
}

func assertNoSensitiveConnectError(t *testing.T, s string) {
	t.Helper()

	lower := strings.ToLower(s)
	for _, marker := range []string{"postgres://", "postgresql://", "password="} {
		if strings.Contains(lower, marker) {
			t.Fatalf("error leaked sensitive marker %q: %q", marker, s)
		}
	}
	if strings.Contains(s, "@") {
		t.Fatalf("error unexpectedly contains '@' authority marker: %q", s)
	}
}

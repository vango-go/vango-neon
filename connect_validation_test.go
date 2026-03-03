package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnect_RequiresConnectionString(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "neon: ConnectionString is required"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_InvalidConnectionString_IsSafeAndNoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:supersecret@%zz/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "neon: invalid connection string (expected URL form: postgresql://user:pass@host/db?... )"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_RejectsInsecureTLS_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@localhost/neondb?sslmode=disable",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "insecure connection rejected") {
		t.Fatalf("expected insecure rejection, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_RejectsPlaintextFallback_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@localhost/neondb?sslmode=prefer",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sslmode=allow/prefer") {
		t.Fatalf("expected fallback rejection, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_DirectURLDerivationFailure_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "host=ep-demo-pooler.us-east-2.aws.neon.tech user=user password=supersecret dbname=neondb sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not URL-form parseable") {
		t.Fatalf("expected direct-url derivation error, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_RejectsInsecureDirectURL_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		DirectURL:        "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=disable",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "insecure connection rejected") {
		t.Fatalf("expected insecure rejection, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DirectURL must include sslmode=require") {
		t.Fatalf("expected direct-url TLS requirement, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_RejectsPlaintextFallbackDirectURL_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		DirectURL:        "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=prefer",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "insecure connection rejected") {
		t.Fatalf("expected insecure rejection, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DirectURL uses sslmode=allow/prefer") {
		t.Fatalf("expected direct-url fallback rejection, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_InvalidDirectURL_IsSafeAndNoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		DirectURL:        "postgresql://user:supersecret@%zz/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "neon: invalid DirectURL (must be a valid pgx DSN with sslmode=require or stricter)"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_ValidDirectURL_ContinuesToPoolConstruction(t *testing.T) {
	cause := errors.New("pool constructor failed")

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, _ *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, cause
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		DirectURL:        "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to create pool") {
		t.Fatalf("expected pool-construction error path, got: %v", err)
	}
	assertSafeErrorWraps(t, err, cause)
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_RejectsPooledDirectURL_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		DirectURL:        "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "DirectURL must be a direct (non-pooled) Neon endpoint") {
		t.Fatalf("expected pooled-direct rejection, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_ForcePoolerModeWithoutDirectURL_FailsFast_NoLeak(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@localhost/neondb?sslmode=require",
		ForcePoolerMode:  true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ForcePoolerMode=true requires Config.DirectURL") {
		t.Fatalf("expected forced-mode direct-url guidance, got: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_ForcePoolerModeWithExplicitDirectURL_ContinuesToPoolConstruction(t *testing.T) {
	cause := errors.New("pool constructor failed")

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, _ *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, cause
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@localhost/neondb?sslmode=require",
		ForcePoolerMode:  true,
		DirectURL:        "postgresql://user:pass@localhost/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to create pool") {
		t.Fatalf("expected pool-construction error path, got: %v", err)
	}
	assertSafeErrorWraps(t, err, cause)
	assertNoDSNLeak(t, err.Error())
}

func TestConnect_ForcePoolerModeWithDerivableNeonPooler_AllowsDerivation(t *testing.T) {
	cause := errors.New("pool constructor failed")

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, _ *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, cause
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
		ForcePoolerMode:  true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "ForcePoolerMode=true requires Config.DirectURL") {
		t.Fatalf("unexpected forced-mode direct-url error for derivable neon pooler: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to create pool") {
		t.Fatalf("expected pool-construction error path, got: %v", err)
	}
	assertSafeErrorWraps(t, err, cause)
	assertNoDSNLeak(t, err.Error())
}

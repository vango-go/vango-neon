package neon

import (
	"context"
	"strings"
	"testing"
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

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
}

func TestConnect_InvalidConnectionString_IsSanitized(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{
		ConnectionString: "postgresql://user:supersecret@%zz/neondb?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "neon: invalid connection string (expected URL form)"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	assertNoSensitiveErrorContent(t, err.Error())
}

func TestConnect_RejectsInsecureTLSConfig(t *testing.T) {
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
}

func TestConnect_RejectsPlaintextFallbackModes(t *testing.T) {
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
}

func assertNoSensitiveErrorContent(t *testing.T, s string) {
	t.Helper()

	for _, marker := range []string{"postgres://", "postgresql://", "password="} {
		if strings.Contains(strings.ToLower(s), marker) {
			t.Fatalf("error leaked sensitive marker %q: %q", marker, s)
		}
	}
	if strings.Contains(s, "@") {
		t.Fatalf("error unexpectedly contains '@' authority marker: %q", s)
	}
}

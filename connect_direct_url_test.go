package neon

import (
	"strings"
	"testing"
)

func TestResolveDirectURL_UsesExplicitDirectURL(t *testing.T) {
	t.Parallel()

	got, err := resolveDirectURL(Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
		DirectURL:        "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, "ep-demo-pooler.us-east-2.aws.neon.tech")
	if err != nil {
		t.Fatalf("resolveDirectURL error: %v", err)
	}
	if want := "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require"; got != want {
		t.Fatalf("directURL=%q, want %q", got, want)
	}
}

func TestResolveDirectURL_DerivesFromNeonPoolerURL(t *testing.T) {
	t.Parallel()

	got, err := resolveDirectURL(Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	}, "ep-demo-pooler.us-east-2.aws.neon.tech")
	if err != nil {
		t.Fatalf("resolveDirectURL error: %v", err)
	}
	if want := "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require"; got != want {
		t.Fatalf("directURL=%q, want %q", got, want)
	}
}

func TestResolveDirectURL_DoesNotRewriteNonNeonHost(t *testing.T) {
	t.Parallel()

	conn := "postgresql://user:pass@my-pooler.db.example.com/neondb?sslmode=require"
	got, err := resolveDirectURL(Config{ConnectionString: conn}, "my-pooler.db.example.com")
	if err != nil {
		t.Fatalf("resolveDirectURL error: %v", err)
	}
	if got != conn {
		t.Fatalf("directURL=%q, want %q", got, conn)
	}
}

func TestResolveDirectURL_FailsForUnrewritablePoolerHost(t *testing.T) {
	t.Parallel()

	_, err := resolveDirectURL(Config{
		ConnectionString: "host=ep-demo-pooler.us-east-2.aws.neon.tech user=user password=secret dbname=neondb sslmode=require",
	}, "ep-demo-pooler.us-east-2.aws.neon.tech")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateResolvedDirectURL_RejectsNeonPoolerHost(t *testing.T) {
	t.Parallel()

	err := validateResolvedDirectURL(
		"postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "DirectURL must be a direct (non-pooled) Neon endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestResolveDirectURL_ForcePoolerModeWithoutDirectURL_Fails(t *testing.T) {
	t.Parallel()

	_, err := resolveDirectURL(Config{
		ConnectionString: "postgresql://user:pass@localhost/neondb?sslmode=require",
		ForcePoolerMode:  true,
	}, "localhost")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ForcePoolerMode=true requires Config.DirectURL") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNoDSNLeak(t, err.Error())
}

func TestResolveDirectURL_ForcePoolerMode_DerivableNeonPooler_StillDerives(t *testing.T) {
	t.Parallel()

	got, err := resolveDirectURL(Config{
		ConnectionString: "postgresql://user:pass@ep-demo-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require",
		ForcePoolerMode:  true,
	}, "ep-demo-pooler.us-east-2.aws.neon.tech")
	if err != nil {
		t.Fatalf("resolveDirectURL error: %v", err)
	}
	if want := "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require"; got != want {
		t.Fatalf("directURL=%q, want %q", got, want)
	}
}

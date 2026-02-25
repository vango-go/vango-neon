package neon

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

var dsnAuthorityPattern = regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s]+@`)

func assertNoDSNLeak(t *testing.T, msg string) {
	t.Helper()

	lower := strings.ToLower(msg)
	for _, placeholder := range []string{
		"postgresql://user:pass@host/db?...",
		"postgres://user:pass@host/db?...",
	} {
		lower = strings.ReplaceAll(lower, placeholder, "")
	}
	for _, marker := range []string{"postgres://", "postgresql://", "password="} {
		if strings.Contains(lower, marker) {
			t.Fatalf("error leaked sensitive marker %q: %q", marker, msg)
		}
	}
	if dsnAuthorityPattern.MatchString(lower) {
		t.Fatalf("error leaked DSN authority info: %q", msg)
	}
}

func assertSafeErrorWraps(t *testing.T, err error, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("expected errors.Is to match %v, got %v", want, err)
	}
	var se *SafeError
	if !errors.As(err, &se) {
		t.Fatalf("expected SafeError wrapper, got %T", err)
	}
}

//go:build integration

package neon

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	integrationDSNURLPattern   = regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s]+`)
	integrationPasswordPattern = regexp.MustCompile(`(?i)password=[^\s]+`)
	integrationSchemaPattern   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

func requireIntegrationEnv(t *testing.T) (pooledURL, directURL string) {
	t.Helper()

	pooledURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	directURL = strings.TrimSpace(os.Getenv("DATABASE_URL_DIRECT"))

	var missing []string
	if pooledURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if directURL == "" {
		missing = append(missing, "DATABASE_URL_DIRECT")
	}
	if len(missing) > 0 {
		t.Fatalf("integration requires environment variable(s): %s", strings.Join(missing, ", "))
	}

	return pooledURL, directURL
}

func integrationSchemaName(t *testing.T) string {
	t.Helper()

	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("failed to generate random schema suffix: %s", sanitizeErrorMessage(err))
	}
	name := fmt.Sprintf("vango_neon_it_%d_%x", time.Now().Unix(), binary.BigEndian.Uint32(b[:]))
	if !integrationSchemaPattern.MatchString(name) {
		t.Fatalf("generated invalid schema name: %q", name)
	}

	return name
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func qualifiedTable(schema, table string) string {
	return quoteIdent(schema) + "." + quoteIdent(table)
}

func sanitizeErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	msg := err.Error()
	msg = integrationDSNURLPattern.ReplaceAllString(msg, "[REDACTED_DSN]")
	msg = integrationPasswordPattern.ReplaceAllString(msg, "password=[REDACTED]")
	return msg
}

func mustNoErr(t *testing.T, err error, operation string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %s", operation, sanitizeErrorMessage(err))
	}
}

func mustIs(t *testing.T, got error, want error, operation string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got=%s want=%v", operation, sanitizeErrorMessage(got), want)
	}
}

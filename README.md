# vango-neon

`vango-neon` is the Neon database package for Vango apps.

It provides:
- a `DB` interface for application/service dependency injection
- a `Pool` implementation backed by `pgxpool`
- `Connect` defaults tuned for Neon usage
- safe-by-default connect-path errors (`SafeError`)
- direct URL resolution for migration/session-level operations (`Pool.DirectURL()`)

## Install

```bash
go get github.com/vango-go/vango-neon
```

## Quickstart

```go
package db

import (
	"context"
	"os"

	neon "github.com/vango-go/vango-neon"
)

func ConnectPool(ctx context.Context) (*neon.Pool, error) {
	return neon.Connect(ctx, neon.Config{
		ConnectionString: os.Getenv("DATABASE_URL"),
		DirectURL:        os.Getenv("DATABASE_URL_DIRECT"),
	})
}
```

Use pooled URL for app queries and direct URL for migrations/session-level features.

- `DATABASE_URL`: pooled endpoint (hostname contains `-pooler`)
- `DATABASE_URL_DIRECT`: direct endpoint (hostname without `-pooler`)

If `DirectURL` is empty and `ConnectionString` is a Neon pooled URL in URL form,
`vango-neon` derives the direct URL by removing `-pooler` from the first hostname
label.

## Security posture

- `Connect` rejects plaintext-capable settings (`sslmode=allow` / `sslmode=prefer`).
- `Connect` requires TLS (`sslmode=require` or stricter).
- public error strings are safe to log by default and avoid DSN credential leakage.
- `Pool.DirectURL()` returns credentials and must be treated as secret material.

## Tracing and advanced pgx options

Use `WithPgxConfig` for advanced configuration (tracing hooks, custom pgx setup).
The modifier runs after package defaults and can override them.

## Contract reference

See [NEON_INTEGRATION_SPEC.md](./NEON_INTEGRATION_SPEC.md) for the authoritative
specification.

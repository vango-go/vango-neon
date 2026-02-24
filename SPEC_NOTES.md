# SPEC_NOTES

These notes record compile/runtime-level clarifications while preserving the
behavioral contract from `NEON_INTEGRATION_SPEC.md`.

## URL-form derivation requirement

For pooled-to-direct derivation, `resolveDirectURL` treats a connection string as
URL-form only when all are true:

1. `url.Parse` succeeds
2. scheme is `postgres` or `postgresql`
3. `u.Hostname()` is non-empty

This avoids false positives from keyword/value DSNs, where `url.Parse` may succeed
without producing a valid URL authority/host.

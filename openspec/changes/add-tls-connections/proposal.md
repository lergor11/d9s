# Change: Add TLS support for database connections

## Why
Every adapter currently connects in plaintext — `internal/db/postgres.go:40`
explicitly sets `TLSConfig = nil`. Managed databases (RDS, Cloud SQL, Neon,
Supabase, ClickHouse Cloud, Redis Cloud) require TLS, so d9s cannot reach them
at all today. This is the single biggest gap between d9s and a usable
DataGrip replacement.

## What Changes
- New optional `tls` block per connection: `mode` (`disable` | `require` |
  `verify-ca` | `verify-full`), `ca`, `cert`, `key`, `server_name`
- Postgres, ClickHouse, and Redis adapters honour it; TLS composes with SSH
  tunneling (the tunnel carries an already-encrypted stream)
- Default stays `disable` for a connection with an `ssh` block (SSH already
  encrypts) and becomes `require` otherwise, so a plain host:port to a cloud
  provider works without extra configuration
- Client certificates may reference 1Password via `op://` like passwords

## Impact
- Affected specs: connection-config (new tls block), engine-adapters (TLS
  negotiation per engine), secret-resolution (certificate material from
  1Password)
- Affected code: `internal/config`, `internal/db/{postgres,clickhouse,redis}.go`,
  `internal/secrets`

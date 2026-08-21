# Tasks — add-tls-connections

## 1. Configuration
- [x] 1.1 `config.TLS` struct (mode, ca, cert, key, server_name), parsing and mode validation
- [x] 1.2 Default resolution: `disable` when an ssh block is present, `require` otherwise
- [x] 1.3 Unit tests for parsing, defaults, and rejected modes

## 2. Certificate material
- [x] 2.1 Resolve `ca`/`cert`/`key` from a path or an `op://` reference into memory
- [x] 2.2 Build a `*tls.Config` per mode; no `InsecureSkipVerify` outside `require`
- [x] 2.3 Unit tests with a self-signed fixture pair generated in-test

## 3. Adapters
- [x] 3.1 Postgres: set `cfg.TLSConfig` (composes with the existing DialFunc)
- [x] 3.2 ClickHouse: `Options.TLS`
- [x] 3.3 Redis: `Options.TLSConfig`

## 4. UI
- [x] 4.1 Connection list shows a TLS badge; `require` renders as unverified

## 5. Verification
- [x] 5.1 Integration tests against a TLS-enabled postgres container (server cert fixture)
- [x] 5.2 `make lint test` green

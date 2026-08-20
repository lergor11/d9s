# Tasks — add-d9s-mvp

## 1. Project Skeleton
- [ ] 1.1 `go mod init github.com/andreim/d9s`; layout `cmd/d9s`, `internal/*`
- [ ] 1.2 Makefile (build/test/lint), .gitignore, README

## 2. Config & Secrets
- [ ] 2.1 `internal/config`: YAML load/validate, defaults, `${ENV}` + `op://` detection
- [ ] 2.2 `internal/secrets`: `SecretResolver` interface, `op read` impl, env impl, in-memory cache
- [ ] 2.3 Unit tests: config parsing, plaintext-password warning, resolver selection

## 3. SSH Tunnel
- [ ] 3.1 `internal/sshtunnel`: agent socket probe (1Password → SSH_AUTH_SOCK), ssh.Client with known_hosts verification
- [ ] 3.2 `DialContext` provider shared per connection; lazy init; Close()

## 4. Engine Adapters
- [ ] 4.1 `internal/db`: `Driver` interface, registry, `Result` model (columns/rows/affected/err/duration)
- [ ] 4.2 Postgres adapter (pgx, custom DialFunc)
- [ ] 4.3 ClickHouse adapter (clickhouse-go, custom DialContext)
- [ ] 4.4 Redis adapter (go-redis, custom Dialer; command-line splitter)
- [ ] 4.5 SQL statement splitter + destructive-pattern scanner + unit tests

## 5. TUI
- [ ] 5.1 App model & routing (connections → databases → query view), header/footer, help overlay
- [ ] 5.2 Connection list with async connect + status
- [ ] 5.3 Database list per engine
- [ ] 5.4 Query view: textarea, run (Ctrl+R), results table, per-statement sections, cancel, destructive confirm dialog
- [ ] 5.5 Error surfaces (connect errors, op errors, tunnel errors)

## 6. Verification
- [ ] 6.1 `go build ./...`, `go vet ./...`, unit tests green
- [ ] 6.2 Smoke run: binary starts with sample config, `--version` works
- [ ] 6.3 (best-effort) Docker postgres/clickhouse/redis smoke: list DBs + run query

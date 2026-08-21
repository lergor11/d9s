# Tasks — add-d9s-mvp

## 1. Project Skeleton
- [x] 1.1 `go mod init github.com/andreim/d9s`; layout `cmd/d9s`, `internal/*`
- [x] 1.2 Makefile (build/test/lint), .gitignore, README

## 2. Config & Secrets
- [x] 2.1 `internal/config`: YAML load/validate, defaults, `${ENV}` + `op://` detection
- [x] 2.2 `internal/secrets`: `SecretResolver` interface, `op read` impl, env impl, in-memory cache
- [x] 2.3 Unit tests: config parsing, plaintext-password warning, resolver selection

## 3. SSH Tunnel
- [x] 3.1 `internal/sshtunnel`: agent socket probe (1Password → SSH_AUTH_SOCK), ssh.Client with known_hosts verification
- [x] 3.2 `DialContext` provider shared per connection; lazy init; Close()

## 4. Engine Adapters
- [x] 4.1 `internal/db`: `Driver` interface, registry, `Result` model (columns/rows/affected/err/duration)
- [x] 4.2 Postgres adapter (pgx, custom DialFunc)
- [x] 4.3 ClickHouse adapter (clickhouse-go, custom DialContext)
- [x] 4.4 Redis adapter (go-redis, custom Dialer; command-line splitter)
- [x] 4.5 SQL statement splitter + destructive-pattern scanner + unit tests

## 5. TUI
- [x] 5.1 App model & routing (connections → databases → query view), header/footer, help overlay
- [x] 5.2 Connection list with async connect + status
- [x] 5.3 Database list per engine
- [x] 5.4 Query view: textarea, run (Ctrl+R), results table, per-statement sections, cancel, destructive confirm dialog
- [x] 5.5 Error surfaces (connect errors, op errors, tunnel errors)

## 6. Verification
- [x] 6.1 `go build ./...`, `go vet ./...`, unit tests green (plus `golangci-lint run` clean)
- [x] 6.2 Smoke run: binary starts and renders the connection list with a sample
      config; `--version` works
- [x] 6.3 Docker postgres/clickhouse/redis smoke: list DBs + run queries
      (`internal/db/integration_test.go`, build tag `integration`) — all three
      adapters verified against live engines

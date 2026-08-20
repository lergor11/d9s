# Project Context

## Purpose
**d9s** — a k9s-style terminal UI (TUI) database client for macOS and Linux. An
open-source alternative to DataGrip for engineers who live in the terminal.

MVP goals:
- Manage a list of database connections (PostgreSQL, ClickHouse, Redis)
- Connect through SSH bastion hosts using the 1Password SSH agent
- Resolve database credentials from 1Password via `op://` secret references
- Execute queries/commands: a single statement or several statements in one run

## Tech Stack
- Go 1.23+ (single static binary, cross-compiled for darwin/linux, amd64/arm64)
- TUI: `github.com/charmbracelet/bubbletea` + `bubbles` + `lipgloss`
- PostgreSQL: `github.com/jackc/pgx/v5`
- ClickHouse: `github.com/ClickHouse/clickhouse-go/v2`
- Redis: `github.com/redis/go-redis/v9`
- SSH: `golang.org/x/crypto/ssh` (+ `ssh/agent` for the 1Password agent socket)
- Config: YAML via `gopkg.in/yaml.v3`
- Secrets: 1Password CLI (`op read`) invoked as a subprocess; 1Password SSH agent
  socket at `~/.1password/agent.sock` (Linux: `~/.1password/agent.sock` or
  `$SSH_AUTH_SOCK`)

## Project Conventions

### Code Style
- Standard Go style: `gofmt`, `go vet`; idiomatic error wrapping with `fmt.Errorf("...: %w", err)`
- Package layout: `cmd/d9s/` (main), `internal/` for everything else
  (`internal/config`, `internal/secrets`, `internal/sshtunnel`, `internal/db`,
  `internal/ui`)
- No global state; dependencies passed explicitly

### Architecture Patterns
- Engine adapters implement a common `internal/db.Driver` interface
  (Connect, ListDatabases, Execute, Close); the UI is engine-agnostic
- All I/O (connect, query, `op read`) runs off the UI goroutine; results are
  delivered to Bubble Tea as messages (`tea.Cmd`)
- SSH tunnel is a local dialer injected into DB drivers (no local listening
  ports; use `ssh.Client.Dial` as a custom DialContext)

### Testing Strategy
- Unit tests for config parsing, statement splitting, destructive-query detection
- Integration tests behind a build tag (`//go:build integration`) using
  Docker containers (postgres, clickhouse, redis)
- No tests that require a real 1Password account; `op` calls mocked via an
  interface

### Git Workflow
- `main` is releasable; feature branches per OpenSpec change
- Conventional commits (`feat:`, `fix:`, `docs:`, `refactor:`)

## Domain Context
- k9s-style UX: single-key navigation, `:`-command mode, contextual key hints
  in the footer, everything doable without a mouse
- "Connection" = a configured endpoint (engine + host + credentials + optional
  SSH bastion). "Database" = a database within a connection (Postgres/ClickHouse
  databases, Redis logical DB indexes 0–15)

## Important Constraints
- Secrets MUST never be written to disk in plaintext (no passwords in config,
  history, or logs); config stores only `op://` references
- SSH private keys MUST never be read into the application; authentication goes
  through the 1Password SSH agent socket
- Destructive statements (DROP/TRUNCATE/DELETE without WHERE, FLUSHALL, etc.)
  require interactive confirmation before execution
- Must work on macOS and Linux terminals (no OS-specific UI dependencies)

## External Dependencies
- 1Password desktop app + 1Password CLI (`op`) v2 — required for secret
  resolution and the SSH agent; d9s degrades gracefully (plain env-var/prompt
  auth) when absent
- Docker — only for integration tests

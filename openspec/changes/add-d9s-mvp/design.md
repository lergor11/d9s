# Design — d9s MVP

## Context
Greenfield Go TUI. Key risks: SSH-agent auth without touching key files,
keeping secrets off disk, engine differences behind one interface, and keeping
the Bubble Tea event loop non-blocking.

## Goals / Non-Goals
- Goals: connection list, database list, single/multi-statement execution,
  bastion via 1Password SSH agent, `op://` secret resolution, destructive-op
  confirmation.
- Non-Goals (MVP): query history, schema browser, export, multi-connection
  fan-out, autocomplete, TLS client certs, Windows.

## Decisions
- **Bubble Tea over tview**: composable Elm-style architecture, active
  ecosystem (bubbles textarea/table/spinner), easier async via `tea.Cmd`.
- **No local listening ports for tunnels**: inject `ssh.Client.Dial` as the
  DialContext of each DB driver (pgx `DialFunc`, clickhouse-go `DialContext`,
  go-redis `Dialer`). Avoids port collisions and exposure on localhost.
- **`op` CLI subprocess over 1Password SDK**: the CLI delegates auth/biometrics
  to the desktop app; no tokens to manage. Wrapped in a `SecretResolver`
  interface so tests can mock it.
- **Statement splitting in-app** (SQL lexer aware of strings/comments/dollar
  quoting) rather than relying on server multi-statement support — needed for
  per-statement results and destructive-op scanning across all engines.
- **Driver registry**: `map[EngineType]func(cfg) Driver`, UI iterates registry.

## Risks / Trade-offs
- 1Password agent socket path differs per platform → probe list + honor
  `IdentityAgent`-style override in config (`ssh.agent_socket`).
- ClickHouse native protocol through tunnel: supported via custom DialContext.
- Redis command parsing (quotes) — implement minimal shell-like splitter.
- Bastion host key verification: MVP uses `known_hosts` when readable, else
  prompts trust-on-first-use; never silently `InsecureIgnoreHostKey`.

## Migration Plan
Greenfield; no migration. Rollback = delete repo.

## Open Questions
- None blocking; backlog items tracked as future OpenSpec changes.

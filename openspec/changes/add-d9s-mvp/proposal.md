# Change: Add d9s MVP — TUI database client with 1Password + SSH bastion support

## Why
Engineers need an open-source, terminal-first alternative to DataGrip that works
over SSH bastions and never stores plaintext secrets. Nothing in this repo exists
yet; this change bootstraps the whole MVP.

## What Changes
- New Go project `d9s` (Bubble Tea TUI, single binary)
- YAML connection config at `~/.config/d9s/config.yaml` with `op://` secret references
- Secret resolution through 1Password CLI (`op read`), never persisted
- SSH bastion tunneling authenticated via the 1Password SSH agent
- Engine adapters for PostgreSQL, ClickHouse, Redis behind one `Driver` interface
- Query execution: single statement or a multi-statement script in one run,
  with per-statement results and interactive confirmation for destructive statements
- k9s-style TUI: connection list → database list → query view with results table

## Impact
- Affected specs: connection-config, secret-resolution, ssh-tunnel,
  engine-adapters, query-execution, tui-shell (all new)
- Affected code: entire repo (new): `cmd/d9s/`, `internal/{config,secrets,sshtunnel,db,ui}`

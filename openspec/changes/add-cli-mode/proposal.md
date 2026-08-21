# Change: Non-interactive CLI mode

## Why
d9s is only a full-screen interface today, so it cannot be used from a script,
a pipeline, or an agent's shell. The connection machinery — 1Password secrets,
SSH bastions, TLS — is exactly what those callers want, and re-implementing it
with `psql`/`clickhouse-client`/`redis-cli` means re-solving the credentials
problem each time.

## What Changes
- Subcommands alongside the existing interface: `d9s connections`,
  `d9s databases <connection>`, `d9s tables <connection> [database]`,
  `d9s describe <connection> <table>`, and `d9s query <connection> [sql]`
- `d9s query` reads SQL from an argument, a `-f file`, or stdin, so it composes
  in pipelines
- `-o` selects the output format: `table` (default when attached to a
  terminal), `csv`, `json`, or `jsonl` (default when piped)
- Read-only by default: destructive statements are refused unless `--write` is
  passed, because a non-interactive caller cannot answer a confirmation prompt
- Exit codes distinguish success, a failed query, and a connection failure, so
  scripts can branch
- Running `d9s` with no subcommand keeps opening the interface, unchanged

## Impact
- Affected specs: cli-mode (new)
- Affected code: `cmd/d9s/` (subcommand dispatch), new `internal/cli`, reusing
  `internal/db`, `internal/config`, `internal/secrets`, `internal/sshtunnel`,
  and `internal/export` for formatting

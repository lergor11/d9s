# Tasks — add-cli-mode

## 1. Dispatch
- [x] 1.1 Subcommand routing in `cmd/d9s` that keeps the bare invocation interactive
- [x] 1.2 Shared flag set: `-config`, `-o`, `-f`, `--write`, `--timeout`
      — plus `-database` for `describe` and `query`, since a connection need
        not configure one; each command registers only the flags it accepts
- [x] 1.3 Exit-code constants with a documented meaning each
      — `internal/cli`: 0 success, 1 unexpected, 2 usage, 3 connect, 4
        statement, 5 refused

## 2. Commands
- [x] 2.1 `connections`, `databases`, `tables`, `describe`
- [x] 2.2 `query` with argument/file/stdin input and multi-statement handling
- [x] 2.3 Destructive-statement refusal reusing `db.Destructive`
      — checked before connecting, so a refused run touches nothing

## 3. Output
- [x] 3.1 `table` renderer (aligned, terminal-width aware)
- [x] 3.2 `csv`/`json`/`jsonl` via `internal/export`; stdout carries data only, diagnostics go to stderr
      — `JSONL` added next to `CSV`/`JSON`, sharing the NULL→null mapping
- [x] 3.3 Format defaulting from `term.IsTerminal`

## 4. Verification
- [x] 4.1 Unit tests for dispatch, format defaulting, and refusal logic
- [x] 4.2 Integration tests running real queries against containers and asserting exit codes
      — `internal/cli/integration_test.go`, postgres + clickhouse + redis
- [x] 4.3 README section and `--help` text for the subcommands
- [x] 4.4 `make lint test` green

## 5. Follow-up
- [x] 5.1 Move `internal/ui` onto `internal/session` so the interface and the
      command line cannot drift apart in how they connect
      — `session.OpenTunnel` borrows the connection-level tunnel; `Close`
        releases only a tunnel the session raised itself

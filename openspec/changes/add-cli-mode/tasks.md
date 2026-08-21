# Tasks — add-cli-mode

## 1. Dispatch
- [ ] 1.1 Subcommand routing in `cmd/d9s` that keeps the bare invocation interactive
- [ ] 1.2 Shared flag set: `-config`, `-o`, `-f`, `--write`, `--timeout`
- [ ] 1.3 Exit-code constants with a documented meaning each

## 2. Commands
- [ ] 2.1 `connections`, `databases`, `tables`, `describe`
- [ ] 2.2 `query` with argument/file/stdin input and multi-statement handling
- [ ] 2.3 Destructive-statement refusal reusing `db.Destructive`

## 3. Output
- [ ] 3.1 `table` renderer (aligned, terminal-width aware)
- [ ] 3.2 `csv`/`json`/`jsonl` via `internal/export`; stdout carries data only, diagnostics go to stderr
- [ ] 3.3 Format defaulting from `term.IsTerminal`

## 4. Verification
- [ ] 4.1 Unit tests for dispatch, format defaulting, and refusal logic
- [ ] 4.2 Integration tests running real queries against containers and asserting exit codes
- [ ] 4.3 README section and `--help` text for the subcommands
- [ ] 4.4 `make lint test` green

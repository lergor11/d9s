# Tasks — add-editor-autocomplete

## 1. Schema cache
- [x] 1.1 `internal/schema`: per-session cache of tables and columns loaded via `db.Driver`, async fill, refresh, and a loading state
- [x] 1.2 Unit tests with a fake driver: cold read, concurrent readers, refresh

## 2. Completion engine
- [x] 2.1 `internal/ui/complete.go`: cursor-context detection (after FROM/JOIN/SELECT/WHERE/…, dot-qualified, statement start) reusing the SQL lexer in `internal/db`
      — `db.Tokenize`/`db.Token` export that lexer; `Split` and `Destructive` run on the same tokenizer
- [x] 2.2 Alias resolution from the current statement
- [x] 2.3 Candidate ranking: prefix matches first, then case-insensitive substring; common-prefix insertion
- [x] 2.4 Redis: command list plus bounded SCAN key lookup (through `ListColumns`, which scans a prefix with a cursor)
- [x] 2.5 Table-driven tests for every context in the spec

## 3. UI
- [x] 3.1 Popup widget: list, selection, Tab/arrows/Enter/Esc, narrowing as the user types
- [x] 3.2 Rebind focus toggle to `Ctrl+J`; Tab completes in the editor
- [x] 3.3 Update footer hints and the `?` overlay for the new bindings; `ctrl+g` reloads the cached names
      — README and CLI help text are owned by another change in flight

## 4. Verification
- [x] 4.1 Unit tests for popup state transitions
- [x] 4.2 Integration test: completion candidates against a live postgres schema
      (`internal/ui/complete_integration_test.go`, `-tags integration`, run against postgres:16-alpine)
- [x] 4.3 `make lint test` green

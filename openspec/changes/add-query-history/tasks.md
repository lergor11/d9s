# Tasks — add-query-history

## 1. Implementation
- [x] 1.1 internal/history: append-only JSONL store at ~/.local/share/d9s/history.jsonl (entry: ts, connection, database, statement, ok, duration); load with de-dup (latest wins)
- [x] 1.2 Record every executed statement (including failed; not cancelled-before-start)
- [x] 1.3 History overlay in query view: Ctrl+H opens, type-to-filter (substring, case-insensitive), j/k select, Enter inserts into editor, Esc closes
- [x] 1.4 Unit tests: store round-trip, filter, de-dup
- [x] 1.5 go build/vet/test green

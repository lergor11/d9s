# Tasks — add-meta-commands

## 1. Parsing
- [x] 1.1 `internal/meta`: parse a backslash command into verb, `+` flag, and argument, respecting quoting
      — double-quoted arguments with `""` escaping; a second argument is
        refused rather than silently dropped
- [x] 1.2 Recognise a meta-command in `db.Split` so it survives statement splitting alongside SQL
      — a statement whose first code token is a backslash runs to the end of
        its line, with `;` also accepted; a quoted argument may hold either
- [x] 1.3 Nearest-match suggestion for an unknown verb
      — Levenshtein over the supported verbs, suggestions capped at 2 edits
- [x] 1.4 Table-driven tests, including `\d+ "My Table"` and a command mixed into a SQL script
      — `internal/meta/meta_test.go`, split cases in `internal/db/split_test.go`

## 2. Answering
- [x] 2.1 Map verbs onto the existing `ListDatabases`/`ListTables`/`ListColumns`
      — `\l`, `\dt`, `\d`, `\d <table>`; `\dn`/`\du`/`\df` go to per-engine
        catalog queries through `Execute`
- [x] 2.2 Extend the driver with the detail `\d+` needs (size, indexes, comments) per engine
      — `db.Describer`, kept off `Driver` the way `Streamer` is; postgres
        answers from pg_class/pg_index, clickhouse from system.tables and
        system.data_skipping_indices; an engine without one gets the plain
        description
- [x] 2.3 Per-engine "not supported here" errors naming the engine
      — redis names itself for `\dn`/`\du`/`\df`; clickhouse's `\dn` points
        at `\l`
- [x] 2.4 `\?` help text and `\q`
      — `\?` renders as a two-column result table; `\q` quits the interface
        and ends a CLI script successfully, running nothing more

## 3. Surfaces
- [x] 3.1 Query view: results render as a normal result section
      — export, copy, sorting and filtering apply unchanged
- [x] 3.2 `d9s query` accepts meta-commands too, so scripts can use them
- [x] 3.3 README and `--help`

## 4. Verification
- [x] 4.1 Integration tests per engine against live containers
      — `internal/meta/integration_test.go`: postgres (`\dt`, `\d+` with
        index/size/comment, `\dn`, `\du`), clickhouse (`\d`, `\d+` with
        primary key and comment, `\dn` error), redis (`\l`, `\du` error)
- [x] 4.2 `make lint test` green

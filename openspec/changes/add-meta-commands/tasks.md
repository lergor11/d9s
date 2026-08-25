# Tasks — add-meta-commands

## 1. Parsing
- [ ] 1.1 `internal/meta`: parse a backslash command into verb, `+` flag, and argument, respecting quoting
- [ ] 1.2 Recognise a meta-command in `db.Split` so it survives statement splitting alongside SQL
- [ ] 1.3 Nearest-match suggestion for an unknown verb
- [ ] 1.4 Table-driven tests, including `\d+ "My Table"` and a command mixed into a SQL script

## 2. Answering
- [ ] 2.1 Map verbs onto the existing `ListDatabases`/`ListTables`/`ListColumns`
- [ ] 2.2 Extend the driver with the detail `\d+` needs (size, indexes, comments) per engine
- [ ] 2.3 Per-engine "not supported here" errors naming the engine
- [ ] 2.4 `\?` help text and `\q`

## 3. Surfaces
- [ ] 3.1 Query view: results render as a normal result section
- [ ] 3.2 `d9s query` accepts meta-commands too, so scripts can use them
- [ ] 3.3 README and `--help`

## 4. Verification
- [ ] 4.1 Integration tests per engine against live containers
- [ ] 4.2 `make lint test` green

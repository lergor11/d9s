# Change: psql-style meta-commands in the editor

## Why
Anyone coming from `psql` reaches for `\d+ users` before they reach for a
catalog query, and today that is a syntax error. The schema panel already
knows how to answer those questions, but it costs a detour out of the editor
and cannot answer the ones that take an argument.

## What Changes
- The editor recognises backslash commands and answers them from the driver
  instead of sending them to the engine: `\l` databases, `\dt` tables, `\d`
  tables in short form, `\d <table>` its columns, `\d+ <table>` the same with
  sizes, comments, and indexes, `\dn` schemas, `\du` roles, `\df` functions
- `\?` lists the supported commands, `\q` quits, and an unknown command names
  the closest match rather than failing at the engine
- ClickHouse maps the same verbs onto its own catalog (`system.tables`,
  `system.columns`), and Redis answers `\l` with its logical databases and
  `\dt` with key prefixes; a verb an engine cannot answer says so plainly
- Meta-commands mix with SQL in one buffer: a script may open with `\dt` and
  continue with a SELECT, each producing its own result section
- Output is a normal result table, so export, copy, and the grid's sorting and
  filtering all work on it

## Impact
- Affected specs: meta-commands (new), query-execution (a statement may be a
  meta-command rather than SQL)
- Affected code: new `internal/meta`, `internal/db` (a richer table
  description for `\d+`), `internal/ui/query.go`, `internal/cli`

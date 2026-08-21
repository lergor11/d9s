# Change: Add Tab autocomplete in the query editor

## Why
Typing `SELECT * FROM ` and pressing Tab should offer the tables you can
select from, the way a real SQL client does. Today the editor is a plain
textarea: every table and column name has to be typed from memory or looked up
in the schema panel first. This is the most-missed feature after connecting at
all.

## What Changes
- Tab in the editor completes the word at the cursor, choosing candidates from
  what is grammatically expected at that point: databases and tables after
  `FROM`/`JOIN`/`INTO`/`UPDATE`, columns after `SELECT`/`WHERE`/`ON`/
  `GROUP BY`/`ORDER BY`, and SQL keywords otherwise
- Columns are offered for the tables named in the statement, including through
  aliases (`FROM users u` → `u.<Tab>` lists the columns of `users`)
- Redis completes command names and, after a command that takes a key, keys
  discovered by a bounded `SCAN`
- A completion popup lists candidates when more than one matches; a unique
  match is inserted directly, and a common prefix is inserted shell-style
- Schema names are cached per session on first use and refreshable, so
  completion never blocks the event loop on a catalog query
- **BREAKING** Tab no longer toggles editor/results focus; that moves to
  `ctrl+j`, and Tab keeps its focus-toggle meaning everywhere except the editor

## Impact
- Affected specs: editor-autocomplete (new), tui-shell (Tab rebinding)
- Affected code: `internal/ui/query.go`, new `internal/ui/complete.go`, new
  `internal/schema` cache over `db.Driver.ListTables`/`ListColumns`

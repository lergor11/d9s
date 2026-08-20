# Change: Add query history

## Why
Re-typing queries is the top friction in terminal DB clients; users expect
shell-like history with search.

## What Changes
- Persist executed statements to a local history file per connection
- History view with fuzzy search (Ctrl+H from the query view), Enter inserts
  into the editor
- Secrets never enter history (statements are stored as typed; no passwords
  are part of statements by design)

## Impact
- Affected specs: query-history (new), tui-shell (new binding)
- Affected code: internal/history (new), internal/ui

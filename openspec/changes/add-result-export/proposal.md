# Change: Add result export (CSV/JSON, clipboard)

## Why
Query results usually need to leave the terminal — into a file for sharing or
the clipboard for a ticket/PR.

## What Changes
- From the results area: `e` exports the focused statement's result to a file
  (CSV or JSON, prompted path with sensible default), `y` copies it as
  aligned text/CSV to the system clipboard
- Works on macOS (pbcopy) and Linux (xclip/wl-copy fallbacks)

## Impact
- Affected specs: result-export (new), tui-shell (bindings)
- Affected code: internal/export (new), internal/ui

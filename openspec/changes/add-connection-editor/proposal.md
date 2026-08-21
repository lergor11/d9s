# Change: Manage connections from the interface

## Why
Adding or fixing a connection means quitting d9s and hand-editing YAML. A
first-run user with no config has nowhere to start except the sample text.

## What Changes
- `a` adds a connection, `e` edits the highlighted one, `d` deletes it after
  confirmation, all through a form (name, engine, host, port, user, password
  reference, database, ssh block)
- Edits are written back to the config file preserving comments and key order
- A connection can be tested from the form before saving
- Secrets stay references: the password field accepts `op://` or `${ENV}` and
  refuses to store a literal without an explicit confirmation

## Impact
- Affected specs: connection-config (writing, not just reading), tui-shell
  (form view and bindings)
- Affected code: `internal/config` (round-trip writer), `internal/ui`

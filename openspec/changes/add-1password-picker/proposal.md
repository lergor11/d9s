# Change: Pick 1Password items instead of typing op:// paths

## Why
Using 1Password today means knowing the exact `op://vault/item/field` path and
typing it into YAML by hand. Getting it wrong surfaces only as a failed
connection.

## What Changes
- A picker lists vaults, then items, then fields via the 1Password CLI, and
  writes the chosen `op://` reference into the field being edited
- Reference validation: an `op://` path can be checked without revealing the
  secret, reporting "resolves" or the CLI's error
- Clear guidance when 1Password is locked or the CLI integration is off,
  instead of a raw exit status

## Impact
- Affected specs: secret-resolution (item discovery and validation)
- Affected code: `internal/secrets` (list vaults/items/fields), `internal/ui`
  (picker used by the connection form)

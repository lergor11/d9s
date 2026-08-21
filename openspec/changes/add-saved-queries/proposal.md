# Change: Saved queries

## Why
History records what you ran, but there is nowhere to keep the handful of
queries you rerun every week under a name you recognise.

## What Changes
- Save the editor buffer under a name, scoped to a connection or global
- Browse, filter, and load saved queries; loading fills the editor without
  running
- Saved queries live in a plain YAML file the user can edit or check into a
  dotfiles repo

## Impact
- Affected specs: saved-queries (new)
- Affected code: new `internal/snippets`, `internal/ui/query.go`

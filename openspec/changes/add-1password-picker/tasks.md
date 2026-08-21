# Tasks — add-1password-picker

## 1. CLI wrapper
- [x] 1.1 `op vault list`, `op item list --vault`, `op item get --format json` parsed into vault/item/field models
- [x] 1.2 Distinguish locked, not-installed, and not-signed-in states with actionable messages
- [x] 1.3 Validation call that resolves a reference and discards the value
- [x] 1.4 Unit tests against a stubbed CLI runner

## 2. UI
- [x] 2.1 Three-step picker overlay reachable from the connection form's password field
      — `ctrl+p` on that field; type to filter, `↑/↓` select, `enter` descend,
        `esc` back a step; concealed fields are listed first
- [x] 2.2 Inline validation feedback for a typed reference
      — `ctrl+k` checks it through `secrets.Validate`, which never returns the
        value; the form reports that it resolves, or the CLI's reason

## 3. Verification
- [x] 3.1 Unit tests for picker state and reference construction
      — reference construction is covered in `internal/secrets`; picker state,
        filtering, step navigation, and every error kind in
        `internal/ui/picker_test.go`
- [ ] 3.2 `make lint test` green

## Notes
- The picker shows names only. No field value is ever fetched, so there is
  nothing secret on screen and nothing to leak into an error.
- Failures render as the advice `OpError` already carries — "unlock the desktop
  app", "run op signin" — never as a CLI exit status. A failure part-way down
  steps back to the previous level instead of dropping out of the picker.

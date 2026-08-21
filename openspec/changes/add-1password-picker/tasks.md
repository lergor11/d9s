# Tasks — add-1password-picker

## 1. CLI wrapper
- [x] 1.1 `op vault list`, `op item list --vault`, `op item get --format json` parsed into vault/item/field models
- [x] 1.2 Distinguish locked, not-installed, and not-signed-in states with actionable messages
- [x] 1.3 Validation call that resolves a reference and discards the value
- [x] 1.4 Unit tests against a stubbed CLI runner

## 2. UI
- [ ] 2.1 Three-step picker overlay reachable from the connection form's password field
- [ ] 2.2 Inline validation feedback for a typed reference

## 3. Verification
- [ ] 3.1 Unit tests for picker state and reference construction
      — reference construction is covered in `internal/secrets`; picker state
        waits on section 2
- [ ] 3.2 `make lint test` green

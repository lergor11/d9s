# Tasks — add-saved-queries

## 1. Storage
- [ ] 1.1 `internal/snippets`: YAML load/save, scopes, overwrite detection, atomic write with 0600 permissions
- [ ] 1.2 Unit tests: round trip, scope filtering, hand-edited file reload

## 2. UI
- [ ] 2.1 Save action prompting for a name and scope, with overwrite confirmation
- [ ] 2.2 Browser overlay: list, filter, load into editor, delete with confirmation
- [ ] 2.3 Footer hints, `?` overlay, README, and CLI help updated

## 3. Verification
- [ ] 3.1 Unit tests for browser state
- [ ] 3.2 `make lint test` green

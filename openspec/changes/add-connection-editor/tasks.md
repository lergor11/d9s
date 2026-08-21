# Tasks — add-connection-editor

## 1. Config writer
- [ ] 1.1 Comment-preserving round trip using yaml.Node
- [ ] 1.2 Atomic write (temp file + rename), 0600 permissions
- [ ] 1.3 Unit tests: comments preserved, ordering stable, atomicity on failure

## 2. UI
- [ ] 2.1 Form view with field validation (required fields, port range, reference format)
- [ ] 2.2 Bindings: `a` add, `e` edit, `d` delete with confirmation
- [ ] 2.3 "Test connection" action reusing the normal connect path
- [ ] 2.4 First-run prompt offering to create the file when none exists

## 3. Verification
- [ ] 3.1 Unit tests for form state and validation
- [ ] 3.2 `make lint test` green

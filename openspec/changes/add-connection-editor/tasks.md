# Tasks — add-connection-editor

## 1. Config writer
- [x] 1.1 Comment-preserving round trip using yaml.Node
      — the edited entry's lines are re-encoded from its node and spliced back
        into the original text, so everything outside them stays byte-identical
- [x] 1.2 Atomic write (temp file + rename), 0600 permissions
- [x] 1.3 Unit tests: comments preserved, ordering stable, atomicity on failure

## 2. UI
- [x] 2.1 Form view with field validation (required fields, port range, reference format)
      — the form's checks are a fast subset shown while typing; `config`
        validates again on save and stays the authority, so the two cannot
        drift apart on what is legal
- [x] 2.2 Bindings: `a` add, `e` edit, `d` delete with confirmation
- [x] 2.3 "Test connection" action reusing the normal connect path
      — `session.Open` with its own throwaway tunnel; it never borrows the
        connection list's, which `sshtunnel.Close` would poison
- [x] 2.4 First-run prompt offering to create the file when none exists

## 3. Verification
- [x] 3.1 Unit tests for form state and validation
- [ ] 3.2 `make lint test` green

## Notes
- The form is an overlay inside the connections view rather than a view of its
  own, so `connections.go` owns its keys, its rendering, and its state. The
  root model needed one field (`editor`), one `Update` case for the
  `connFormMsg` interface, and hooks for the spinner, the `?` guard, the
  footer, and the help overlay.
- Editing starts from the connection as loaded and overwrites only the fields
  the form shows, so a `tls` block, a redis `mode`, `addresses`, `protocol`, or
  `allow_write` survives an edit made through it.

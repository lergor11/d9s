<!-- OPENSPEC:START -->
# OpenSpec Instructions

These instructions are for AI assistants working in this project.

Always open `@/openspec/AGENTS.md` when the request:
- Mentions planning or proposals (words like proposal, spec, change, plan)
- Introduces new capabilities, breaking changes, architecture shifts, or big performance/security work
- Sounds ambiguous and you need the authoritative spec before coding

Use `@/openspec/AGENTS.md` to learn:
- How to create and apply change proposals
- Spec format and conventions
- Project structure and guidelines

Keep this managed block so 'openspec update' can refresh the instructions.

<!-- OPENSPEC:END -->

# d9s Conventions

## Before every commit

```sh
make lint test      # gofmt + go vet + golangci-lint + go test ./...
```

All four must be clean — reviews here reject style nits, so treat a linter
finding as a build failure. `golangci-lint` runs the standard set plus
misspell, unconvert, unparam, gocritic, and revive's `exported` rule: every
exported identifier needs a doc comment starting with its name.

## Code

- Errors: check every returned error; write `_ =` when ignoring is deliberate;
  wrap with `fmt.Errorf("...: %w", err)`.
- Secrets: never write a resolved password or SSH key material to disk, a log,
  or the history file. Config holds `op://` or `${ENV}` references only.
- Engine work belongs behind `db.Driver`; the UI must stay engine-agnostic
  apart from display labels.
- Bubble Tea: all I/O happens in a `tea.Cmd` off the event loop; the UI never
  blocks on a network call.
- Tests are table-driven and hermetic (`t.TempDir()`, `t.Setenv`). Tests that
  need live engines go behind the `integration` build tag — see the README for
  the Docker one-liners.

## Verifying the TUI

The terminal UI cannot be driven by injected keystrokes in agent sandboxes
(Bubble Tea ignores them under a pseudo-terminal there). Render checks work;
for behavior, unit-test the model rather than scripting the interface.

# Change: Syntax highlighting in the query editor

## Why
The editor renders one flat colour, so keywords, strings, and comments look
alike and typos in long statements are hard to spot.

## What Changes
- SQL keywords, strings, numbers, comments, and identifiers are coloured in
  the editor, reusing the lexer that already backs statement splitting
- Redis command names are highlighted distinctly from their arguments
- The statement under the cursor is marked in the gutter, so a multi-statement
  script shows what `Ctrl+R` will run first
- Colours come from the existing style set and stay readable on light and dark
  terminals

## Impact
- Affected specs: tui-shell (editor rendering)
- Affected code: `internal/db` (expose lexer tokens), `internal/ui/query.go`,
  `internal/ui/styles.go`

# tui-shell — Delta

## ADDED Requirements

### Requirement: Editor Syntax Highlighting
The editor SHALL colour SQL keywords, string literals, numbers, and comments
distinctly from plain identifiers, and SHALL highlight Redis command names
distinctly from their arguments. Highlighting SHALL not alter the buffer text
or cursor behaviour.

#### Scenario: Keywords and strings are distinguishable
- **WHEN** the buffer contains `SELECT 'a;b' FROM t -- note`
- **THEN** the keyword, the string (semicolon included), and the comment each
  render in their own colour, and the text is unchanged

#### Scenario: Highlighting survives editing
- **WHEN** the user types in the middle of a highlighted statement
- **THEN** colours update and the cursor stays where the user put it

### Requirement: Current Statement Marker
The gutter SHALL mark the statement containing the cursor, so the user can see
which statement a run starts from in a multi-statement buffer.

#### Scenario: Marker follows the cursor
- **WHEN** the buffer holds three statements and the cursor sits in the second
- **THEN** the gutter marks the second statement's lines

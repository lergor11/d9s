# tui-shell — Delta

## MODIFIED Requirements

### Requirement: Query View With Results Table
The query view SHALL contain a multi-line editor and a results area. `Ctrl+R`
(or `F5`) runs the buffer. Results render as a scrollable table (rows/columns
for SQL, formatted replies for Redis) with row count and timing; `Ctrl+J`
toggles focus between editor and results; long cells are truncated with the
full value visible on selection. `Tab` is reserved for completion while the
editor is focused, and toggles focus everywhere else.

#### Scenario: Run and browse results
- **WHEN** the user runs a SELECT returning 500 rows
- **THEN** a scrollable table renders; the status line shows "500 rows" and
  elapsed time

#### Scenario: Error display
- **WHEN** a statement fails
- **THEN** the engine's error text is shown in the results area without
  clearing the editor buffer

#### Scenario: Focus toggle with the editor focused
- **WHEN** the editor is focused and the user presses `Ctrl+J`
- **THEN** focus moves to the results area, and pressing `Tab` there moves it
  back

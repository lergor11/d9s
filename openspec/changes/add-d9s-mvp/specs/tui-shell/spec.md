# tui-shell — Delta

## ADDED Requirements

### Requirement: k9s-Style Navigation
The TUI SHALL present three levels: connection list → database list → query
view. Navigation: `j/k`/arrows to move, `Enter` to descend, `Esc` to go back,
`q`/`Ctrl+C` to quit (with confirmation if a query is running), `?` for a help
overlay listing all key bindings. A footer SHALL always show contextual key
hints; a header SHALL show the current connection/database and engine type.

#### Scenario: Drill down to query view
- **WHEN** the user selects a connection, then a database
- **THEN** the query view opens for that database with the editor focused

#### Scenario: Help overlay
- **WHEN** the user presses `?`
- **THEN** a dismissible overlay lists all bindings for the current view

### Requirement: Connection List View
The connection list SHALL show name, engine type, host, SSH badge (when a
bastion is configured), and live status (disconnected / connecting /
connected / error). Connecting runs asynchronously and never freezes the UI.

#### Scenario: Async connect
- **WHEN** the user presses Enter on a disconnected connection
- **THEN** status shows a spinner and the UI stays responsive; on success the
  database list opens, on failure the error is shown inline

### Requirement: Query View With Results Table
The query view SHALL contain a multi-line editor and a results area. `Ctrl+R`
(or `F5`) runs the buffer. Results render as a scrollable table (rows/columns
for SQL, formatted replies for Redis) with row count and timing; `Tab` toggles
focus between editor and results; long cells are truncated with full value
visible on selection.

#### Scenario: Run and browse results
- **WHEN** the user runs a SELECT returning 500 rows
- **THEN** a scrollable table renders; the status line shows "500 rows" and
  elapsed time

#### Scenario: Error display
- **WHEN** a statement fails
- **THEN** the engine's error text is shown in the results area without
  clearing the editor buffer

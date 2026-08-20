# query-execution — Delta

## ADDED Requirements

### Requirement: Single And Multi-Statement Execution
The system SHALL execute the editor buffer as one or more statements. For SQL
engines, statements are split on `;` outside string literals, quoted
identifiers, and comments; for Redis, one command per line. Statements run
sequentially; each statement's result (rows, affected count, or error) SHALL be
shown separately with its execution time.

#### Scenario: Single statement
- **WHEN** the buffer contains `SELECT 1;`
- **THEN** one result set is displayed with timing

#### Scenario: Multi-statement script
- **WHEN** the buffer contains three `;`-separated statements
- **THEN** all three run in order and three labeled results are shown

#### Scenario: Mid-script failure
- **WHEN** statement 2 of 3 fails
- **THEN** execution stops at 2, its error is shown, statement 1's result is
  preserved, and statement 3 is marked skipped

### Requirement: Destructive Statement Confirmation
Before executing, the system SHALL scan statements for destructive patterns —
`DROP`, `TRUNCATE`, `ALTER`, `DELETE`/`UPDATE` without `WHERE`, Redis
`FLUSHALL`/`FLUSHDB`/`DEL` — and require an explicit interactive confirmation
listing the flagged statements.

#### Scenario: Confirm destructive
- **WHEN** the buffer contains `DELETE FROM users;` (no WHERE)
- **THEN** a confirmation dialog names the statement, and it only runs after
  the user confirms

#### Scenario: Cancel destructive
- **WHEN** the user declines the confirmation
- **THEN** nothing from that run is executed

### Requirement: Cancellable Execution
A running script SHALL be cancellable from the UI; cancellation stops before
the next statement and aborts the in-flight statement via context cancellation.

#### Scenario: Cancel long query
- **WHEN** the user presses the cancel key during a long-running statement
- **THEN** the statement is aborted and the UI returns to an idle state

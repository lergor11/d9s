# live-query-progress — Delta

## ADDED Requirements

### Requirement: Progress While A Query Runs
For engines that report it, the system SHALL show rows read, bytes read,
elapsed time and memory usage while a query is still running, updating as the
engine reports, and SHALL show a percentage when the engine reports a total to
read. Updates SHALL arrive on the UI goroutine as messages and never block it.

#### Scenario: A long scan reports progress
- **WHEN** a ClickHouse query scanning a large table runs
- **THEN** rows and bytes read climb on screen while it runs, with elapsed
  time, rather than only a spinner

#### Scenario: Percentage when a total is known
- **WHEN** the engine reports total rows to read
- **THEN** the display includes the percentage complete

#### Scenario: Cancelling keeps the counters
- **WHEN** the user cancels a running query
- **THEN** the display states how much had been read at the moment of the
  cancel

### Requirement: Profile Events After A Query
The system SHALL collect the profile events an engine emits and make them
available for the finished statement, presented on demand so they do not
crowd out the result.

#### Scenario: Events available for the last statement
- **WHEN** a ClickHouse query finishes and the user asks for its profile
- **THEN** the collected events are listed with their values, one row each

#### Scenario: No events, no empty panel
- **WHEN** the engine reported no events
- **THEN** the panel says so instead of showing an empty table

### Requirement: Server Log Lines
The system SHALL surface the log lines an engine sends with a query, tagged
with their level, alongside that statement's result.

#### Scenario: Logs shown with the statement
- **WHEN** ClickHouse sends log lines for a query
- **THEN** they are readable with that statement's result, with their levels

### Requirement: Honest Degradation
An engine that reports no progress SHALL show the elapsed-time counter alone,
and the interface SHALL NOT present invented or estimated figures.

#### Scenario: PostgreSQL shows what it has
- **WHEN** a long PostgreSQL query runs
- **THEN** elapsed time is shown, with no row or byte counters

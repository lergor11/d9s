# query-execution — Delta

## ADDED Requirements

### Requirement: Paged Result Fetching
The system SHALL fetch rows in pages rather than buffering an entire result
set before display. The first page SHALL render as soon as it arrives, and
further pages SHALL load as the user scrolls toward the end of the loaded
rows.

#### Scenario: First page renders immediately
- **WHEN** a query matching one million rows runs
- **THEN** the first page renders without waiting for the remaining rows, and
  the interface stays responsive

#### Scenario: Scrolling loads more
- **WHEN** the user scrolls to the last loaded row and more rows remain
- **THEN** the next page is fetched and appended

### Requirement: Row Cap With Explicit Continuation
The system SHALL stop fetching at a configurable row cap (default 10 000),
mark the result as truncated, and continue only when the user asks for more.

#### Scenario: Cap reached
- **WHEN** a result exceeds the cap
- **THEN** loading stops, the status line states how many rows are loaded and
  that the result is truncated, and a documented key resumes loading

#### Scenario: Cancellation releases the cursor
- **WHEN** the user leaves the result or cancels while rows remain unfetched
- **THEN** the open cursor is closed and its resources released

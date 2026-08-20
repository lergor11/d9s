# query-history — Delta

## ADDED Requirements

### Requirement: Persistent Query History
The system SHALL append every executed statement (successful or failed) to a
local history file (`~/.local/share/d9s/history.jsonl`) with timestamp,
connection name, database, and outcome. The file MUST NOT contain resolved
secrets.

#### Scenario: Statement recorded
- **WHEN** the user runs `SELECT 1;` on connection `prod-pg`
- **THEN** a history entry with the statement, connection, database, success
  flag, and duration is appended

#### Scenario: History survives restart
- **WHEN** d9s is restarted
- **THEN** previously recorded history is available in the history view

### Requirement: History Search And Reuse
The query view SHALL provide a history overlay (Ctrl+H) with case-insensitive
substring filtering across statements, most recent first, de-duplicated
(latest occurrence wins). Selecting an entry SHALL insert its statement into
the editor without executing it.

#### Scenario: Filter and insert
- **WHEN** the user opens history, types `users`, and presses Enter on a match
- **THEN** the overlay closes and the selected statement replaces the editor
  buffer, not yet executed

#### Scenario: Empty filter shows recent
- **WHEN** the overlay is opened with no filter text
- **THEN** the most recent entries are listed first

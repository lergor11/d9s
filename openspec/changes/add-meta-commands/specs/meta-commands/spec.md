# meta-commands — Delta

## ADDED Requirements

### Requirement: Backslash Commands Answered Locally
The system SHALL treat a statement whose first non-blank character is a
backslash as a meta-command, answering it from the driver's catalog rather
than sending it to the engine. The result SHALL be an ordinary result table,
so export, copy, sorting, and filtering apply to it unchanged.

#### Scenario: Listing tables
- **WHEN** the user runs `\dt`
- **THEN** the tables of the current database are shown as a result table, and
  nothing is sent to the engine as SQL

#### Scenario: Describing a table
- **WHEN** the user runs `\d users`
- **THEN** the columns of `users` are shown with their types and nullability

#### Scenario: Verbose description
- **WHEN** the user runs `\d+ users`
- **THEN** the same columns are shown together with the extra detail the
  engine offers, such as size, indexes, and comments

#### Scenario: Mixed with SQL
- **WHEN** a buffer holds `\dt;` followed by `SELECT 1;`
- **THEN** both run in order and each produces its own labeled result section

### Requirement: Unknown Commands Are Explained
An unrecognised backslash command SHALL produce an error naming it and
suggesting the closest supported command, without contacting the engine.

#### Scenario: Typo suggests a command
- **WHEN** the user runs `\dtt`
- **THEN** the error names `\dtt` as unknown and points at `\dt`

#### Scenario: Help lists what exists
- **WHEN** the user runs `\?`
- **THEN** every supported command is listed with a one-line description

### Requirement: Per-Engine Coverage
Each engine SHALL answer the verbs its catalog supports and SHALL say plainly
when it cannot answer one, rather than returning an empty result. ClickHouse
answers from `system.tables` and `system.columns`; Redis answers `\l` with its
logical databases and `\dt` with key prefixes.

#### Scenario: ClickHouse describes a table
- **WHEN** the user runs `\d events` on a clickhouse connection
- **THEN** the columns are listed from the engine's own catalog

#### Scenario: Unsupported verb is explicit
- **WHEN** the user runs `\du` on a redis connection
- **THEN** the error says redis has no roles to list, naming the engine

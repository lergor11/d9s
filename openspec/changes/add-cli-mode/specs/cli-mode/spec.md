# cli-mode — Delta

## ADDED Requirements

### Requirement: Non-Interactive Subcommands
The system SHALL provide subcommands that complete without a terminal
interface: listing connections, listing databases of a connection, listing
tables, describing a table, and running a query. Invoking d9s with no
subcommand SHALL continue to open the interactive interface.

#### Scenario: List connections without a terminal
- **WHEN** `d9s connections` runs with output piped to a file
- **THEN** the configured connections are written to stdout and the process
  exits without drawing an interface

#### Scenario: Interactive mode unchanged
- **WHEN** `d9s` runs with no subcommand
- **THEN** the interactive interface opens as before

#### Scenario: Unknown connection named
- **WHEN** a subcommand names a connection absent from the config
- **THEN** the error names it, lists the configured names, and the exit code
  signals a usage error

### Requirement: Query Input Sources
`d9s query` SHALL accept SQL as a positional argument, from a file given with
`-f`, or from stdin when neither is present, and SHALL run multiple statements
in one invocation using the same splitting rules as the interface.

#### Scenario: SQL from stdin
- **WHEN** `echo 'SELECT 1' | d9s query prod-pg` runs
- **THEN** the statement executes and its result is written to stdout

#### Scenario: Several statements
- **WHEN** a file holds three `;`-separated statements
- **THEN** all three run in order, each result labeled, stopping at the first
  failure

### Requirement: Output Formats
The system SHALL support `table`, `csv`, `json`, and `jsonl` output, defaulting
to `table` when stdout is a terminal and `jsonl` otherwise, so piped output is
machine-readable without a flag.

#### Scenario: Piped output is machine-readable
- **WHEN** `d9s query prod-pg 'SELECT 1 AS x' | jq .` runs
- **THEN** stdout carries one JSON object per row and nothing else; any
  progress or warning text goes to stderr

#### Scenario: Explicit format wins
- **WHEN** `-o csv` is passed while attached to a terminal
- **THEN** CSV is written

### Requirement: Read-Only Unless Asked
The system SHALL refuse statements it considers destructive unless `--write` is
passed, naming the statement and the flag in the error, because a
non-interactive caller cannot answer the interface's confirmation prompt.

#### Scenario: Destructive statement refused
- **WHEN** `d9s query prod-pg 'DROP TABLE users'` runs without `--write`
- **THEN** nothing executes, the error names the statement and `--write`, and
  the exit code signals refusal

#### Scenario: Write allowed explicitly
- **WHEN** the same command runs with `--write`
- **THEN** the statement executes

### Requirement: Distinguishable Exit Codes
The system SHALL exit 0 on success, and use distinct non-zero codes for a
usage error, a connection failure, a statement error, and a refused
destructive statement.

#### Scenario: Connection failure distinguishable from query failure
- **WHEN** the database is unreachable
- **THEN** the exit code differs from the code produced by a syntactically
  invalid query against a reachable database

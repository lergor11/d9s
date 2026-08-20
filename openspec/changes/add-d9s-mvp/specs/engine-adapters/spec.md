# engine-adapters — Delta

## ADDED Requirements

### Requirement: Common Driver Interface
The system SHALL access every engine through a single `Driver` interface with
at minimum: `Connect(ctx, target)`, `ListDatabases(ctx)`,
`Execute(ctx, statement)`, `Close()`. The UI MUST NOT contain engine-specific
branching beyond display labels.

#### Scenario: Engine-agnostic UI
- **WHEN** a new engine adapter is registered
- **THEN** it appears in the UI with no UI-code changes other than registration

### Requirement: PostgreSQL Adapter
The system SHALL support PostgreSQL via pgx: list databases from
`pg_database` (non-template), execute arbitrary SQL, return rows with column
names or the affected-row count for non-SELECT statements.

#### Scenario: List Postgres databases
- **WHEN** the user opens a postgres connection
- **THEN** non-template databases are listed with owner and size

#### Scenario: Execute SQL
- **WHEN** the user runs `SELECT 1 AS x`
- **THEN** a one-row result with column `x` is displayed

### Requirement: ClickHouse Adapter
The system SHALL support ClickHouse via clickhouse-go (native protocol):
list databases from `system.databases`, execute arbitrary SQL.

#### Scenario: List ClickHouse databases
- **WHEN** the user opens a clickhouse connection
- **THEN** entries of `system.databases` are listed

### Requirement: Redis Adapter
The system SHALL support Redis via go-redis: "databases" are logical DB
indexes 0–15 (annotated with key counts from `INFO keyspace`); statements are
raw Redis commands (e.g. `GET key`, `SCAN 0 MATCH user:*`).

#### Scenario: List Redis databases
- **WHEN** the user opens a redis connection
- **THEN** DB indexes are listed, with key counts for non-empty ones

#### Scenario: Execute Redis command
- **WHEN** the user runs `SET k v` then `GET k`
- **THEN** replies are rendered (`OK`, then `v`), including nested arrays for
  commands like `SCAN`

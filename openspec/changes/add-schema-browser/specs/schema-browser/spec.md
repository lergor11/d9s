# schema-browser — Delta

## ADDED Requirements

### Requirement: Table And Column Browsing
For SQL engines the system SHALL list tables of the current database (with a
row-count estimate where cheap) and, per table, its columns with name, data
type, and nullability. The schema panel SHALL be reachable from the query view
with a single key (`s`) and navigable with the standard movement keys.

#### Scenario: Browse Postgres columns
- **WHEN** the user opens the schema panel on a postgres database and selects a table
- **THEN** the table's columns are listed with type and nullability

#### Scenario: Insert SELECT template
- **WHEN** the user presses `i` on a highlighted table
- **THEN** `SELECT <cols or *> FROM <table> LIMIT 100;` is inserted into the
  editor without executing

### Requirement: Redis Key Browsing
For Redis the schema panel SHALL browse keys grouped by prefix (up to the
first `:`), using cursor-based SCAN (never KEYS), showing type and TTL for
individual keys.

#### Scenario: Browse keys by prefix
- **WHEN** the user opens the schema panel on a redis database containing
  `user:1`, `user:2`, `session:9`
- **THEN** prefixes `user` (2) and `session` (1) are listed; drilling into
  `user` shows both keys with their types and TTLs

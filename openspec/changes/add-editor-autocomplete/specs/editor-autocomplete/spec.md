# editor-autocomplete — Delta

## ADDED Requirements

### Requirement: Context-Aware Tab Completion
Pressing Tab in the query editor SHALL complete the identifier at the cursor
using candidates appropriate to the surrounding SQL: table names (optionally
database-qualified) after `FROM`, `JOIN`, `INTO`, `UPDATE`, and `TABLE`;
column names after `SELECT`, `WHERE`, `ON`, `HAVING`, `GROUP BY`, `ORDER BY`,
and `SET`; and SQL keywords when no schema context applies. Completion SHALL
never execute a statement.

#### Scenario: Tables offered after FROM
- **WHEN** the buffer is `SELECT * FROM ` with the cursor at the end and the
  database holds `users` and `orders`
- **THEN** a popup lists `orders` and `users`

#### Scenario: Prefix narrows candidates
- **WHEN** the buffer is `SELECT * FROM us` and the tables are `users`,
  `user_roles`, and `orders`
- **THEN** only `user_roles` and `users` are offered, and the shared prefix
  `user` is inserted immediately

#### Scenario: Unique match is inserted without a popup
- **WHEN** the buffer is `SELECT * FROM ord` and only `orders` matches
- **THEN** `orders` is inserted and no popup opens

#### Scenario: Columns offered for the queried table
- **WHEN** the buffer is `SELECT  FROM users` with the cursor after `SELECT `
- **THEN** the columns of `users` are offered

#### Scenario: Keywords when no schema context applies
- **WHEN** the buffer is `SEL` at the start of a statement
- **THEN** `SELECT` is offered

### Requirement: Alias-Qualified Column Completion
When an identifier before the cursor ends in a dot, the system SHALL offer the
columns of the table that the qualifier names, resolving table aliases
declared anywhere in the current statement.

#### Scenario: Alias resolves to its table
- **WHEN** the buffer is `SELECT u. FROM users u` with the cursor after `u.`
- **THEN** the columns of `users` are offered

#### Scenario: Unknown qualifier offers nothing
- **WHEN** the qualifier matches no table or alias in the statement
- **THEN** no popup opens and the buffer is unchanged

### Requirement: Completion Popup Navigation
When several candidates match, the system SHALL show a popup listing them with
the first selected. Tab and arrow keys SHALL move the selection, Enter SHALL
insert the selected candidate, and Esc SHALL close the popup leaving the
buffer unchanged. Typing SHALL narrow the list and close the popup when
nothing matches.

#### Scenario: Select and insert
- **WHEN** the popup is open and the user moves to the second candidate and
  presses Enter
- **THEN** that candidate replaces the partial identifier and the popup closes

#### Scenario: Esc abandons completion
- **WHEN** the popup is open and the user presses Esc
- **THEN** the popup closes, the buffer keeps exactly what was typed, and the
  editor stays focused

### Requirement: Non-Blocking Schema Cache
Completion candidates SHALL come from a per-session cache populated
asynchronously from the driver's table and column listings. A Tab press while
the cache is still loading SHALL show a loading indicator rather than freezing
the interface, and the cache SHALL be refreshable on demand.

#### Scenario: First Tab while the cache is cold
- **WHEN** the user presses Tab before the schema has loaded
- **THEN** the interface stays responsive, a loading hint appears, and the
  popup opens by itself once candidates arrive

#### Scenario: Cache refresh picks up a new table
- **WHEN** the user creates a table and refreshes the cache
- **THEN** the new table is offered by the next completion

### Requirement: Redis Command And Key Completion
For Redis connections Tab SHALL complete command names at the start of a line
and, for commands whose next argument is a key, keys found by a bounded
cursor-based `SCAN` of the typed prefix.

#### Scenario: Command completion
- **WHEN** the line is `GE` and the cursor is at the end
- **THEN** `GET`, `GETDEL`, `GETEX`, and `GETRANGE` are offered

#### Scenario: Key completion
- **WHEN** the line is `GET user:` and keys `user:1` and `user:2` exist
- **THEN** both keys are offered without issuing `KEYS`

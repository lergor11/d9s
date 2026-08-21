# mcp-server — Delta

## ADDED Requirements

### Requirement: Stdio MCP Server
`d9s mcp` SHALL run an MCP server over stdio exposing `list_connections`,
`list_databases`, `list_tables`, `describe_table`, and `query`, each with a
schema describing its arguments. Connections SHALL be opened lazily on first
use and reused across calls, and closed when the server exits.

#### Scenario: Tool discovery
- **WHEN** a client lists the server's tools
- **THEN** all five are returned with argument schemas and descriptions

#### Scenario: Connection reused across calls
- **WHEN** two `query` calls name the same connection
- **THEN** the second reuses the open session rather than re-resolving secrets
  and re-dialing the bastion

#### Scenario: Protocol stream stays clean
- **WHEN** the server writes diagnostics
- **THEN** they go to stderr, leaving stdout carrying protocol messages only

### Requirement: Read-Only By Default
The server SHALL refuse statements that `db.Destructive` flags unless the
process was started with `--allow-write` **and** the target connection sets
`allow_write: true`. The refusal SHALL name the statement and explain both
conditions.

#### Scenario: Destructive statement refused
- **WHEN** an agent calls `query` with `DELETE FROM users` on a default
  connection
- **THEN** nothing executes and the response explains the refusal

#### Scenario: Write requires both switches
- **WHEN** the server runs with `--allow-write` but the connection does not set
  `allow_write`
- **THEN** the statement is still refused

### Requirement: Bounded Results
Every tool response SHALL be bounded by a row cap (default 200) and a byte cap
(default 100 KB), and SHALL state when a result was truncated, including the
number of rows returned.

#### Scenario: Large result truncated and labeled
- **WHEN** a query matches 100 000 rows
- **THEN** at most the row cap is returned and the response says the result was
  truncated

#### Scenario: Wide values truncated
- **WHEN** a single cell exceeds the byte cap
- **THEN** the value is cut, marked as cut, and the response stays within the
  cap

### Requirement: Connection Allowlist
The server SHALL accept `--connections` naming the connections it may use, and
SHALL behave as though the others do not exist, including in
`list_connections`.

#### Scenario: Production hidden
- **WHEN** the server starts with `--connections staging-pg`
- **THEN** `list_connections` returns only `staging-pg`, and a `query` naming
  `prod-pg` fails as an unknown connection

### Requirement: Secrets Never Exposed
No tool response, error message, or log line SHALL contain a resolved
password, certificate material, or the contents of an SSH key. Configuration
echoed back SHALL show the reference (`op://…`) rather than the value.

#### Scenario: Connection details show references only
- **WHEN** `list_connections` returns a connection using a 1Password password
- **THEN** the response shows the `op://` reference, never the secret

#### Scenario: Failure text carries no secret
- **WHEN** authentication fails
- **THEN** the error explains the failure without echoing the password

### Requirement: Bundled Agent Skill
The repository SHALL ship a Claude Code skill documenting the tools, the
read-only contract, how to point the server at a connection, and the expected
workflow of listing tables and describing them before querying. Installation
SHALL be documented for both the skill and the MCP server registration.

#### Scenario: Skill explains setup
- **WHEN** a user follows the documented steps
- **THEN** `claude mcp add` registers `d9s mcp` and the skill appears in the
  agent's skill list

#### Scenario: Skill states the safety contract
- **WHEN** an agent reads the skill
- **THEN** it learns that queries are read-only by default and how writes are
  enabled

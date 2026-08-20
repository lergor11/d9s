# connection-config — Delta

## ADDED Requirements

### Requirement: YAML Connection Configuration
The system SHALL load connection profiles from `~/.config/d9s/config.yaml`
(overridable via `--config` flag or `D9S_CONFIG` env var). Each connection MUST
declare `name`, `type` (`postgres` | `clickhouse` | `redis`), `host`, and MAY
declare `port` (engine default when omitted), `user`, `password`, `database`,
`ssh`, and engine-specific options.

#### Scenario: Valid config loads
- **WHEN** d9s starts and the config file contains valid connections
- **THEN** all connections appear in the connection list, in file order

#### Scenario: Missing config file
- **WHEN** d9s starts and no config file exists
- **THEN** d9s starts with an empty connection list and shows the config path
  and a sample snippet instead of an error

#### Scenario: Invalid config
- **WHEN** the config file has invalid YAML or an unknown `type`
- **THEN** d9s exits with a clear error naming the file, line, and problem

### Requirement: No Plaintext Secrets In Config
Config values for `password` MUST be either an `op://` secret reference or an
`${ENV_VAR}` reference. A literal plaintext password SHALL cause a startup
warning naming the offending connection.

#### Scenario: op:// reference accepted
- **WHEN** a connection has `password: op://Infra/prod-pg/password`
- **THEN** the reference is stored as-is and resolved only at connect time

#### Scenario: Plaintext password warned
- **WHEN** a connection has a literal `password: hunter2`
- **THEN** d9s still works but shows a warning suggesting an `op://` reference

### Requirement: SSH Block Per Connection
The system SHALL accept an optional `ssh` block per connection with `bastion`
(host, required), `user`, and `port` (default 22). When the block is present,
the system SHALL route all traffic to the database host through the bastion.

#### Scenario: SSH block parsed
- **WHEN** a connection declares `ssh: {bastion: bastion.corp.com, user: deploy}`
- **THEN** connecting to that database dials through the bastion tunnel

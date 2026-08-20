# secret-resolution — Delta

## ADDED Requirements

### Requirement: 1Password Secret References
The system SHALL resolve `op://vault/item/field` references by invoking the
1Password CLI (`op read <ref>`) at connect time. Resolved secrets MUST be kept
only in memory, MUST NOT be logged, and MUST NOT be written to disk.

#### Scenario: Successful resolution
- **WHEN** the user connects to a database whose password is `op://Infra/prod-pg/password`
- **THEN** d9s runs `op read op://Infra/prod-pg/password` and uses its output
  (trailing newline stripped) as the password

#### Scenario: op CLI missing
- **WHEN** `op` is not on PATH and a connection needs an `op://` reference
- **THEN** the connection attempt fails with a message explaining how to
  install/enable the 1Password CLI, and d9s stays running

#### Scenario: op read fails
- **WHEN** `op read` exits non-zero (locked vault, bad reference)
- **THEN** the error output is shown to the user and no partial connection is kept

### Requirement: Environment Variable References
The system SHALL resolve `${ENV_VAR}` references in `password` from the
process environment at connect time.

#### Scenario: Env var resolution
- **WHEN** a connection has `password: ${PGPASS}` and `PGPASS` is set
- **THEN** the value of `PGPASS` is used as the password

### Requirement: Secret Caching Per Session
The system SHALL cache resolved secrets in memory for the lifetime of the d9s
process to avoid repeated 1Password prompts, and SHALL discard them on exit.

#### Scenario: Reconnect uses cache
- **WHEN** the user reconnects to the same connection within one session
- **THEN** the previously resolved secret is reused without invoking `op` again

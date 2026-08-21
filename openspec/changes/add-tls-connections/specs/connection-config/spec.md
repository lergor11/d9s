# connection-config — Delta

## ADDED Requirements

### Requirement: TLS Configuration Per Connection
The system SHALL accept an optional `tls` block per connection with `mode`
(one of `disable`, `require`, `verify-ca`, `verify-full`), and optional `ca`,
`cert`, `key`, and `server_name` paths. When the block is absent, the mode
SHALL default to `disable` for connections carrying an `ssh` block and
`require` for all others. An unknown mode SHALL fail configuration loading
with an error naming the connection.

#### Scenario: Cloud database without explicit configuration
- **WHEN** a connection has no `ssh` block and no `tls` block
- **THEN** the connection negotiates TLS in `require` mode

#### Scenario: Tunneled connection stays plaintext
- **WHEN** a connection declares an `ssh` block and no `tls` block
- **THEN** no TLS is negotiated, because the SSH tunnel already encrypts the
  stream

#### Scenario: Invalid mode rejected
- **WHEN** a connection declares `tls: {mode: yes-please}`
- **THEN** loading fails with an error naming the connection and the accepted
  modes

### Requirement: Certificate Verification Modes
In `verify-ca` the system SHALL validate the server certificate chain against
the configured `ca` (or the system roots when unset); in `verify-full` it
SHALL additionally verify the hostname, using `server_name` when given. In
`require` the system SHALL encrypt without verifying, and the UI SHALL mark
such connections as unverified.

#### Scenario: Hostname mismatch rejected under verify-full
- **WHEN** a connection uses `verify-full` and the server certificate does not
  match the host
- **THEN** connecting fails with an error naming the expected and presented
  names

#### Scenario: Unverified connection is flagged
- **WHEN** a connection is established in `require` mode
- **THEN** the connection list marks it as encrypted but unverified

### Requirement: Client Certificates From 1Password
The system SHALL accept `op://` references for `cert` and `key`, resolving
them at connect time into memory without writing certificate material to disk.

#### Scenario: Client certificate resolved from 1Password
- **WHEN** a connection sets `cert: op://Infra/db/cert` and `key: op://Infra/db/key`
- **THEN** both are read through the 1Password CLI at connect time and used
  for mutual TLS, and neither is written to disk

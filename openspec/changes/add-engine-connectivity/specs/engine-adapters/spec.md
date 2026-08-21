# engine-adapters — Delta

## ADDED Requirements

### Requirement: ClickHouse Protocol Selection
The ClickHouse adapter SHALL accept `protocol: native` (default, port 9000) or
`protocol: http` (port 8123), using the selected transport for every
operation, including through an SSH tunnel.

#### Scenario: HTTP transport
- **WHEN** a connection sets `protocol: http` and port 8123
- **THEN** databases list and queries run over HTTP

#### Scenario: Default stays native
- **WHEN** no protocol is given
- **THEN** the native protocol on port 9000 is used

### Requirement: Redis Deployment Modes
The Redis adapter SHALL support `mode: standalone` (default), `cluster`, and
`sentinel`. Cluster and sentinel accept several addresses, sentinel requires
`master_name`, and cluster SHALL list only database index 0, because Redis
Cluster has no other logical databases.

#### Scenario: Cluster lists one database
- **WHEN** a connection uses `mode: cluster`
- **THEN** only index 0 is listed and commands run against the cluster client

#### Scenario: Sentinel without a master name is rejected
- **WHEN** a connection uses `mode: sentinel` without `master_name`
- **THEN** loading the configuration fails with an error naming the connection

### Requirement: Postgres Unix Socket
The Postgres adapter SHALL treat a `host` beginning with `/` as a unix socket
directory rather than a TCP host.

#### Scenario: Socket connection
- **WHEN** `host: /var/run/postgresql`
- **THEN** the connection is made over the unix socket and no TCP dial occurs

### Requirement: Configurable Connect Timeout
Each connection SHALL accept `connect_timeout` (default 10 seconds) bounding
the connect and handshake phase across all engines.

#### Scenario: Timeout surfaces as an error
- **WHEN** a host is unreachable and the timeout elapses
- **THEN** the connection attempt fails with a timeout error naming the host,
  and the interface stays responsive

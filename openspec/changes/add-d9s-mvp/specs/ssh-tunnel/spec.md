# ssh-tunnel — Delta

## ADDED Requirements

### Requirement: Bastion Tunneling Via SSH Agent
The system SHALL establish SSH connections to bastion hosts using an SSH agent
socket, preferring the 1Password agent at `~/.1password/agent.sock` and falling
back to `$SSH_AUTH_SOCK`. Private key files MUST NOT be read by the application.

#### Scenario: Tunnel through 1Password agent
- **WHEN** a connection declares an `ssh` block and `~/.1password/agent.sock` exists
- **THEN** d9s authenticates to the bastion via that agent socket and dials the
  database host from inside the SSH connection (direct-tcpip), with no local
  listening port

#### Scenario: Fallback to SSH_AUTH_SOCK
- **WHEN** the 1Password agent socket does not exist but `$SSH_AUTH_SOCK` is set
- **THEN** d9s uses `$SSH_AUTH_SOCK` for agent authentication

#### Scenario: No agent available
- **WHEN** neither agent socket is available
- **THEN** the connection fails with a message explaining how to enable the
  1Password SSH agent

### Requirement: Tunnel Lifecycle
SSH tunnels SHALL be established lazily on first connect, shared by all
database sessions of the same connection, and closed when the connection is
disconnected or d9s exits.

#### Scenario: Shared tunnel
- **WHEN** the user opens two database sessions on the same connection
- **THEN** both use one SSH client (no second bastion handshake)

#### Scenario: Tunnel drop surfaces error
- **WHEN** the SSH connection drops mid-session
- **THEN** the next query fails with a reconnect hint rather than hanging

## Context

The command line and the interface both need the same connect path: resolve
the password, optionally raise an SSH tunnel, dial the driver. `internal/session`
now holds that path and the CLI uses it; `internal/ui` still carries its own
copy in `connections.go` and `databases.go`. Task 5.1 of this change migrates
the interface onto `internal/session` so the two cannot drift.

This note records where the two implementations differ **today**, so the
migration preserves behaviour instead of quietly standardising on the CLI's.
Nothing here is a bug in the shipped code: the interface's copy is correct for
the interface, and `session.Open` is correct for a one-shot process.

## Decisions the migration must make

### Tunnel ownership — the load-bearing one

The interface creates one tunnel per **connection** (`connections.go`, guarded
by `cs.tunnel == nil` so it survives reconnects), shares it with every database
session opened under that connection, and closes it once at teardown.
`session.Open` creates one tunnel per **session** and closes it in `Close`.

Adopting the session behaviour unchanged would turn opening five databases into
five bastion handshakes. `session` MUST therefore accept a tunnel it does not
own — a borrowed-tunnel field, or an `Open` variant — before the interface can
move. Per-session tunnels are not an acceptable simplification.

### Closing a borrowed tunnel is unrecoverable

`sshtunnel.Close` is terminal, not merely a teardown: it sets `closed` and every
later `Dial` returns "tunnel is closed" rather than re-establishing. On a failed
connect the interface deliberately closes only the driver and keeps the tunnel
for the retry; `session.Open` closes both.

So a borrowed tunnel plus `session.Open`'s cleanup means **one failed database
open permanently poisons the bastion for every other session on that
connection**. `Close` must skip a tunnel the session did not create. This is the
single most likely way to break the interface while "only refactoring".

### Timeouts come from different places

The interface bounds the whole operation — secret resolution, handshake,
connect, and `ListDatabases` — with a hard-coded 60s. The CLI passes
`context.Background()` unless `-timeout` is given and relies on the driver's own
`connect_timeout` (default 10s), applied inside `Connect`. An `op://` resolve
waiting on a biometric prompt is therefore capped at 60s in the interface and
unbounded in the CLI.

`session.Open` should keep inventing no timeout of its own and let the caller
supply the context: the interface then passes its 60s and nothing changes. That
should be a decision, not an accident.

### Error prefixes will double up

`session.Open` wraps a resolver failure as `resolving the password of "prod-pg":
…`. The interface returns it bare and adds the connection name when rendering
the status line, which after migration reads `prod-pg: resolving the password of
"prod-pg": …`. The database-open path has the same shape. Drop the interface's
prefix: `session`'s names the connection even where the status line does not.

### `ListDatabases` stays inside the connect command

The interface's connect command returns the driver *and* the database list in
one message. `session.Open` stops at `Connect`. The UI can call
`s.Driver.ListDatabases` itself, but both calls must stay inside the same
`tea.Cmd`, or connecting becomes two round trips and the spinner gains a state
it has never had.

### Dropping the cached password is a simplification, not a regression

The interface caches the resolved plaintext on `connState` and passes it to each
database open; `session.Open` always calls the resolver. That is safe to adopt:
`secrets.Resolver` caches internally, and only the `op://` branch is expensive —
`${ENV}` is an environment lookup and a literal is a return. Do not preserve the
cache out of caution and thread a pre-resolved password through `session.Open`.

## Already aligned

Database selection: the interface's connection-level session passes no database
and the per-database one passes the name, and `session.Open` passes its argument
straight through. Both rely on the same driver-side fallback to
`Config.Database`. Identical behaviour — leave it alone.

## Risks / Trade-offs

- Borrowed-tunnel handling adds a small amount of ownership bookkeeping to
  `session`; the alternative (per-session tunnels) is a user-visible regression.
- The migration touches the files the interface work lands in, so it must run
  after that work, not beside it.

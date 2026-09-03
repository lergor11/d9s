# d9s

A k9s-style terminal UI for databases. Open-source, terminal-first alternative
to DataGrip for PostgreSQL, ClickHouse, and Redis — with first-class SSH
bastion support and 1Password-backed secrets.

![d9s scanning 8 billion rows in ClickHouse: live rows/bytes/percent/memory in the status line, then the engine's profile events](docs/demo.gif)

## Features (MVP)

- Connection list → database list → query view, fully keyboard-driven
- PostgreSQL, ClickHouse (native protocol), Redis
- SSH bastion tunneling authenticated via the **1Password SSH agent**
  (private keys never leave 1Password; no local port forwarding)
- Database passwords as `op://vault/item/field` references resolved through
  the 1Password CLI at connect time — no plaintext secrets on disk
- Run a single statement or a multi-statement script with per-statement
  results and timings
- Engine-native query plans (`F6`): PostgreSQL static `EXPLAIN` and explicit
  `ANALYZE, BUFFERS`; ClickHouse `PLAN`, `PIPELINE`, and `ESTIMATE`
- Authoritative PostgreSQL transaction status and explicit controls (`F7`):
  begin, commit, rollback, savepoint, rollback-to, and release
- Interactive confirmation before destructive statements
  (DROP / TRUNCATE / DELETE without WHERE / FLUSHALL / …)
- Searchable query history (`Ctrl+H`) persisted to
  `~/.local/share/d9s/history.jsonl` (honors `XDG_DATA_HOME`)
- Schema panel (`s`) browsing tables and their columns — Redis key prefixes and
  their keys — with `i` inserting a ready `SELECT … LIMIT 100`
- Export a result to CSV or JSON (`e`) or copy it as CSV to the clipboard (`y`,
  via pbcopy / wl-copy / xclip)
- TLS to managed databases (RDS, Cloud SQL, Neon, Supabase, ClickHouse Cloud,
  Redis Cloud), with `verify-ca` and `verify-full` verification and client
  certificates that may themselves come from 1Password
- `Tab` completion in the editor: tables after `FROM`, columns after `SELECT`
  and `WHERE`, aliases resolved (`SELECT u.<Tab> FROM users u`), Redis commands
  and keys
- ClickHouse over the native protocol or HTTP; Redis standalone, Cluster, or
  Sentinel; PostgreSQL over TCP or a unix socket
- Live progress while a ClickHouse query runs — rows and bytes read, a
  percentage when the engine knows the total, memory, elapsed time — with the
  counters preserved on cancel; profile events on demand (`p`) and server log
  lines under the result. These arrive over the native protocol only: over
  HTTP (and on engines without a progress stream, like PostgreSQL) the display
  degrades honestly to elapsed time

### What a running query looks like

While a ClickHouse scan runs, the status line counts what the server is
actually doing, so a heavy query and a stuck one stop looking the same:

```text
[1] SELECT number % 10 AS k, count() FROM numbers(2000000000) GROUP BY k
running...

⠸ running 1.24B rows · 9.2 GB · 62% · mem 428 MB · 4.1s (ctrl+x to cancel)
```

Cancel it and the section keeps the counters instead of pretending nothing
happened; finish it and `p` opens the engine's own accounting:

```text
[1] SELECT number % 10 AS k, count() FROM numbers(2000000000) GROUP BY k
context canceled
5.0s · read 1.51B rows (11.2 GB) before it stopped

┌ profile · statement 1 ─────────────────┐
│ SelectedRows              2000000000   │
│ SelectedBytes            16000000000   │
│ MemoryTracking             448790528   │
│ NetworkSendBytes                4096   │
│ ProfileInfo.Rows                  10   │
└ ↑/↓ scroll · y copy the raw value ─────┘
```

The recording at the top of this page is generated with
[vhs](https://github.com/charmbracelet/vhs): `docs/demo.tape` has the steps
and prerequisites, and `vhs docs/demo.tape` re-records `docs/demo.gif`.

## Install

Homebrew:

```sh
brew install lergor11/tap/d9s
```

A release archive, verified against its checksum:

```sh
VERSION=0.2.0 OS=darwin ARCH=arm64            # or linux / amd64
BASE=https://github.com/lergor11/d9s/releases/download/v$VERSION
curl -sSLO $BASE/d9s_${VERSION}_${OS}_${ARCH}.tar.gz
curl -sSLO $BASE/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar xzf d9s_${VERSION}_${OS}_${ARCH}.tar.gz && sudo mv d9s /usr/local/bin/
```

From source (Go 1.25+):

```sh
make build   # produces ./d9s, stamped as a development build
```

For 1Password integration: the 1Password desktop app with the SSH agent and
CLI integration enabled (`op` on PATH).

Starting `./d9s` with no configuration is safe: it explains where the config
file goes and shows a sample.

## Try it against local containers

```sh
docker run -d --rm --name d9s-pg -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
docker run -d --rm --name d9s-redis -p 16379:6379 redis:7-alpine
docker run -d --rm --name d9s-ch -p 19000:9000 clickhouse/clickhouse-server:24-alpine

DEMO_PG_PASSWORD=secret ./d9s --config examples/demo-docker.yaml

docker stop d9s-pg d9s-redis d9s-ch   # when done
```

## Configure

`~/.config/d9s/config.yaml` (override with `--config` or `D9S_CONFIG`):

```yaml
connections:
  - name: prod-pg
    type: postgres
    host: 10.0.1.5
    port: 5432
    user: app
    password: op://Infra/prod-pg/password
    ssh:
      bastion: bastion.corp.com   # or a ~/.ssh/config Host alias
      user: deploy                # key comes from the 1Password SSH agent
  - name: analytics-ch
    type: clickhouse
    host: ch.internal
    user: default
    password: ${CH_PASSWORD}
  - name: cache-redis
    type: redis
    host: 127.0.0.1
```

`password` accepts an `op://` reference, an `${ENV_VAR}` reference, or (with a
warning) a literal.

`bastion` accepts a `~/.ssh/config` Host alias as well as a hostname: d9s asks
`ssh -G` to resolve it, so `Hostname`, `User`, and `Port` from your ssh config
apply, including anything pulled in by `Include` or `Match`. Values set in the
d9s config win over the ones ssh_config supplies.

### TLS

A connection without an `ssh` block negotiates TLS in `require` mode by
default, so a managed database works with no extra configuration. A connection
behind a bastion defaults to `disable`, because the tunnel already encrypts the
stream. Set the block explicitly to verify the server:

```yaml
  - name: cloud-pg
    type: postgres
    host: db.abc123.eu-central-1.rds.amazonaws.com
    user: app
    password: op://Infra/cloud-pg/password
    tls:
      mode: verify-full          # disable | require | verify-ca | verify-full
      ca: /etc/ssl/rds-ca.pem    # omit to use the system roots
      # server_name: db.internal # when the certificate names another host
      # cert: op://Infra/cloud-pg/cert   # mutual TLS; cert and key together
      # key:  op://Infra/cloud-pg/key
```

`require` encrypts without checking who is on the other end, so the connection
list marks such connections as unverified; prefer `verify-full` for anything
that matters. The mode is also a field in the connection form, so `e` on a
connection lets you switch it with the arrow keys without editing YAML;
`default` there means no `tls` block at all. `ca`, `cert`, and `key` take a file path or an `op://` reference,
and certificate material is never written to disk.

### Per-engine connectivity

```yaml
  - name: ch-http                # ClickHouse behind an HTTP load balancer
    type: clickhouse
    host: ch.internal
    protocol: http               # native (default) | http
    tls:
      mode: disable              # drop this and it is HTTPS on 8443

  - name: redis-cluster
    type: redis
    host: node1.internal
    mode: cluster                # standalone (default) | cluster | sentinel
    addresses: [node2.internal:6379, node3.internal:6379]

  - name: redis-sentinel
    type: redis
    host: sentinel1.internal
    mode: sentinel
    master_name: mymaster        # required for sentinel

  - name: local-socket           # a host starting with / is a unix socket
    type: postgres
    host: /var/run/postgresql
    user: postgres

  - name: slow-link
    type: postgres
    host: db.far.away
    connect_timeout: 30s         # default 10s, applies to every engine
```

Redis Cluster has no logical databases, so a cluster connection lists index 0
only.

The port follows the protocol and the TLS mode, so it rarely needs saying:
ClickHouse serves the native protocol on 9000 and 9440 encrypted, and HTTP on
8123 and 8443 encrypted. Since a direct connection defaults to `require`,
`protocol: http` alone means HTTPS on 8443; add `tls: {mode: disable}` for a
plaintext endpoint. Both fields are in the connection form too, so `e` on a
ClickHouse connection switches protocol and TLS with the arrow keys.

## Keys

| Key | Action |
|-----|--------|
| `j`/`k`, arrows | Move |
| `Enter` | Connect / open |
| `Esc` | Back |
| `Ctrl+R` / `F5` / `Alt+Enter` | Run buffer |
| `F6` | Choose and run an engine-native query plan |
| `F7` | Open PostgreSQL transaction controls |
| `Ctrl+X` | Cancel running query or plan |
| `Ctrl+H` | Query history (type to filter, `Enter` inserts without running) |
| `s` / `Ctrl+S` | Schema panel (`Enter` drills into columns, `/` filters, `i` inserts a `SELECT`) |
| `e` | Export the focused result to a file (CSV or JSON, by extension) |
| `y` | Copy the focused result to the clipboard as CSV |
| `p` | Profile events of the focused result (with results focused) |

### Query plans

Press `F6` with exactly one SQL statement in the editor. PostgreSQL offers a
static `PLAN` and runtime `ANALYZE`; ClickHouse offers `PLAN`, `PIPELINE`, and
`ESTIMATE`. Redis reports query plans as unsupported.

The static PostgreSQL plan does not execute the statement. `ANALYZE` does, so
d9s shows a second confirmation and refuses statements it cannot classify as
read-only. This classifier is defense in depth, not a proof that a function
has no side effects: use a server-side read-only role for production access.

Plan rows use the normal result grid, so paging, scrolling, cancellation,
copy, and export work unchanged. Neither the planned SQL nor its output is
added to persistent query history automatically.

### Transactions

PostgreSQL sessions continuously show `tx:idle`, `tx:active`, or `tx:failed`
in the header. This state comes from PostgreSQL's protocol status, so manually
typed `BEGIN`, `COMMIT`, and `ROLLBACK` are reflected just like actions chosen
through `F7`. State is refreshed after successful statements, errors, query
cancellation, and transaction actions.

`F7` offers only actions appropriate to the current state. Savepoint actions
accept a name and list the names created through the controls; names are sent
as quoted PostgreSQL identifiers rather than interpolated SQL. A manually
created savepoint can still be entered by name and PostgreSQL remains the
authority on whether it exists.

Leaving the database, `Ctrl+C`, or `\q` with an open transaction shows a
rollback/commit/stay prompt. Rollback is selected by default, failed
transactions offer rollback or stay, and d9s never commits automatically. If
the connection is lost, the UI reports the transaction outcome as unknown
instead of claiming a commit or rollback succeeded. ClickHouse and Redis
explicitly report these controls as unsupported.

### psql-style meta-commands

The editor understands backslash commands and answers them from the driver's
catalog instead of sending them to the engine, on every engine:

| Command | Answer |
|---------|--------|
| `\l` | Databases |
| `\dt`, `\d` | Tables (Redis: key prefixes) |
| `\d <table>` | The table's columns, types, and nullability |
| `\d+ <table>` | The same plus size, indexes, and comments |
| `\dn` | Schemas (postgres) |
| `\du` | Roles |
| `\df` | Functions |
| `\?` | This list |
| `\q` | Quit |

They mix with SQL in one buffer — `\dt` on its own line, then a `SELECT` —
and each produces a normal result section, so export, copy, sorting and
filtering work on the answer. A typo like `\dtt` suggests the closest
command; a verb an engine cannot answer says so, naming the engine. The same
commands work in scripts: `d9s query prod-pg '\dt'`.
| `Tab` | Complete the name at the cursor (in the editor); toggle focus elsewhere |
| `Ctrl+G` | Reload the cached table and column names used for completion |
| `Ctrl+J` | Toggle editor/results focus |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

### Shift+Enter to run

Most terminals send a bare carriage return for Shift+Enter, indistinguishable
from Enter, which the editor has to keep as a newline — so d9s cannot bind it
directly. Map it in the terminal instead, to send `Ctrl+R` (`\x12`):

```
# Ghostty (~/.config/ghostty/config)
keybind = shift+enter=text:\x12
```

Without a mapping Ghostty emits `\x1b[27;2;13~` for Shift+Enter, which lands in
the editor as stray characters. `Alt+Enter` works everywhere with no terminal
configuration.

## Scripting: the command line

Every view is also a subcommand, so the same connections — 1Password secrets,
bastions, TLS and all — work from a script without a second credentials setup:

```sh
d9s connections                              # the configured connections
d9s databases prod-pg                        # databases of one connection
d9s tables prod-pg app                       # tables of a database
d9s describe prod-pg public.users            # columns of a table
d9s query prod-pg 'SELECT id FROM users LIMIT 5'
d9s query prod-pg -f report.sql              # or from a file
echo 'SELECT 1' | d9s query prod-pg          # or from stdin
d9s plan prod-pg 'SELECT * FROM users'       # static PostgreSQL EXPLAIN
d9s plan prod-pg -mode analyze 'SELECT count(*) FROM users'
d9s plan analytics-ch -mode pipeline 'SELECT * FROM events'
```

Output is a table on a terminal and JSONL when piped, so a pipeline reads
clean rows; `-o table|csv|json|jsonl` overrides it. Data goes to stdout and
everything else to stderr:

```sh
d9s query prod-pg 'SELECT id, email FROM users LIMIT 5' | jq -r .email
d9s plan prod-pg 'SELECT * FROM users' -o csv > users-plan.csv
```

Destructive statements are refused unless `--write` is given, because there is
no prompt to answer out here. Exit codes tell a script what happened: `0`
success, `2` usage error or unknown connection, `3` database unreachable, `4`
statement failed, `5` destructive statement refused.

Three more things worth knowing. `-database name` picks the database for
`describe`, `query`, and `plan`, for connections whose configuration does not
name one.
`-timeout 30s` bounds the whole command, on top of the per-connection
`connect_timeout`. And SQL that begins with a dash needs a `--` ahead of it, so
the flag parser leaves it alone:

```sh
d9s query prod-pg -database analytics 'SELECT count(*) FROM events'
d9s query prod-pg -timeout 30s -f slow-report.sql
d9s query prod-pg -- '-- monthly totals
SELECT month, sum(amount) FROM invoices GROUP BY month'
```

Flags may come before, between, or after the positional arguments, so
`d9s query prod-pg 'SELECT 1' -o csv` works as written.

`d9s plan` accepts SQL from its positional argument, `-f`, or stdin and uses
the same `-o table|csv|json|jsonl` and `-timeout` flags as `query`. Its default
mode is always the non-executing static plan. PostgreSQL runtime analysis must
be requested explicitly with `-mode analyze`; ClickHouse accepts `-mode plan`,
`pipeline`, or `estimate`.

## Agents: the MCP server

`d9s mcp` serves the same connections over MCP, so Claude Code and similar
agents can explore a schema and read data while the credentials stay in
1Password and never enter the agent's context:

```sh
claude mcp add d9s -- d9s mcp --connections staging-pg,analytics-ch
```

The tools are `list_connections`, `list_databases`, `list_tables`,
`describe_table`, and `query`. Install the bundled skill by copying
`skills/d9s/` into `~/.claude/skills/`; it teaches the workflow of listing and
describing before querying.

Three things keep an agent from doing damage:

- **Read-only by default.** `query` refuses destructive statements. A write
  needs *both* `--allow-write` at launch and `allow_write: true` on that
  connection, so neither a careless prompt nor a single forgotten flag is
  enough.
- **Bounded results.** Every response is capped at 200 rows and 100 KB and says
  when it was cut, so one `SELECT *` cannot flood the context.
- **A narrow window.** `--connections` limits the server to the connections you
  name; the rest do not exist as far as the agent is concerned.

Passwords appear in tool output only as the `op://` or `${ENV}` reference they
come from, never as values.

## Development

Spec-driven via [OpenSpec](https://github.com/Fission-AI/OpenSpec): see
`openspec/` for capabilities and pending changes. Run `make lint test` before
opening a PR.

Integration tests run against live engines and are skipped unless their ports
are set:

```sh
docker run -d --rm -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
docker run -d --rm -p 16379:6379 redis:7-alpine
docker run -d --rm -p 19000:9000 clickhouse/clickhouse-server:24-alpine

D9S_IT_PG_PORT=15432 D9S_IT_PG_PASSWORD=secret \
D9S_IT_CH_PORT=19000 D9S_IT_REDIS_PORT=16379 \
  go test -tags integration ./internal/db ./internal/cli
```

The `internal/cli` suite drives the subcommands end to end against the same
containers and asserts the exit codes.

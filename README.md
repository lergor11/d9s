# d9s

A k9s-style terminal UI for databases. Open-source, terminal-first alternative
to DataGrip for PostgreSQL, ClickHouse, and Redis — with first-class SSH
bastion support and 1Password-backed secrets.

## Features (MVP)

- Connection list → database list → query view, fully keyboard-driven
- PostgreSQL, ClickHouse (native protocol), Redis
- SSH bastion tunneling authenticated via the **1Password SSH agent**
  (private keys never leave 1Password; no local port forwarding)
- Database passwords as `op://vault/item/field` references resolved through
  the 1Password CLI at connect time — no plaintext secrets on disk
- Run a single statement or a multi-statement script with per-statement
  results and timings
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

## Install

Homebrew:

```sh
brew install andreim/tap/d9s
```

A release archive, verified against its checksum:

```sh
VERSION=0.2.0 OS=darwin ARCH=arm64            # or linux / amd64
BASE=https://github.com/andreim/d9s/releases/download/v$VERSION
curl -sSLO $BASE/d9s_${VERSION}_${OS}_${ARCH}.tar.gz
curl -sSLO $BASE/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar xzf d9s_${VERSION}_${OS}_${ARCH}.tar.gz && sudo mv d9s /usr/local/bin/
```

From source (Go 1.23+):

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
      bastion: bastion.corp.com
      user: deploy        # key comes from the 1Password SSH agent
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
that matters. `ca`, `cert`, and `key` take a file path or an `op://` reference,
and certificate material is never written to disk.

### Per-engine connectivity

```yaml
  - name: ch-http                # ClickHouse behind an HTTP load balancer
    type: clickhouse
    host: ch.internal
    port: 8123
    protocol: http               # native (default, 9000) | http

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

## Keys

| Key | Action |
|-----|--------|
| `j`/`k`, arrows | Move |
| `Enter` | Connect / open |
| `Esc` | Back |
| `Ctrl+R` / `F5` | Run buffer |
| `Ctrl+X` | Cancel running query |
| `Ctrl+H` | Query history (type to filter, `Enter` inserts without running) |
| `s` / `Ctrl+S` | Schema panel (`Enter` drills into columns, `/` filters, `i` inserts a `SELECT`) |
| `e` | Export the focused result to a file (CSV or JSON, by extension) |
| `y` | Copy the focused result to the clipboard as CSV |
| `Tab` | Complete the name at the cursor (in the editor); toggle focus elsewhere |
| `Ctrl+G` | Reload the cached table and column names used for completion |
| `Ctrl+J` | Toggle editor/results focus |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

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
  go test -tags integration ./internal/db
```

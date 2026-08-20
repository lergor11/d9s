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

## Install

```sh
make build   # produces ./d9s
```

Requires Go 1.23+. For 1Password integration: the 1Password desktop app with
the SSH agent and CLI integration enabled (`op` on PATH).

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

## Keys

| Key | Action |
|-----|--------|
| `j`/`k`, arrows | Move |
| `Enter` | Connect / open |
| `Esc` | Back |
| `Ctrl+R` / `F5` | Run buffer |
| `Ctrl+X` | Cancel running query |
| `Tab` | Toggle editor/results focus |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

## Development

Spec-driven via [OpenSpec](https://github.com/Fission-AI/OpenSpec): see
`openspec/` for capabilities and pending changes. `make test vet` before PRs.

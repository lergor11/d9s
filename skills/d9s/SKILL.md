---
name: d9s
description: Explore and query the user's PostgreSQL, ClickHouse, and Redis databases through the d9s MCP server. Use when asked to inspect a schema, look up rows, check what a table contains, or answer a question about data in a configured database. Credentials stay in 1Password and are never exposed. Queries are read-only by default.
---

# Querying databases with d9s

The `d9s` MCP server exposes the databases configured in the user's d9s config
file. It holds the credentials, the SSH bastion hops, and the TLS material, so
you get results without ever seeing a password. There is nothing for you to
authenticate and no connection string to build: you name a connection, and the
server does the rest.

## The tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `list_connections` | none | every connection this server exposes: name, engine, address, whether writes are permitted |
| `list_databases` | `connection` | the databases on that connection |
| `list_tables` | `connection`, `database` (optional) | tables with a row estimate; key prefixes on Redis |
| `describe_table` | `connection`, `database` (optional), `table` | columns with type, nullability, and default; keys under a prefix on Redis |
| `query` | `connection`, `database` (optional), `sql` | the result rows |

`database` always defaults to the one the connection configures, so omit it
unless you have a reason to look elsewhere.

## Work from names you have seen, not names you assume

Schemas are never what you would guess, and a failed query costs a round trip
and tells the user you were guessing. So:

1. `list_connections` — pick the connection. If more than one could plausibly
   be meant, ask the user rather than picking for them; one of them may be
   production.
2. `list_tables` — find the real table name. Use it exactly as returned,
   including any schema prefix such as `billing.invoices`.
3. `describe_table` — read the real column names and types before you write a
   predicate against them.
4. `query` — now write the statement.

Skip steps 2 and 3 only when the user gave you the exact table and columns.

## Queries are read-only

`query` refuses destructive statements — `DROP`, `TRUNCATE`, `ALTER`, a
`DELETE` or `UPDATE` with no `WHERE`, and the Redis equivalents such as
`FLUSHALL` and `DEL`. The refusal happens on the server before anything runs,
and it applies to every statement in the request, so a `DROP` after a harmless
`SELECT` is caught too.

Writing is possible only when the operator has opened both halves of a gate:
the server was started with `--allow-write`, **and** the target connection sets
`allow_write: true` in the d9s config. `list_connections` shows which
connections currently permit writes. If a write is refused and the user wants
it to work, tell them which half is missing — the refusal message names it —
and let them change their own configuration. Do not look for a way around it.

A refusal is a correct outcome, not an error to retry.

## Results are capped

Every response stops at **200 rows** and **100 KB**, and single values are cut
at 2 KB. When a cap fires, the response says so and tells you how many rows
came back out of how many matched — so read the last lines of a result before
you conclude the table only has 200 rows in it.

Because of the caps:

- Add an explicit `LIMIT`. You will get one anyway; asking for it means the
  engine does less work and the rows you get are the rows you chose.
- Select the columns you need instead of `SELECT *`, especially where a table
  holds JSON or blob columns.
- Aggregate on the server. `SELECT count(*) ... GROUP BY status` beats pulling
  rows back and counting them yourself, and it fits in the cap.

## Setup

Register the server with Claude Code:

```sh
claude mcp add d9s -- d9s mcp
```

Restrict it to the connections an agent may touch — a good idea, and the way to
keep production out of reach:

```sh
claude mcp add d9s -- d9s mcp --connections staging-pg,analytics-ch
```

Connections left out of `--connections` do not exist as far as the server is
concerned: they are absent from `list_connections`, and naming one fails as an
unknown connection.

Install this skill by copying its directory to `~/.claude/skills/d9s` (for
every project) or `.claude/skills/d9s` (for one project).

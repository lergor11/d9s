# Change: Broaden engine connectivity options

## Why
Each adapter supports exactly one deployment shape: ClickHouse only over the
native protocol, Redis only as a single node, Postgres only over TCP. Common
real-world setups — ClickHouse behind an HTTP load balancer, Redis Cluster or
Sentinel, Postgres over a unix socket — cannot be reached.

## What Changes
- ClickHouse: `protocol: native | http` (default native), so port 8123 works
- Redis: `mode: standalone | cluster | sentinel`, with `master_name` and
  additional addresses for the latter two; the database list adapts (cluster
  exposes only index 0)
- Postgres: a unix socket path accepted in `host`
- Connection-level `connect_timeout`, replacing the hardcoded values

## Impact
- Affected specs: connection-config (new fields), engine-adapters (per-engine
  connectivity)
- Affected code: `internal/config`, `internal/db/{postgres,clickhouse,redis}.go`

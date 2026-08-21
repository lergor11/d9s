# Tasks — add-engine-connectivity

## 1. Config
- [x] 1.1 Fields: `protocol` (clickhouse), `mode`/`master_name`/`addresses` (redis), `connect_timeout`
- [x] 1.2 Validation per engine, including sentinel requiring a master name
- [x] 1.3 Unit tests for parsing and rejections

## 2. Adapters
- [x] 2.1 ClickHouse HTTP transport, tunnel-compatible
- [x] 2.2 Redis cluster and sentinel clients; database list adapts
- [x] 2.3 Postgres unix socket host handling
- [x] 2.4 Apply connect_timeout uniformly

## 3. Verification
- [x] 3.1 Integration tests: clickhouse over 8123, redis cluster via a container
- [x] 3.2 `make lint test` green

# Tasks — add-schema-browser

## 1. Implementation
- [x] 1.1 Extend Driver: ListTables(ctx) ([]Table, error), ListColumns(ctx, table) ([]Column, error); Table{Name, Detail}, Column{Name, Type, Nullable, Detail}
- [x] 1.2 Postgres: information_schema.tables/columns (+ pg_class reltuples estimate)
- [x] 1.3 ClickHouse: system.tables/system.columns
- [x] 1.4 Redis: ListTables = SCAN by pattern buckets (prefix up to first ':'), ListColumns = keys within a prefix with TYPE and TTL
- [x] 1.5 UI: from the query view or database list, `s` opens the schema panel (tables → Enter → columns; `i` inserts SELECT ... LIMIT 100 for the highlighted table)
- [x] 1.6 Tests for catalog queries (unit-level formatting) + build/vet green

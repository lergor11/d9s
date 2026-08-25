# Tasks — add-grid-navigation

## 1. Result model
- [x] 1.1 Carry column type names in `db.Result`; populate in all three adapters
      — clickhouse and postgres report the engine's type names (postgres via
        the pgx type map: int4, text, timestamptz); redis has no column types
        to report, which the spec allows ("where the driver provides one"),
        so its headers stay bare
- [x] 1.2 Detect numeric columns for sorting

## 2. Grid
- [x] 2.1 Sorting with direction indicator, stable for equal keys
- [x] 2.2 Filtering across all columns or the selected one, with counts in the status line
- [x] 2.3 Cell inspector with JSON pretty-printing and copy

## 3. Verification
- [x] 3.1 Table-driven tests for sort comparators, filter matching, and JSON detection
      — `internal/ui/grid_test.go`; the live postgres paging test also asserts
        the reported column types
- [x] 3.2 `make lint test` green

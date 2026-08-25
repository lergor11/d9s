# Tasks — add-grid-navigation

## 1. Result model
- [ ] 1.1 Carry column type names in `db.Result`; populate in all three adapters
      — the `ColumnTypes` field exists and clickhouse fills it; postgres and
        redis still leave it empty, so headers show a type only on clickhouse
- [x] 1.2 Detect numeric columns for sorting

## 2. Grid
- [x] 2.1 Sorting with direction indicator, stable for equal keys
- [x] 2.2 Filtering across all columns or the selected one, with counts in the status line
- [x] 2.3 Cell inspector with JSON pretty-printing and copy

## 3. Verification
- [ ] 3.1 Table-driven tests for sort comparators, filter matching, and JSON detection
      — no grid tests exist yet; only the statement-selection keys are covered,
        in export_test.go
- [x] 3.2 `make lint test` green

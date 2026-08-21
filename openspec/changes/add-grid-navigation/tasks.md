# Tasks — add-grid-navigation

## 1. Result model
- [ ] 1.1 Carry column type names in `db.Result`; populate in all three adapters
- [ ] 1.2 Detect numeric columns for sorting

## 2. Grid
- [ ] 2.1 Sorting with direction indicator, stable for equal keys
- [ ] 2.2 Filtering across all columns or the selected one, with counts in the status line
- [ ] 2.3 Cell inspector with JSON pretty-printing and copy

## 3. Verification
- [ ] 3.1 Table-driven tests for sort comparators, filter matching, and JSON detection
- [ ] 3.2 `make lint test` green

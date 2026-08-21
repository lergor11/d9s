# Change: Sort, filter, and inspect cells in the result grid

## Why
Results are a static table: you cannot sort by a column, narrow to matching
rows, or read a value longer than the column width without exporting.

## What Changes
- Sort the loaded rows by the selected column, ascending and descending
- Filter rows by a substring across all columns, or one column when a column
  is selected
- Open the selected cell full-screen to read long text or JSON, with a key to
  copy just that value
- Column headers show the engine's data type alongside the name

## Impact
- Affected specs: result-grid (new)
- Affected code: `internal/ui/query.go`, `internal/db` (carry column types in
  `Result`)

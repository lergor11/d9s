# Change: Page large result sets instead of buffering them

## Why
`Execute` reads every row into `db.Result.Rows` before the UI sees anything,
so `SELECT * FROM big_table` buffers the whole table in memory and can wedge
the process. A database client has to survive a careless query.

## What Changes
- Results stream in pages; the driver keeps the cursor open and fetches the
  next page as the user scrolls
- A configurable row cap (default 10 000) after which fetching stops with a
  visible "truncated" marker and a key to keep loading
- A memory-bounded cell store: oversized values are held truncated with the
  full value fetched on demand when a cell is opened
- Export still writes the full result by re-reading from the cursor

## Impact
- Affected specs: query-execution (paged results), engine-adapters (cursor
  lifetime), result-export (full re-read)
- Affected code: `internal/db/*`, `internal/ui/query.go`, `internal/export`

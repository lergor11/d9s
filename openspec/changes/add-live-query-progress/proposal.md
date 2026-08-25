# Change: Live query progress and ClickHouse profile events

## Why
A long query in d9s shows a spinner and nothing else, so there is no way to
tell a query that is scanning billions of rows from one that is stuck. The
native `clickhouse-client` shows rows read, bytes, elapsed time and memory as
the query runs, and that display is what tells you whether to wait or cancel.

The driver already carries the data: `clickhouse-go` exposes `WithProgress`,
`WithProfileInfo`, `WithProfileEvents` and `WithLogs` on the query context, so
this is wiring, not invention.

## What Changes
- ClickHouse queries stream progress while they run: rows and bytes read,
  elapsed time, memory in use, and a percentage when the engine reports a
  total
- Profile events (`SelectedRows`, `SelectedBytes`, `NetworkSendBytes`, and the
  rest) are collected and shown after the query, on demand rather than in the
  way
- Server-side log lines arrive with the query and can be shown alongside it,
  which is where ClickHouse explains what it decided to do
- PostgreSQL has no equivalent stream, so it keeps the elapsed-time counter it
  has, and the display degrades to that rather than pretending
- Cancelling remains one key, and the progress display makes clear what was
  read before the cancel

## Impact
- Affected specs: live-query-progress (new), tui-shell (the running state
  shows progress rather than a bare spinner)
- Affected code: `internal/db/clickhouse.go`, `internal/db/driver.go` (a
  progress channel on the execute path), `internal/ui/query.go`

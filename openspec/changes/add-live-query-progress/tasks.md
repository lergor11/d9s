# Tasks — add-live-query-progress

## 1. Driver
- [x] 1.1 Extend the execute path with an optional progress sink, kept off `Driver` the way `Streamer` is, so engines without one are unaffected
      — `db.ProgressStreamer` + `db.StreamProgress`, which falls back to plain
        `Stream` and leaves the sink silent
- [x] 1.2 ClickHouse: `clickhouse.WithProgress`, `WithProfileInfo`, `WithProfileEvents`, `WithLogs` on the query context
      — a `progressTracker` sums the per-packet deltas and per-thread event
        increments; `send_logs_level=information` asks for the log lines
- [x] 1.3 Note the transport limit: these arrive over the native protocol; say what the HTTP transport does or does not deliver
      — documented on `ExecuteStreamProgress` and in the README: over HTTP the
        query runs normally and the sink hears nothing
- [x] 1.4 Unit tests with a fake sink for accumulation and cancellation
      — `internal/db/progress_test.go`

## 2. UI
- [x] 2.1 Running state shows rows, bytes, elapsed, memory, and a percentage when a total is known
      — in the status line, refreshed by the spinner ticks; engines without a
        stream show elapsed time alone
- [x] 2.2 Counters survive a cancel and are stated in the final line
      — the statement's final line adds "read N rows (X) before it stopped"
- [x] 2.3 Profile events and server logs on demand for the finished statement
      — `p` with results focused opens the profile panel (reusing the
        inspector overlay); log lines render under the statement's result
- [x] 2.4 Footer hints, `?` overlay, README

## 3. Verification
- [x] 3.1 Integration test against a container: a `numbers()` scan reports climbing progress
      — `TestClickHouseProgressLive`: monotonic rows, a total, profile events
- [x] 3.2 `make lint test` green

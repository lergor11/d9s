# Tasks — add-live-query-progress

## 1. Driver
- [ ] 1.1 Extend the execute path with an optional progress sink, kept off `Driver` the way `Streamer` is, so engines without one are unaffected
- [ ] 1.2 ClickHouse: `clickhouse.WithProgress`, `WithProfileInfo`, `WithProfileEvents`, `WithLogs` on the query context
- [ ] 1.3 Note the transport limit: these arrive over the native protocol; say what the HTTP transport does or does not deliver
- [ ] 1.4 Unit tests with a fake sink for accumulation and cancellation

## 2. UI
- [ ] 2.1 Running state shows rows, bytes, elapsed, memory, and a percentage when a total is known
- [ ] 2.2 Counters survive a cancel and are stated in the final line
- [ ] 2.3 Profile events and server logs on demand for the finished statement
- [ ] 2.4 Footer hints, `?` overlay, README

## 3. Verification
- [ ] 3.1 Integration test against a container: a `numbers()` scan reports climbing progress
- [ ] 3.2 `make lint test` green

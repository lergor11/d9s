# Tasks — add-result-paging

## 1. Driver contract
- [x] 1.1 Add a streaming execute returning a cursor handle (NextPage, Close) alongside the existing one-shot Execute
      — `Cursor`/`Streamer` plus a `Stream` helper, kept off `Driver` so the
        interface, the command line and the MCP server compile untouched
- [x] 1.2 Postgres, ClickHouse, and Redis implementations; cursor closed on context cancellation
- [x] 1.3 Row cap in config (`result_cap`, default 10000)

## 2. UI
- [ ] 2.1 Results area fetches the next page on scroll; truncation marker and a key to continue
      — THE GAP THAT MATTERS: nothing calls `db.Stream` yet, so a `SELECT *`
        still buffers the whole table in memory
- [ ] 2.2 Status line reports loaded vs total-unknown state

## 3. Export
- [ ] 3.1 Export re-reads the full result from a fresh cursor rather than the loaded page

## 4. Verification
- [x] 4.1 Integration test: a 100k-row postgres table renders quickly and stays under a memory bound
      — driver level only; the UI cannot be measured until 2.1 lands
- [x] 4.2 `make lint test` green

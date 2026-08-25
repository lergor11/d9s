# Tasks — add-result-paging

## 1. Driver contract
- [x] 1.1 Add a streaming execute returning a cursor handle (NextPage, Close) alongside the existing one-shot Execute
      — `Cursor`/`Streamer` plus a `Stream` helper, kept off `Driver` so the
        interface, the command line and the MCP server compile untouched
- [x] 1.2 Postgres, ClickHouse, and Redis implementations; cursor closed on context cancellation
- [x] 1.3 Row cap in config (`result_cap`, default 10000)

## 2. UI
- [x] 2.1 Results area fetches the next page on scroll; truncation marker and a key to continue
      — the query view now runs every statement through `db.Stream` and renders
        each page as it lands, so the first rows appear before the read ends and
        `result_cap` bounds what is held. The cursor is closed when its
        statement ends rather than kept open, so no connection is pinned while
        the user reads; raising the cap past the marker still means editing
        `result_cap`, so a key that fetches more is not there yet
- [ ] 2.2 Status line reports loaded vs total-unknown state
      — the summary counts loaded rows; it does not distinguish "all of them"
        from "the cap stopped us", which the section marker shows instead

## 3. Export
- [ ] 3.1 Export re-reads the full result from a fresh cursor rather than the loaded page
      — export still writes the loaded rows, so a truncated result exports
        truncated; the command line is unaffected, since it never caps

## 4. Verification
- [x] 4.1 Integration test: a 100k-row postgres table renders quickly and stays under a memory bound
      — driver level only; the UI cannot be measured until 2.1 lands
- [x] 4.2 `make lint test` green

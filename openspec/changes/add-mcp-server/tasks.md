# Tasks — add-mcp-server

## 1. Server
- [x] 1.1 `internal/mcp`: stdio server using the official Go MCP SDK (check current API via context7 before coding)
- [x] 1.2 Tools: list_connections, list_databases, list_tables, describe_table, query — with argument schemas
- [x] 1.3 Lazy session pool keyed by connection, closed on shutdown; diagnostics to stderr only

## 2. Safety
- [x] 2.1 Destructive refusal requiring both `--allow-write` and per-connection `allow_write`
- [x] 2.2 Row cap (200) and byte cap (100 KB) with explicit truncation notices
- [x] 2.3 `--connections` allowlist applied to every tool
- [x] 2.4 Secret redaction audit across responses, errors, and logs

## 3. Skill
- [x] 3.1 `skills/d9s/SKILL.md`: tools, read-only contract, explore-then-query workflow
- [x] 3.2 Installation docs: `claude mcp add d9s -- d9s mcp --connections …` and skill placement

## 4. Verification
- [x] 4.1 Unit tests per tool against a fake driver, including refusal and truncation
- [x] 4.2 An end-to-end test driving the server over stdio with real MCP frames
- [ ] 4.3 README section — text handed to the change owner, who owns README.md
- [x] 4.4 `make lint test` green

## Notes
- SDK pinned at `github.com/modelcontextprotocol/go-sdk v1.7.0`.
- `config.Connection` gained one field, `allow_write`, the per-connection half
  of the write gate. The TUI and the CLI ignore it.
- Verified live against `postgres:16-alpine`: the tool surface, the row and
  byte caps, the refusal (which left all 1000 rows in place), the allowlist,
  and a write succeeding only with both switches open.

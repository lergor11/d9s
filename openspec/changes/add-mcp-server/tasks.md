# Tasks — add-mcp-server

## 1. Server
- [ ] 1.1 `internal/mcp`: stdio server using the official Go MCP SDK (check current API via context7 before coding)
- [ ] 1.2 Tools: list_connections, list_databases, list_tables, describe_table, query — with argument schemas
- [ ] 1.3 Lazy session pool keyed by connection, closed on shutdown; diagnostics to stderr only

## 2. Safety
- [ ] 2.1 Destructive refusal requiring both `--allow-write` and per-connection `allow_write`
- [ ] 2.2 Row cap (200) and byte cap (100 KB) with explicit truncation notices
- [ ] 2.3 `--connections` allowlist applied to every tool
- [ ] 2.4 Secret redaction audit across responses, errors, and logs

## 3. Skill
- [ ] 3.1 `skills/d9s/SKILL.md`: tools, read-only contract, explore-then-query workflow
- [ ] 3.2 Installation docs: `claude mcp add d9s -- d9s mcp --connections …` and skill placement

## 4. Verification
- [ ] 4.1 Unit tests per tool against a fake driver, including refusal and truncation
- [ ] 4.2 An end-to-end test driving the server over stdio with real MCP frames
- [ ] 4.3 README section
- [ ] 4.4 `make lint test` green

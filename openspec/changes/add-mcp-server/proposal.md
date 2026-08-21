# Change: MCP server so agents can query configured databases

## Why
Claude Code and similar agents cannot reach the user's databases without being
handed credentials. d9s already holds the hard parts — 1Password secrets, SSH
bastions, TLS — behind named connections, so exposing them over MCP lets an
agent explore a schema and run queries while the credentials stay in
1Password and never enter the agent's context.

## What Changes
- `d9s mcp` runs a stdio MCP server exposing tools: `list_connections`,
  `list_databases`, `list_tables`, `describe_table`, and `query`
- Read-only by default and enforced server-side: `query` refuses destructive
  statements, and writes require both a `--allow-write` flag at launch and an
  `allow_write: true` on the connection, so a careless prompt cannot drop a
  table
- Every result is row-capped and byte-capped, with truncation stated in the
  response, so a `SELECT *` cannot flood the agent's context
- `--connections` limits the server to named connections, so an agent gets the
  staging database and not production
- A bundled Claude Code skill documents the tools, the read-only contract, and
  the exploration-before-query workflow
- Secrets never appear in tool output, error text, or logs

## Impact
- Affected specs: mcp-server (new)
- Affected code: new `internal/mcp`, `cmd/d9s` (subcommand), `skills/d9s/`
  (skill), README

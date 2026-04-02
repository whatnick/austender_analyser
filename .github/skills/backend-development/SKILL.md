---
name: backend-development
description: 'Use when working on the austender backend in server/, including API handlers, Lambda parity, MCP tools, /api/search, /api/llm, /api/mcp, cache behavior, and server tests.'
argument-hint: 'Describe the endpoint, handler, MCP tool, or backend flow to change.'
---

# Backend Development

Repo-specific workflow for the Go HTTP and Lambda backend in `server/`.

## When to Use
- Editing files in `server/`.
- Changing API request or response structs, routing, validation, or handler behavior.
- Updating MCP tools, LLM orchestration, caching, or Lambda integration.
- Adding backend smoke tests or server build/deploy steps.

## Constraints
- Keep local HTTP and Lambda behavior aligned. Shared validation and payload semantics should stay consistent.
- Reuse collector helpers rather than copying scraping or aggregation logic into the server.
- MCP tool definitions in `server/mcp_server.go` must stay aligned with collector request and response shapes.
- Same-day in-memory caching is additive; do not break the underlying collector-backed cache path.

## Workflow
1. Start by identifying whether the change is HTTP-only, Lambda-only, or shared. Default to shared behavior.
2. For data or aggregation changes, confirm whether the root change belongs in `collector/` first.
3. Update handlers, request structs, and MCP tool contracts together when their interfaces move.
4. Keep failure modes machine-readable for MCP and API consumers.
5. Add or extend tests in `server/*_test.go`, including real temp-cache or temp-lake tests when validating search paths.

## Validation
- Run `cd server && go test ./...` or `task server:test`.
- If the backend change affects shared workflows, run repo-level or smoke-task validation as well.
- For ClickHouse-backed search or cache paths, use the repo smoke scripts when applicable.

## References
- Server overview: `server/README.md`
- Shared smoke entrypoint: `hack/smoke-clickhouse.sh`
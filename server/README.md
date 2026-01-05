# Server

Go HTTP + Lambda entrypoint that fronts the collector search helpers and exposes LLM + MCP surfaces.

## Endpoints
- `POST /api/scrape` – `{ "keyword": "KPMG" }` -> `{ "result": "$X.XX" }` using collector search helpers.
- `POST /api/llm` – `{ "prompt": "How much was spent by Department of Defence?", "mcpConfig": { ... } }`.
  - `mcpConfig` (optional) is forwarded so downstream agents can call tools defined in `mcp_server.go`.
- `GET /api/llm/models` – Lists available LLM models and the active backend (`openai` or `ollama`).
- `POST /api/mcp` – MCP tool surface for agents (see `mcp_server.go`).

## Running locally
- `task run:server` (recommended) or `bash hack/run-server.sh` from repo root. Defaults to `:8080`.
- Env `AUSTENDER_MODE=local` (default) runs the HTTP server; `AUSTENDER_MODE=lambda` uses API Gateway proxy handler.

## Tests
- From this directory: `go test ./...`
- Root `task test:all` covers this module.

## Notes
- `llm_handler.go` handles prompt wiring and optional MCP config attachment.
- `mcp_server.go` keeps typed tools in sync with collector helpers; avoid drift when changing request/response structs.
- Lambda builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` (`task server:build`).
- If `OPENAI_API_KEY` is present the server prefers OpenAI regardless of `OLLAMA_HOST`. Set `OLLAMA_HOST` (plus `OLLAMA_DEFAULT_MODEL` if desired) to surface local models when OpenAI is unavailable. Prefix a requested model with `ollama:` or `openai:` to force a specific backend per-request.

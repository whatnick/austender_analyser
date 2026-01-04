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
- Set `OLLAMA_HOST` (and optional `OLLAMA_DEFAULT_MODEL`) to use a local Ollama instance and expose its installed models; otherwise the handler defaults to OpenAI (`OPENAI_DEFAULT_MODEL` overrides the default `gpt-4o-mini`).

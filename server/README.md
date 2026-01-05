# Server

Go HTTP + Lambda entrypoint that wraps collector helpers, layers a same-day in-memory cache, exposes MCP tools, and runs an LLM-backed agent for spend analysis.

## Endpoints
- `POST /api/scrape` – accepts `ScrapeRequest` (`keyword`, optional `company`, `agency`, `source`, date fields, and `lookbackPeriod`) and returns `{ "result": "$X.XX" }`.
- `POST /api/llm` – accepts `LLMRequest` (`prompt`, optional `model`, `source`, `lookbackPeriod`, `useCache`, `mcpConfig`). Runs an agent that can call collector tools (jurisdiction detection, catalog lookup, contract aggregation). Falls back to single-prompt completion on agent failure.
- `GET /api/llm/models` – exposes active backend (`openai` or `ollama`), default model, and selectable alternatives.
- `POST /api/mcp` – serves typed MCP tools defined in `mcp_server.go` for external agents.

All routes honour CORS preflight (`OPTIONS`). `/api/scrape` and `/api/llm` sit behind the same Parquet-backed cache to avoid redundant scraping.

## Running Locally
- `task run:server` (or `bash ../hack/run-server.sh`) starts the HTTP server on `:8080`.
- `AUSTENDER_MODE=local` (default) starts the HTTP server; `AUSTENDER_MODE=lambda` exposes the API Gateway proxy handler for Lambda.
- `task server:build` cross-compiles `dist/main.zip` for deployment.

## Key Environment Variables
- `OPENAI_API_KEY` – enables OpenAI backend (preferred when set).
- `OLLAMA_HOST` – enables Ollama backend and model picker; set alongside optional `OLLAMA_DEFAULT_MODEL` and `OLLAMA_SYSTEM_PROMPT`.
- `AUSTENDER_CACHE_DIR`, `AUSTENDER_CACHE_TZ` – passed to collector helpers and same-day cache utilities.
- `AUSTENDER_MCP_CONFIG` – default MCP config JSON injected into `/api/llm` requests when present.

## Testing
- Run module tests with coverage: `task server:test`
- Repo-wide coverage (collector + server + infra): `task test:all`

## Implementation Notes
- `api.go` owns HTTP routing, daily in-memory cache, and request validation shared by local and Lambda modes.
- `llm_handler.go` selects backend (`openai` vs `ollama`), executes the tool-using agent loop, and gracefully falls back to vanilla completion.
- `mcp_server.go` stays aligned with collector helpers; update both when request/response structs evolve.
- Lambda builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`; deploy via `task server:fastdeploy` once the SSM parameter `austender` points to the target function name.

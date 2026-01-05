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
- If `OPENAI_API_KEY` is present the server prefers OpenAI regardless of `OLLAMA_HOST`, and automatically falls back to OpenAI listings when the Ollama host is unreachable. Set `OLLAMA_HOST` (plus `OLLAMA_DEFAULT_MODEL` if desired) to surface local models when OpenAI is unavailable. Prefix a requested model with `ollama:` or `openai:` to force a specific backend per-request.
- Repo-wide coverage (collector + server + infra): `task test:all`

## Implementation Notes
- `api.go` owns HTTP routing, daily in-memory cache, and request validation shared by local and Lambda modes.
- `llm_handler.go` selects backend (`openai` vs `ollama`), warms caches ahead of agent runs, and falls back to either vanilla completion or a direct collector aggregation when models time out.
- `mcp_server.go` stays aligned with collector helpers; update both when request/response structs evolve.
- Lambda builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`; deploy via `task server:fastdeploy` once the SSM parameter `austender` points to the target function name.

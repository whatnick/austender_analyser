# Frontend

Static HTMX chat UI that talks to `/api/llm` and demonstrates MCP tool wiring.

## Usage
- Open `index.html` directly or run `task run:frontend` from repo root (assumes API on `http://localhost:8080`).
- Ask plain-text spend questions in the chat box.
- Toggle **Enable MCP backend** to send the MCP config from `config.local.js`.
- The page auto-detects `/api/llm/models` on load (with retries) and shows the Ollama model picker when `OLLAMA_HOST` is configured on the server. The status banner reflects the detected backend/host.
- Responses render markdown + math via markdown-it + markdown-it-mathjax3 (MathJax v3; supports `$...$`, `$$...$$`, `\[...\]`) and are sanitized with DOMPurify. If libraries are not ready yet, messages show plain text and are re-rendered once the renderer initializes. Scripts are deferred and the chat logic waits for DOMContentLoaded. Context is shown when the server could answer from cache.

## Configuration
- `config.local.js` sets `apiBase` and optional `mcpConfig` (default points to `/api/mcp`). Adjust for deployed environments.
- Requests are simple JSON: `{ "prompt": "...", "mcpConfig": { ... } }`.

## Styling/behavior
- Built with Bootstrap + HTMX; no build step required.
- Chat log renders user and assistant messages; context (if returned) is shown in a muted line.

## Testing
- No automated frontend tests; rely on manual checks via browser. Backing endpoints covered by server tests.

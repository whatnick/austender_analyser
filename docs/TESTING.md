# Testing & Coverage Strategy

Target: **≥90% coverage** across Go modules (`collector/`, `server/`, `infra/`). The static frontend is excluded from this target.

## Quick Commands
- Repo aggregate (runs all module tests, merges coverage): `task test:all`
- Merge-only helper when profiles already exist: `task coverage:stack`
- Module-specific coverage runners:
  - Collector: `task collector:test`
  - Server: `task server:test`
  - Infra: `task infra:test`

Each module emits `coverage.out`. Inspect with `go tool cover -html=coverage.out`. After `task test:all`, combined output lives at `coverage/combined.out`.

## Module Focus Areas
- **collector/**: exercise cache/lake flows (`RunSearchWithCache`, `windowsCached`), date window helpers, jurisdiction detection, and CLI flag permutations. Stub network calls via `runSearchFunc` overrides.
- **server/**: cover REST validation, same-day cache behaviour, MCP tool wiring, Lambda vs local entry points, and the LLM agent loop (tool selection, error recovery).
- **infra/**: assert synthesized resources (Lambda, API Gateway, S3, CloudFront) and environment-driven overrides. Keep tests pure `go test` (no AWS calls).

## Optional CPU-only LLM Integration Test
Goal: run `/api/llm` end-to-end without GPU by pairing langchaingo with a tiny GGUF model.

1. Model: cache a compact chat model (e.g., TinyLlama 1.1B Q4) with checksum verification.
2. Backend: use `github.com/tmc/langchaingo/llms/llamacpp` with `llamacpp.WithModelPath(...)` and deterministic sampling settings.
3. Flow (in `server/llm_integration_test.go`): start the handler via `httptest`, POST `{ "prompt": "hello" }`, assert non-empty `result` and HTTP 200.
4. Gate behind `LOCAL_LLM=1` to keep default CI fast; create a dedicated job that installs llamacpp deps and downloads the model when enabled.

## CI Recommendations
- Add coverage enforcement that parses `go tool cover -func coverage/combined.out` and fails when any module drops below 90% coverage.
- Provide an opt-in `llm-integration` job guarded by `LOCAL_LLM=1` for smoke testing local models without blocking standard PR checks.

## Next Steps
- Expand collector table-driven tests for `resolveLookbackYears`, `resolveDates`, and catalog reindex edge cases.
- Add server tests around MCP error surfacing and Ollama/OpenAI backend selection fallbacks.
- Enhance infra assertions for CloudFront behaviour (default root object, caching policies) and IAM least-privilege checks.

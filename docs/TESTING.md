# Testing & Coverage Strategy

Target: **≥90% coverage** across Go modules (`collector/`, `server/`, `infra/`). Frontend is excluded from the target per instructions.

## Quick commands
- Root aggregate (prints per-module summaries): `task coverage:all`
- Module-specific:
  - Collector: `task collector:coverage`
  - Server: `task server:coverage`
  - Infra: `task infra:coverage`

Coverage profiles are written as `coverage.out` in each module; open with `go tool cover -html=coverage.out`.

## Module focus areas
- **collector/**: exercise cache/lake paths, date windows, lookback logic, and CLI flag handling. Add table-driven tests around `resolveDates`, `matchesFilters`, lake partitioning, and cache checkpointing. Avoid network by stubbing `runSearchFunc`.
- **server/**: cover request validation and prefetch branches in `llm_handler.go`, MCP handler glue, and lambda/local modes in `main.go`/`lambda.go`. Use httptest with fake collectors and MCP servers.
- **infra/**: cover CDK stack synthesis and parameterized defaults. Use env overrides to assert bucket/domain names, and keep tests `go test` (no AWS calls).

## CPU-only LLM integration test (proposal)
Goal: end-to-end request through `/api/llm` using a tiny local LLM so CI stays CPU-only.

1. **Model**: use a small GGUF (e.g., TinyLlama-1.1B-chat Q4) cached locally. Store the download behind a CI job step with checksum.
2. **Langchaingo backend**: use `github.com/tmc/langchaingo/llms/llamacpp` with `llamacpp.WithModelPath(...)` and `llamacpp.WithContext(context.Background())`.
3. **Test flow (Go, in `server/llm_integration_test.go`):**
   - Spin up httptest server wrapping `llmHandler`.
   - Stub collector prefetch helpers to return deterministic strings (no network).
   - Start llamacpp client pointing to the local GGUF; set a small `MaxTokens` and deterministic `Temperature`.
   - POST `{"prompt":"hello","prefetch":false}` to `/api/llm` and assert the response JSON has a non-empty `result` and status 200.
   - Skip test (`t.Skip`) unless env `LOCAL_LLM=1` is set to keep default CI fast; add a dedicated CI job that sets `LOCAL_LLM=1` and downloads the model.
4. **CI notes:** ensure the job installs `libc6`/`libgomp` as needed for llamacpp, runs on standard CPU runners, and caches the GGUF between runs.

## CI recommendations
- Add a coverage workflow that runs `task coverage:all`, uploads `coverage.out` artifacts per module, and fails if any module falls below 90% (parse `go tool cover -func`).
- Add a separate optional job `llm-integration` gated by `LOCAL_LLM=1` to exercise the CPU-only LLM path without blocking quick PR runs.

## Next steps
- Flesh out server prefetch/parse tests for `maybePrefetchSpend` and comparison paths.
- Add collector table-driven coverage for `resolveLookbackYears`, `resolveDates`, and `windowsCached` edge cases.
- Expand infra synth assertions for CloudFront/S3 outputs and parameter defaults.

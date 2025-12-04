# Copilot Development Instructions

> Use this guide whenever you prompt GitHub Copilot or any AI assistant on this repository. It summarizes the overall development workflow, coding style, and operational guardrails expected across the mono-repo.

## 1. Repository Intent
- Provide quick, auditable summaries of Australian government contract spend for any keyword (e.g., company name) using scraped Austender data normalized into the Open Contracting Data Standard (OCDS).
- Serve results via a Go CLI (`collector`), a Go HTTP/Lambda API (`server`), a Model Context Protocol (MCP) bridge for agentic workflows, and a minimal HTMX frontend (`frontend`); provision prod infra with Go CDK (`infra`).
- Keep the pipeline auditable end-to-end by publishing OCDS release packages and MCP tool schemas alongside every deployment artifact.

## 2. Architecture Snapshot
- **collector/** – Colly-based scraper + Cobra CLI. Normalize every scrape into OCDS release + record structures and expose reusable helpers (`github.com/whatnick/austender_analyser/collector`) for other modules.
- **server/** – HTTP handlers + AWS Lambda proxy integration (uses `aws-lambda-go`). Controlled by `AUSTENDER_MODE` env: `local` for `:8080` server, `lambda` for API Gateway. Hosts the MCP-compatible tool surface defined in `server/mcp_server.go`.
- **infra/** – AWS CDK (Go) stack building Lambda, API Gateway, S3 (static site), CloudFront, and minimal state buckets to ship OCDS JSON artifacts. Default region/account pulled from `aws sts get-caller-identity` and `ap-southeast-1`.
- **frontend/** – Static HTMX page pointing at `/api/scrape`. `config.local.js` overrides API base when running locally and can render OCDS releases directly in-browser for zero-trust validation.
- **query/** – Experimental analytics and MCP automation entrypoints. Use it for shareable MCP recipes and client-side configuration presets.
- **hack/** – Shell scripts mirroring Taskfile targets for consistent CI/local tooling.

## 3. Data Standards & MCP Integration
- OCDS is the canonical data contract. When introducing new fields, update shared structs and include JSON schema notes so both the MCP surface and HTTP API stay aligned.
- MCP tooling (see `server/mcp_server.go` plus `third_party/mcp-go`) is the preferred way to expose scraping/aggregation steps to downstream agents. Keep typed tools synced with collector helpers to avoid drift.
- When exporting data, include OCDS `releasePackage` metadata (publisher, publishedDate, version) so columnar sinks can reason about deltas.
- MCP tools must fail fast with machine-readable errors; avoid interactive prompts unless explicitly requested via the MCP channel.

## 4. Tooling & Commands
- Go 1.25.x is required for all modules. Run `go fmt` + `go test ./...` within each module touched.
- Prefer Taskfile targets; scripts exist for environments without Task installed.

| Action | Task command | Script/Alt |
| --- | --- | --- |
| Run API locally | `task run:server` | `bash hack/run-server.sh` |
| Open frontend | `task run:frontend` | `bash hack/run-frontend.sh` |
| Run both | `task run:local` | `bash hack/run-local.sh` |
| All tests | `task test:all` | `bash hack/test-all.sh` |
| Collector CLI | `task collector:run` (defaults to `--keyword Accenture`) | `cd collector && go run . --keyword KPMG` |
| Module tests | `task collector:test`, `task server:test`, `task infra:test` | `go test ./...` inside module |
| Infra synth/deploy | `task infra:synth`, `task infra:deploy` | `cd infra && cdk synth|deploy` |

## 5. Development Workflow Expectations
1. **Design in collector first** – Any scraping, parsing, or data munging lives in the collector module so the server can import it directly.
2. **Keep API + Lambda parity** – When updating request/response structs in `server/`, ensure both the HTTP handler and `HandleLambdaRequest` share the same validation, error handling, and payload shapes.
3. **Stateless frontend** – The HTMX frontend should remain static; configuration goes through `config.local.js` or the CDN-deployed bundle.
4. **Testing** – Extend/author Go unit tests alongside code. Avoid live Austender calls in tests; prefer fixtures/mocks to keep CI deterministic.
5. **Formatting & linting** – Rely on `go fmt` and idiomatic Go. If Copilot offers unclear code, add succinct comments explaining complex logic.
6. **Dependency hygiene** – Run `go mod tidy` in the affected module when dependencies change. Avoid cross-module imports except via the published module paths.

## 6. Infrastructure & Deployment Notes
- Lambda builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`. `server/Taskfile.yml` handles packaging into `dist/main.zip`.
- `task server:fastdeploy` expects an SSM parameter named `austender` holding the Lambda function name. Ensure AWS credentials/region (`AWS_DEFAULT_REGION`, `CDK_DEFAULT_REGION`) point to the intended account when available; fallback to unauthenticated local mode otherwise.
- `infra/` commands (`task infra:synth|deploy|destroy`) require AWS CDK v2 installed (`cdk` CLI) and bootstrap completed for the target account.
- For minimal-footprint deployments, prefer running the collector via short-lived ECS/Fargate tasks or GitHub Actions, store OCDS bundles in S3, and serve aggregates through CloudFront + `frontend/` so server resources stay near-zero while clients do light-weight filtering.
- CloudFront/S3 updates for the frontend currently mirror API deploys; plan invalidations if static assets change.
- For client-hosted aggregator scenarios, document the required browser storage quota and `config.local.js` overrides so teams can run HTMX + MCP tooling directly without backend changes.

## 7. Contribution & Review Guidance
- Small, focused changes per PR. Update docs (`README.md`, `docs/README.md`, this file) when workflows or commands shift.
- Mention test coverage in PR descriptions; CI runs `hack/test-all.sh`.
- When Copilot generates significant code, double-check for:
  - Proper error propagation/logging (no silent failures).
  - Context timeouts or rate limiting if new network calls are added.
  - Reuse of shared structs/types to prevent drift between modules.
- Coordinate schema or API shape changes with both frontend and infra stakeholders before merging.

## 8. Roadmap & Performance Priorities
- **Columnar acceleration** – Evaluate Duck Lake, DuckDB/MotherDuck, ClickHouse, Apache Arrow Flight SQL, and BigQuery Omni as downstream sinks fed by OCDS releases. Favor engines that support fast group-by on contract value and can be embedded in Lambda or WASM for client-side pivots.
- **Incremental snapshots** – Emit parquet/arrow snapshots alongside JSON to avoid reprocessing entire OCDS archives. Include `releases/updates` partitioning keyed by financial year + agency.
- **Client-forward MCP agents** – Ship lightweight MCP manifests that let end users host the aggregator locally (e.g., in VS Code or browsers with WASM collectors). The central API should only broker credentials and cache scraped HTML to keep server costs negligible.
- **Scalable deployment method** – Default deployment path: collector runs in CI (GitHub Actions or lightweight container runner), pushes OCDS+columnar artifacts to S3, and CDN + MCP endpoints serve static + tool metadata. Consumers opt into richer analytics by pointing Duck Lake/ClickHouse instances at the published parquet/arrow manifests. Document required IAM least-privilege roles for this pattern.
- **Performance guardrails** – Whenever a new destination store is added, provide benchmarks (rows/s, compression ratio, cold-start latency) and update infra automation to toggle stores via Taskfile vars.

Keeping these instructions close ensures Copilot (and humans) produce consistent, deployable changes across the OCDS + MCP-first Austender Analyser stack.
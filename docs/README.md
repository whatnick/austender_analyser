# Austender Analyser Onboarding

Welcome to the Austender Analyser mono-repo. This guide summarises how the pieces fit together and the quickest paths to productivity.

## What the Project Delivers
- Answers “how much has the government spent with <keyword>?” by scraping [Austender](https://www.tenders.gov.au) and aligned state portals (NSW, VIC, QLD, SA, WA).
- Provides a Go CLI (`collector`) for scraping and cache priming, a Go HTTP/Lambda server (`server`) for REST + MCP + LLM access, and Go CDK infrastructure (`infra`) to deploy the stack.
- Ships a minimal HTMX frontend (`frontend`) that exercises `/api/llm`, auto-detects LLM backends, and can attach MCP configurations.
- Maintains a Parquet lake + ClickHouse-friendly JSON index under `~/.cache/austender`, partitioned by FY/month/agency/company. Runs skip month partitions already present and keep keyword/company/agency filters optional for broad warming.
- Layers a same-day in-memory cache inside the server on top of the Parquet lake, so repeated questions avoid redundant scrapes.

## Architecture Snapshot
- **collector/** – Colly-based scraper and Cobra CLI. Exposes helpers consumed by the server (`github.com/whatnick/austender_analyser/collector`). Dedicated adapters for each jurisdiction via `--source` (defaults to federal).
- **server/** – HTTP handlers, AWS Lambda proxy, LLM agent loop, and MCP bridge. `AUSTENDER_MODE=local` runs :8080; `AUSTENDER_MODE=lambda` exposes the API Gateway handler. All `/api/scrape` calls use the Parquet-backed cache by default.
- **infra/** – Go CDK stack that builds the Lambda, API Gateway, S3 bucket for static assets, and CloudFront distribution with Origin Access Control.
- **frontend/** – Static HTML/HTMX UI with Bootstrap styling, Ollama model picker, and MCP toggle powered by `config.local.js`.
- **hack/** – Shell scripts mirroring Task targets for environments that cannot install Task.

## Daily Driver Commands
Prereqs: Go 1.25+, Task (<https://taskfile.dev/#/installation>) or Bash + GNU tools. AWS CLI + CDK are only needed for infra work.

| Action | Taskfile | Shell Script |
| --- | --- | --- |
| Run API locally | `task run:server` | `bash hack/run-server.sh` |
| Open frontend | `task run:frontend` | `bash hack/run-frontend.sh` |
| Run both helpers | `task run:local` | `bash hack/run-local.sh` |
| Run tests + merged coverage | `task test:all` | `bash hack/test-all.sh` |
| Collector CLI | `task collector:run -- --k KPMG` | `cd collector && go run . --k KPMG` |
| Prime cache/lake | `task collector:prime-lake -- --lookback-period 5` | `bash hack/prime-datalake.sh --lookback-period 5` |
| Build collector | `task collector:build` | `bash hack/build-collector.sh` |
| Server tests | `task server:test` | `bash hack/test-server.sh` |
| Infra synth/deploy | `task infra:synth` / `task infra:deploy` | `cd infra && cdk synth|deploy` |

Add `--source federal|nsw|vic|qld|sa|wa` to collector commands to target a specific jurisdiction. Use `task collector:prime-lake-all -- --lookback-period 5` when you need to warm every jurisdiction into the ClickHouse-backed lake. `--c` filters by company, `--d` by agency, and `--k` by keyword. Date filters use `--start-date`, `--end-date`, and `--date-type` (defaults to `contractPublished`).

## Development Workflow
1. Shape scraping logic in `collector/` first so both CLI and server reuse it.
2. Mirror request validation between `server/api.go` (HTTP) and the Lambda entry to keep parity.
3. Keep `frontend/index.html` static; tweak behaviour via HTMX/JS and adjust hosts in `frontend/config.local.js`.
4. Run `task test:all` (or module-specific tests) before pushing; CI calls the same script and merges coverage into `coverage/combined.out`.
5. When adjusting infra, update `infra/infra.go`, validate with `task infra:synth`, then deploy via `task infra:deploy` after confirming AWS credentials/region.

## Deployment Notes
- `task server:build` cross-compiles the Lambda binary (`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`) and writes `dist/main.zip`.
- `task server:fastdeploy` expects an SSM parameter named `austender` containing the Lambda function name before calling `aws lambda update-function-code`.
- Infra defaults to region `ap-southeast-1`. Override `AWS_DEFAULT_REGION`/`CDK_DEFAULT_REGION` as needed; `CDK_DEFAULT_ACCOUNT` is auto-detected when AWS CLI credentials exist.

## Contribution Tips
- Share logic through `collector/` to avoid divergence between CLI and server code paths.
- Favour deterministic tests (stub HTTP, avoid hitting live portals). Use table-driven cases for date window, cache, and catalog helpers.
- Update relevant Taskfiles and docs whenever new commands or workflows ship.
- Run `go fmt` and module tests before opening PRs; CI runs `hack/test-all.sh` for validation.

With this primer you should be ready to improve the scraper, extend the API/agent, or evolve the infrastructure.
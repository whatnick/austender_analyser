# Austender Analyser Onboarding

Welcome to the Austender Analyser project. This document captures the essentials you need to get productive quickly and explains how the main pieces in this mono-repo fit together.

## What This Repo Does

- Answers “how much has the government spent with <keyword>?” by scraping [Austender](https://www.tenders.gov.au) and summarising the totals.
- Provides a Go CLI (`collector`) for ad-hoc scraping, a Go HTTP/Lambda server (`server`) for API access, and AWS CDK IaC (`infra`) to deploy everything serverlessly.
- Ships a minimal HTMX frontend (`frontend`) that hits `POST /api/llm` for quick demos, with an MCP toggle and cache prefetch control.
- Maintains a local Parquet lake + SQLite catalog under `~/.cache/austender`, partitioned by FY/month/agency/company, with skip logic to avoid re-downloading months already present.

## Architecture at a Glance

- **Collector (`collector/`)** – Colly-based scraper + Cobra CLI. Exposes the scraping logic that the server imports via `github.com/whatnick/austender_analyser/collector`. Writes all valued releases into the Parquet lake and skips already-populated month partitions.
- **Server (`server/`)** – HTTP handlers and `aws-lambda-go` proxy entry point. `AUSTENDER_MODE=local` runs an HTTP server on `:8080`; `AUSTENDER_MODE=lambda` serves API Gateway. Defaults to `RunSearchWithCache` so API/MCP calls leverage the lake.
- **Infra (`infra/`)** – Go CDK stack that builds Lambda, API Gateway, S3 (static frontend), and CloudFront distribution. Uses `cdk.json` for context, default region `ap-southeast-1`.
- **Frontend (`frontend/`)** – Static HTML/HTMX page plus `config.local.js` to point to `http://localhost:8080`; supports MCP toggle and cache prefetch flag to `/api/llm`.
- **Hack scripts (`hack/`)** – Convenience shell scripts mirroring Task targets for local dev and CI (`hack/run-local.sh`, `hack/test-all.sh`, etc.).

## Daily Driver Commands

Prereqs: Go 1.23+, Taskfile (<https://taskfile.dev/#/installation>) or Bash + GNU tools, AWS CLI + CDK for infra work.

| Action | Taskfile | Shell Script |
| --- | --- | --- |
| Run API locally | `task run:server` | `bash hack/run-server.sh` |
| Open frontend | `task run:frontend` | `bash hack/run-frontend.sh` |
| Run both | `task run:local` | `bash hack/run-local.sh` |
| Run all tests | `task test:all` | `bash hack/test-all.sh` |
| Collector CLI | `task collector:run -- --keyword KPMG` | `cd collector && go run . --keyword KPMG` |
| Server tests | `task server:test` | `bash hack/test-server.sh` |
| Infra synth/deploy | `task infra:synth` / `task infra:deploy` | `cd infra && cdk synth|deploy` |

## Development Workflow

1. **Scraping logic**: build/modify functionality in `collector/` first. Keep exported helpers clean so the server can reuse them without duplication.
2. **API layer**: wire new inputs/outputs in `server/api.go` and ensure both the HTTP handler and `HandleLambdaRequest` stay in sync.
3. **Frontend tweaks**: update `frontend/index.html` or swap to a richer UI. Point to alternate hosts via `frontend/config.local.js`.
4. **Testing**: rely on Go unit tests (`go test ./...`) in each module. Use `task test:all` before pushing; CI mirrors that script.
5. **Infra changes**: modify `infra/infra.go`, run `task infra:synth` to validate, then `task infra:deploy --require-approval never` (already encoded) when ready.

## Deployment Notes

- `server/Taskfile.yml` bundles a `build` target that cross-compiles for Lambda (`GOOS=linux`, `GOARCH=amd64`) and zips to `dist/main.zip`.
- `task server:fastdeploy` queries SSM parameter `austender` for the Lambda function name and calls `aws lambda update-function-code`—ensure the parameter exists in the target account.
- Infra stack expects default AWS account/region from `aws sts get-caller-identity` and `ap-southeast-1`. Export/override `AWS_DEFAULT_REGION` and `CDK_DEFAULT_REGION` if needed.

## Copilot & Contribution Tips

- Keep code modular: any shared logic between CLI and server should live in the collector module so the server imports it rather than duplicating.
- Preserve deterministic tests by stubbing HTTP responses where possible; avoid hitting Austender in unit tests to keep CI reliable.
- Document new commands in the relevant `Taskfile.yml` and update this onboarding doc when workflows change.
- Run `go fmt` + `go test ./...` in the touched module(s) before submitting PRs; CI enforces `hack/test-all.sh`.

With these basics in place, you should be productive quickly whether you are improving the scraper, enhancing the API, or working on infra.
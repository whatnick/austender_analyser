# Copilot Development Instructions

> Use this guide whenever you prompt GitHub Copilot or any AI assistant on this repository. It summarizes the overall development workflow, coding style, and operational guardrails expected across the mono-repo.

## 1. Repository Intent
- Provide quick, auditable summaries of Australian government contract spend for any keyword (e.g., company name) using scraped Austender data.
- Serve results via a Go CLI (`collector`), a Go HTTP/Lambda API (`server`), and a minimal HTMX frontend (`frontend`); provision prod infra with Go CDK (`infra`).

## 2. Architecture Snapshot
- **collector/** – Colly-based scraper + Cobra CLI. Expose reusable scraping helpers here so other modules import `github.com/whatnick/austender_analyser/collector` instead of duplicating logic.
- **server/** – HTTP handlers + AWS Lambda proxy integration (uses `aws-lambda-go`). Controlled by `AUSTENDER_MODE` env: `local` for `:8080` server, `lambda` for API Gateway.
- **infra/** – AWS CDK (Go) stack building Lambda, API Gateway, S3 (static site), and CloudFront. Default region/account pulled from `aws sts get-caller-identity` and `ap-southeast-1`.
- **frontend/** – Static HTMX page pointing at `/api/scrape`. `config.local.js` overrides API base when running locally.
- **hack/** – Shell scripts mirroring Taskfile targets for consistent CI/local tooling.

## 3. Tooling & Commands
- Go 1.23.x is required for all modules. Run `go fmt` + `go test ./...` within each module touched.
- Prefer Taskfile targets; scripts exist for environments without Task installed.

| Action | Task command | Script/Alt |
| --- | --- | --- |
| Run API locally | `task run:server` | `bash hack/run-server.sh` |
| Open frontend | `task run:frontend` | `bash hack/run-frontend.sh` |
| Run both | `task run:local` | `bash hack/run-local.sh` |
| All tests | `task test:all` | `bash hack/test-all.sh` |
| Collector CLI | `task collector:run -- --keyword KPMG` | `cd collector && go run . --keyword KPMG` |
| Module tests | `task collector:test`, `task server:test`, `task infra:test` | `go test ./...` inside module |
| Infra synth/deploy | `task infra:synth`, `task infra:deploy` | `cd infra && cdk synth|deploy` |

## 4. Development Workflow Expectations
1. **Design in collector first** – Any scraping, parsing, or data munging lives in the collector module so the server can import it directly.
2. **Keep API + Lambda parity** – When updating request/response structs in `server/`, ensure both the HTTP handler and `HandleLambdaRequest` share the same validation, error handling, and payload shapes.
3. **Stateless frontend** – The HTMX frontend should remain static; configuration goes through `config.local.js` or the CDN-deployed bundle.
4. **Testing** – Extend/author Go unit tests alongside code. Avoid live Austender calls in tests; prefer fixtures/mocks to keep CI deterministic.
5. **Formatting & linting** – Rely on `go fmt` and idiomatic Go. If Copilot offers unclear code, add succinct comments explaining complex logic.
6. **Dependency hygiene** – Run `go mod tidy` in the affected module when dependencies change. Avoid cross-module imports except via the published module paths.

## 5. Infrastructure & Deployment Notes
- Lambda builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`. `server/Taskfile.yml` handles packaging into `dist/main.zip`.
- `task server:fastdeploy` expects an SSM parameter named `austender` holding the Lambda function name. Ensure AWS credentials/region (`AWS_DEFAULT_REGION`, `CDK_DEFAULT_REGION`) point to the intended account.
- `infra/` commands (`task infra:synth|deploy|destroy`) require AWS CDK v2 installed (`cdk` CLI) and bootstrap completed for the target account.
- CloudFront/S3 updates for the frontend currently mirror API deploys; plan invalidations if static assets change.

## 6. Contribution & Review Guidance
- Small, focused changes per PR. Update docs (`README.md`, `docs/README.md`, this file) when workflows or commands shift.
- Mention test coverage in PR descriptions; CI runs `hack/test-all.sh`.
- When Copilot generates significant code, double-check for:
  - Proper error propagation/logging (no silent failures).
  - Context timeouts or rate limiting if new network calls are added.
  - Reuse of shared structs/types to prevent drift between modules.
- Coordinate schema or API shape changes with both frontend and infra stakeholders before merging.

Keeping these instructions close ensures Copilot (and humans) produce consistent, deployable changes across the Austender Analyser stack.
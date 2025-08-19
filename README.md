# Austender Summarizer
There is always news around how much the government spends on contracting. However it is hard
to find a quick summary of figures in a transparent manner without digging through Austender
website, so public service report or relying on journalists' scraping skills.

Set this up as a serverless go development project to answer spend questions by organization name e.g. KPMG had 5136 tenders awarded, what was the total value ?

![KPMG Tenders](docs/KPMG_result_2023_01_22.png)

## Features
- CLI tool that scrapes for a keyword and returns roll-ups
- Cobra params parsing
- Standard 3-tier design (BE,FE,DB)
- Backend implementation in Golang
- Minimal HTMX frontend included; richer UI (Angular/React) TBD
- Fuzzy name matching in Google "did you mean" style
- Temporal aggregate spend on raw AUD values, not inflation adjusted (I am not an economist)
- Serverless-ready infra in AWS (CDK stack for Lambda + API Gateway + S3 + CloudFront)
- DynamoDB cache of Austender data downloadable as CSV


## Roadmap and Status

- [x] Download one search result (e.g., KPMG) and verify totals (see docs image)
- [x] Basic web server API to serve results for a keyword: POST `/api/scrape` -> `{ result: "$X.XX" }`
- [x] Dual-mode server: local HTTP and AWS Lambda (API Gateway proxy) sharing the same scrape logic
- [x] Initial tests: server unit tests and infra assertions; helper scripts in `hack/`
- [x] Go multi-module layout (collector, server, infra) with direct reuse of collector in server
- [x] CI workflow to run tests across all components via `hack/test-all.sh`
- [ ] Identify fields to cache from Austender and design persistence schema
- [ ] Scheduled ingestion (cron) to populate DynamoDB/ClickHouse with Austender data
- [ ] API enhancements for time ranges (all-time, last 5 years, last year) and filters (agency/company)
- [ ] Fuzzy name matching ("did you mean")
- [ ] Frontend build/deploy pipeline (upload to S3, CloudFront invalidation) and richer UI
- [ ] Performance, rate limiting, CORS, and basic auth hardening

Reference: https://github.com/golang-standards/project-layout

## Folder Structure

The repository is organized as follows:

- `collector/` - Go CLI tool for scraping Austender data. Contains main logic and command utilities.
    - `cmd/` - Cobra command implementations and scraping utilities.
    - `main.go` - Entry point for the CLI tool.
    - `go.mod`, `go.sum` - Go module files.
    - `README.md` - Collector-specific documentation.
- `server/` - Backend server implementation in Go (local HTTP + AWS Lambda handler) that reuses the collector logic directly.
    - `api.go`, `main.go`, `*_test.go` - API, entry point, and tests.
    - `go.mod`, `go.sum` - Go module files for server.
- `frontend/` - Minimal HTMX page posting to `/api/scrape` to display totals.
- `docs/` - Documentation and result images.
    - `KPMG_contracts_flood.png`, `KPMG_result_2023_01_22.png` - Example result images.
    - `README.md` - Documentation for the project.
- `infra/` - Infrastructure as code for deployment (e.g., AWS CDK).
    - `infra.go`, `infra_test.go` - Go code for infrastructure.
    - `cdk.json`, `cdk.out/` - CDK configuration and output files.
    - `go.mod`, `go.sum` - Go module files for infra.
    - `README.md` - Infra-specific documentation.
- `query/` - Reserved for query logic and documentation.
    - `README.md` - Query documentation.
- `hack/` - Helper scripts, including `test-all.sh` to run tests across all modules.
- `.github/workflows/` - CI pipeline that runs `hack/test-all.sh` on pushes and PRs.

Other files:
- `LICENSE.md` - License information.
- `Taskfile.yml` - Project-wide task automation.

## How to run locally

Prereqs: Go 1.23+, a browser. Optional: Task (https://taskfile.dev/#/installation).

- With Task (recommended):
    - Start API server: `task run:server`
    - Open frontend: `task run:frontend`
    - Start both: `task run:local`
    - Run all tests: `task test:all`

- With plain scripts:
    - Start API server (localhost:8080): `bash hack/run-server.sh`
    - Open the minimal frontend: `bash hack/run-frontend.sh`
    - Start both: `bash hack/run-local.sh`

API quick test (without frontend):
- POST to `http://localhost:8080/api/scrape` with JSON body `{"keyword":"KPMG"}`; response is `{ "result": "$X.XX" }`.

### Taskfile setup

- Root `Taskfile.yml` aggregates per-component Taskfiles via `includes`.
- Useful targets:
    - `task test:all` – runs tests across collector, server, infra.
    - `task run:server` – starts local API on :8080.
    - `task run:frontend` – opens the HTMX page.
    - `task run:local` – runs both.
- Component Taskfiles:
    - `collector/Taskfile.yml`: `task collector:test`, `task collector:tidy`.
    - `server/Taskfile.yml`: `task server:test`, `task server:build`, `task server:fastdeploy`.
    - `infra/Taskfile.yml`: `task infra:test`, `task infra:synth`, `task infra:deploy`, `task infra:destroy`.

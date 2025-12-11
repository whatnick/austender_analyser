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
- Chat-style HTMX frontend hitting `/api/llm` with an optional MCP toggle for tool-aware responses; toggle also controls cache prefetch
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
- `server/` - Backend server implementation in Go (local HTTP + AWS Lambda handler) that reuses the collector logic directly. Hosts `/api/scrape` and `/api/llm` plus MCP tools.
    - `api.go`, `main.go`, `*_test.go` - API, entry point, and tests.
    - `llm_handler.go` - LLM+MCP entry with optional cache prefetch via `prefetch` flag.
    - `go.mod`, `go.sum` - Go module files for server.
- `frontend/` - Static HTMX chat page posting to `/api/llm` with MCP toggle and cache prefetch control.
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

LLM/MCP quick test:
- POST to `http://localhost:8080/api/llm` with `{ "prompt": "How much was spent by Department of Defence?", "prefetch": true }` to include cache context, or set `prefetch` false to skip collector queries. Attach `mcpConfig` JSON to allow the model to call tools.

### MCP-friendly chat frontend

- Open `frontend/index.html` (or run `task run:frontend`) and use the chat box to ask spend questions.
- Toggle “Enable MCP backend” to attach the MCP config from `frontend/config.local.js` to `/api/llm` calls. When off, the server also skips cache prefetch to isolate pure LLM responses.
- Responses include any prefetched cache context when the server can answer locally.

### Architecture (high level)
- The frontend is a static HTMX chat page. Each send posts JSON to `/api/llm` and optionally includes an MCP config and `prefetch` flag.
- The server `llmHandler` parses prompts, optionally prefetches spend totals from the collector cache when `prefetch` is true, injects that context, and passes prompts to the selected LLM (OpenAI-compatible via langchaingo). MCP requests are forwarded as config so downstream agents can call tools.
- The collector provides scraping/search helpers and cached parquet-backed spend lookups consumed by the server.
- Infra packages the server for Lambda/API Gateway and serves the static frontend via S3/CloudFront.

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

## Allied MCP ideas for spend analysis

These MCPs pair well with the Austender tools to broaden government spend context:

- OpenSpending / Government Expenditure MCP: query consolidated budget or COFOG-classified spend datasets (e.g., GIFT/OKFN catalogs).
- USAspending MCP: pull US federal contract and assistance obligations for cross-jurisdiction comparisons.
- EU TED/Open Procurement MCP: fetch EU tender awards to benchmark vendor exposure across regions.
- Open Contracting Data Standard (OCDS) Registry MCP: discover and pull published OCDS releases from other jurisdictions for side-by-side analysis.
- Data.gov.au Budget/PAES MCP: surface Australian budget paper line items to compare appropriations vs. awarded contract spend.

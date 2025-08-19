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
- Frontend implementation in Angular/React/HTMX TBD
- Fuzzy name matching in Google "did you mean" style
- Temporal aggregate spend on raw AUD values, not inflation adjusted (I am not an economist)
- Serverless hosting in AWS/Fly.io
- DynamoDB cache of Austender data downloadable as CSV


## Roadmap
- Download one search result - KPMG
- Identify fields to cache from Austender
- Cron to download and populate dynamodb/clickhouse with Austender info
- Webserver API to serve 1 set results
    - total spend on org all time
    - last 5 years
    - last year
- TDD
- Go project layout as it matures : https://github.com/golang-standards/project-layout

## Folder Structure

The repository is organized as follows:

- `collector/` - Go CLI tool for scraping Austender data. Contains main logic and command utilities.
    - `cmd/` - Cobra command implementations and scraping utilities.
    - `main.go` - Entry point for the CLI tool.
    - `go.mod`, `go.sum` - Go module files.
    - `README.md` - Collector-specific documentation.
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
- `server/` - Backend server implementation in Go.
    - `main.go`, `main_test.go` - Server entry point and tests.
    - `Taskfile.yml` - Task automation for server.
    - `go.mod`, `go.sum` - Go module files for server.

Other files:
- `LICENSE.md` - License information.
- `Taskfile.yml` - Project-wide task automation.

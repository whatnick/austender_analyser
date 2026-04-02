# Copilot Development Instructions

> Use this file for always-on, cross-cutting guidance. Domain-specific workflows live under `.github/skills/` and should be used for frontend, backend, infra, and CLI work.

## Repository Intent
- Provide quick, auditable summaries of Australian government contract spend using Austender and state procurement data normalized into OCDS.
- Keep the collector, server, frontend, and infra aligned around the same data contract and deployment flow.
- Preserve the serverless-first footprint: static frontend, Lambda/API backend, and file-backed ClickHouse-style local analytics.

## Domain Skills
- Use `.github/skills/frontend-development/` for `frontend/` work, HTMX UI updates, and static page validation.
- Use `.github/skills/backend-development/` for `server/` work, API handlers, MCP tooling, and Lambda parity.
- Use `.github/skills/infra-development/` for `infra/` CDK changes, packaging, deployment, and cloud resource updates.
- Use `.github/skills/cli-development/` for `collector/` work, scraping, cache/index logic, ClickHouse query flows, and CLI behavior.

## Shared Engineering Rules
- OCDS is the canonical contract. When fields or shapes change, update shared structs and keep the server and MCP surfaces in sync.
- Design scraping, parsing, and cache/data-lake changes in `collector/` first. The server should reuse collector helpers rather than fork logic.
- Keep HTTP and Lambda behavior aligned in `server/`. Validation, payload shapes, and error semantics should match in both modes.
- Keep the frontend static. Environment-specific behavior belongs in `frontend/config.local.js` or deployment configuration, not in a build step.
- Prefer Taskfile entrypoints when available. Mirror commands in `hack/` only when they are part of the established workflow.

## Tooling Expectations
- Go 1.25.x is required.
- Run `go fmt` and `go test ./...` in each touched Go module.
- Run `go mod tidy` in the affected module when dependencies change.
- Avoid live Austender or cloud calls in tests unless the task explicitly requires a real smoke path.
- Update docs when commands, workflows, storage behavior, or deployment steps change.

## Validation Defaults
- Collector changes: validate with `task collector:test` or `cd collector && go test ./...`.
- Server changes: validate with `task server:test` or `cd server && go test ./...`.
- Infra changes: validate with `task infra:test` and relevant `cdk synth` flows when needed.
- Full-stack or workflow changes: prefer `task test:all`, targeted smoke scripts in `hack/`, and the relevant domain skill guidance.
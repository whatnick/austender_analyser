---
name: cli-development
description: 'Use when working on the austender collector CLI in collector/, including Cobra commands, scraping sources, cache/index logic, ClickHouse-backed query flows, parquet lake behavior, and CLI smoke or unit tests.'
argument-hint: 'Describe the collector command, cache flow, source, or query behavior to change.'
---

# CLI Development

Repo-specific workflow for the collector CLI and reusable data-layer helpers in `collector/`.

## When to Use
- Editing files in `collector/`.
- Changing Cobra commands, cache behavior, query helpers, or ClickHouse-backed analytics flows.
- Updating scraping sources for federal, NSW, VIC, SA, WA, or adding a new jurisdiction.
- Modifying the parquet lake, `clickhouse-index.json`, or collector test and smoke coverage.

## Constraints
- Scraping, parsing, and columnar cache logic belongs in the collector first.
- Preserve the partitioned parquet lake plus `clickhouse-index.json` model unless the task explicitly changes storage architecture.
- Keep state and jurisdiction source registration consistent in `collector/cmd/*_source.go` and related registration paths.
- Server-facing helpers like `RunSearchWithCache`, catalog lookup, and contract queries should remain stable or be updated together with backend consumers.

## Workflow
1. Decide whether the change touches scraping, cache/index behavior, analytics query helpers, or CLI UX.
2. If changing a jurisdiction source, keep the source registration, month-window behavior, and fixtures/tests aligned.
3. For cache changes, update both on-disk behavior and query/readback paths together.
4. For query changes, verify both the collector-side helper and any smoke coverage using `clickhouse-local`.
5. When dependencies or command surfaces change, update the collector docs and any matching scripts or CI workflows.

## Validation
- Run `cd collector && go test ./...` or `task collector:test`.
- Use `bash hack/smoke-clickhouse.sh` for ClickHouse-backed smoke coverage.
- For broader workflow changes, validate repo tasks such as `task build:all` or `task test:all` when relevant.

## References
- Collector overview: `collector/README.md`
- ClickHouse smoke script: `hack/smoke-clickhouse.sh`
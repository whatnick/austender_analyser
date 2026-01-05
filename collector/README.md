# Collector

Colly-powered CLI used for scraping Austender (federal) and state procurement portals, priming the Parquet lake, and exposing reusable helpers for the server.

## Capabilities
- Streams OCDS releases into `~/.cache/austender/lake/fy=YYYY-YY/month=YYYY-MM/agency=<key>/company=<key>` with metadata tracked in `catalog.sqlite`.
- Supports keyword (`--k`), company (`--c`), agency (`--d`), and jurisdiction (`--source federal|nsw|vic|sa|wa`) filters. Filters are optional when priming the cache.
- Implements skip logic to avoid re-fetching month partitions already present in the lake.
- Provides reindexing (`reindex-lake`) to reconcile the catalog with on-disk Parquet files.
- Exports helpers such as `RunSearchWithCache`, jurisdiction detection, and catalog lookups for server reuse.

## Commands
- `go run . --k KPMG` – aggregate spend using keyword match (defaults to 20-year lookback when start date omitted).
- `go run . --c Accenture --source nsw` – constrain to company + jurisdiction.
- `go run . cache --lookback-period 5` – hydrate the cache across five years before re-running `reindex-lake` automatically (used by `task collector:prime-lake`).
- `go run . reindex-lake --cache-dir <path>` – rebuild `catalog.sqlite` when files change out of band.

## Environment Variables
- `AUSTENDER_CACHE_DIR` – override cache root (defaults to `~/.cache/austender`).
- `AUSTENDER_USE_CACHE=false` – bypass Parquet/SQLite cache and scrape directly (mostly for debugging).
- `AUSTENDER_LOOKBACK_PERIOD` – default year window when no start date is supplied (falls back to 20).

## Build & Test
- Build binary into repo `dist/collector`: `task collector:build`
- Run tests with coverage: `task collector:test`
- Lint/go mod tidy: `task collector:tidy`

## Notes
- The CLI’s root command mirrors `collector cache` semantics. When no filters are supplied it hydrates the entire date window to keep the cache broad.
- Long flag names follow the single-letter IDs (`--k`, `--c`, `--d`); Cobra does not expose extended aliases today.
- Server requests flow through the same `RunSearchWithCache` code path, so changes here propagate to `/api/scrape` and the LLM agent automatically.
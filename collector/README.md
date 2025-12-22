# Collector
- CLI Tool built on [colly](http://go-colly.org/)
- Shared scraping and cache logic imported by the server
- Multi-source scraping via the `--source` flag (federal default) with dedicated adapters for NSW, VIC, SA, and WA portals.

## Roadmap
- Go Testing , target coverage 80%
- GitHub actions, target publish multiplatform binaries
- Refactor and packages with high level code only in main

## Local datalake cache
- `collector cache` (and the default `austender` root command) write to a partitioned Parquet lake under `~/.cache/austender/lake/fy=YYYY-YY/month=YYYY-MM/agency=<key>/company=<key>` and index files in `catalog.sqlite` for fast discovery. Keyword is optional; running without filters will hydrate the lake for the date window.
- All valued releases stream into the lake via `OnAnyMatch`, even when filters are provided, so the cache remains complete.
- Fetcher skips month windows that already have parquet files in the lake to avoid re-downloading.
- Environment: `AUSTENDER_CACHE_DIR` overrides the lake root; `AUSTENDER_USE_CACHE=false` bypasses cache.
- If the catalog drifts from on-disk files, run `collector reindex-lake --cache-dir <dir>` to rescan the lake and rebuild `parquet_files`.

## Useful commands
- Prime lake and reindex (Task): `task collector:prime-lake -- --lookback-period 5` (filters optional; keyword/company/agency empty hydrates everything in window)
- Prime lake and reindex (shell): `bash ../hack/prime-datalake.sh --lookback-period 5`
- Build binary: `task collector:build` or `bash ../hack/build-collector.sh`
- Query with cache: `go run . cache --keyword KPMG` (all filters optional)
- Target another jurisdiction: append `--source <federal|nsw|sa|vic|wa>` to collector commands.
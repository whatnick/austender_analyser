# Collector v0.0.1

Initial binary release for the Austender collector CLI. This cut captures the new cross-platform build pipeline and streamlines local demos so anyone can try the scraper with zero AWS credentials.

## Highlights
- Multi-architecture build workflow publishes Linux (amd64/arm64), macOS (amd64/arm64), and Windows (amd64) binaries as GitHub release artifacts.
- CI upgraded to Go 1.25.x with optional AWS credential configuration, ensuring tests succeed in secure/offline environments.
- `task collector:run` now defaults to a demo scrape for `Accenture`, making it easy to validate the pipeline without extra flags.
- Root Taskfile no longer requires an AWS profile, enabling contributors to run server/frontend tasks locally with minimal setup.

## Artifacts
Each release publishes the following files:
- `collector-linux-amd64.tar.gz`
- `collector-linux-arm64.tar.gz`
- `collector-darwin-amd64.tar.gz`
- `collector-darwin-arm64.tar.gz`
- `collector-windows-amd64.zip`

## Verification Checklist
1. Download the artifact for your platform from the GitHub release.
2. Extract the archive and run `./collector --keyword Accenture --limit 5` (use `collector.exe` on Windows).
3. Confirm OCDS release packages are written to STDOUT and that error logs remain empty.
4. Optionally run `task collector:test` to re-run unit tests locally.

## Publishing This Release
1. Ensure `main` is up to date and contains this commit.
2. Tag the commit: `git tag collector-v0.0.1`.
3. Push the tag: `git push origin collector-v0.0.1`.
4. The `Collector Release` workflow builds artifacts and attaches this file as the release notes body.

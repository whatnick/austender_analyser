# Deployment Guide

This document covers every supported way to run the Austender Analyser stack — from a single `go run .` on your laptop to fully automated serverless and container-based cloud deployments.

## Prerequisites (all paths)

| Tool | Version | Notes |
|------|---------|-------|
| Go | 1.25+ | `GOTOOLCHAIN=go1.25.5` is set by every Taskfile |
| Task | 3.x | Optional — shell equivalents exist in `hack/` |
| Git | any | For cloning and CI workflows |

Path-specific prerequisites are listed in each section below.

## Path 1 — Local Development

The zero-dependency path. No Docker, no AWS credentials, no cloud accounts.

### Quick start

```bash
# Terminal 1: API server on :8080
task run:server          # or: bash hack/run-server.sh

# Terminal 2: open the HTMX frontend
task run:frontend        # or: bash hack/run-frontend.sh

# Or launch both at once (server backgrounds, frontend opens)
task run:local           # or: bash hack/run-local.sh
```

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `AUSTENDER_MODE` | `local` | Set to `lambda` for Lambda entry point |
| `AUSTENDER_CACHE_DIR` | `~/.cache/austender` | Parquet lake and ClickHouse-friendly index location |
| `AUSTENDER_USE_CACHE` | `true` | Enable the Parquet-backed disk cache |
| `AUSTENDER_CACHE_TZ` | `Australia/Sydney` | Timezone for same-day in-memory cache bucketing |
| `OPENAI_API_KEY` | — | Required for LLM endpoint (OpenAI backend) |
| `AUSTENDER_OCDS_BASE_URL` | `https://api.tenders.gov.au/ocds` | Override the upstream OCDS API |
| `AUSTENDER_LOOKBACK_PERIOD` | `20` | Default lookback years when no start date given |

**Tip:** Store your OpenAI key in `~/.config/austender/openai.env` (as `export OPENAI_API_KEY=sk-...`) and `hack/run-local.sh` picks it up automatically.

### Priming the lake

Before querying, warm the Parquet lake so the server can respond from cache:

```bash
# Prime federal data with 5-year lookback
task collector:prime-lake -- --lookback-period 5

# Prime a specific jurisdiction
cd collector && go run . cache --source nsw --lookback-period 3
```

### Verifying the server

```bash
# Smoke test
curl -s http://localhost:8080/api/scrape -d '{"keyword":"KPMG"}' | jq .

# MCP endpoint
curl -s http://localhost:8080/api/mcp -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .

# LLM backend detection
curl -s http://localhost:8080/api/llm/models | jq .
```

---

## Path 2 — Docker / Docker Compose

Reproducible local environment with no host Go installation required.

### Additional prerequisites

| Tool | Version |
|------|---------|
| Docker | 20.10+ |
| Docker Compose | v2+ |

### Building and running

```bash
# Build the server image
task docker:build        # or: docker build -t austender-server .

# Run the full stack (server + frontend on :8080 and :3000)
task docker:run          # or: docker compose up

# Run collector inside a container
task collector:docker -- --keyword KPMG --source federal
```

### Dockerfile layout (multi-stage)

```
Stage 1 — golang:1.25-bookworm   → builds static server binary (CGO_ENABLED=0)
Stage 2 — gcr.io/distroless/static-debian12 → runs the binary as non-root
```

Both the server and collector use a Parquet lake plus `clickhouse-index.json`, so the local cache stays fully file-backed and continues to build into static binaries without an extra database runtime.

### Compose services

| Service | Port | Description |
|---------|------|-------------|
| `server` | 8080 | API server with mounted cache volume |
| `frontend` | 3000 | nginx serving `frontend/` static files |

The compose file mounts `~/.cache/austender` as a volume for lake persistence across restarts.

### Environment overrides

Pass environment variables via `.env` or `docker compose --env-file`:

```bash
OPENAI_API_KEY=sk-...
AUSTENDER_MODE=local
AUSTENDER_CACHE_TZ=Australia/Sydney
```

---

## Path 3 — AWS Serverless (Lambda + API Gateway + CloudFront)

The production path for near-zero-cost hosting with pay-per-request pricing.

### Additional prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| AWS CLI | v2 | Configured with credentials for target account |
| AWS CDK | v2 | `npm install -g aws-cdk` and bootstrapped (`cdk bootstrap`) |
| Node.js | 18+ | Required by CDK CLI |

### Architecture

```
Browser → CloudFront ─┬─→ S3 (frontend static assets, OAC)
                       └─→ API Gateway → Lambda (server binary)
                                              ↓
                                         Austender scraper
                                              ↓
                                         OCDS API / portals
```

### Building the Lambda package

```bash
# Produces dist/main.zip (linux/amd64 static binary)
task server:build

# Or manually:
cd server
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ../dist/main main.go
chmod +x ../dist/main
cd ../dist && zip main.zip main
```

The CDK stack expects `dist/main.zip` containing a `bootstrap` binary (for `provided.al2023` runtime) or a `main` binary (for the current stack configuration).

### Deploying

```bash
# Synthesize the CloudFormation template
task infra:synth

# Deploy (no approval prompt)
task infra:deploy

# Quick Lambda-only update (skips full CDK deploy)
task server:fastdeploy
```

`server:fastdeploy` reads the Lambda function name from an SSM parameter named `austender`. Create it once:

```bash
aws ssm put-parameter --name "austender" --value "InfraStack-AustenderLambdaXXXXXX" --type String
```

### Environment & secrets

| Variable | Where | Purpose |
|----------|-------|---------|
| `AWS_DEFAULT_REGION` | Shell / CI | Target region (default: `ap-southeast-1`) |
| `CDK_DEFAULT_REGION` | Shell / CI | Same, used by CDK |
| `CDK_DEFAULT_ACCOUNT` | Shell / CI | Auto-detected from `aws sts get-caller-identity` |
| `OPENAI_API_KEY` | Lambda env | Required if using the LLM endpoint |
| `AUSTENDER_MODE` | Lambda env | Must be `lambda` |

### CDK stack resources

The `infra/` stack provisions:

| Resource | CDK Construct | Purpose |
|----------|---------------|---------|
| Lambda function | `awslambda.NewFunction` | Runs the server binary |
| API Gateway | `awsapigateway.NewLambdaRestApi` | REST proxy to Lambda |
| S3 bucket | `awss3.NewBucket` | Frontend static assets (private, OAC) |
| CloudFront | `awscloudfront.NewDistribution` | CDN for S3 + HTTPS termination |

Stack outputs:
- `ApiUrl` — API Gateway endpoint
- `FrontendUrl` — CloudFront distribution domain

### Frontend deployment

Upload static assets to the S3 bucket created by the stack:

```bash
# Get bucket name from stack outputs
BUCKET=$(aws cloudformation describe-stacks --stack-name InfraStack \
  --query 'Stacks[0].Outputs[?OutputKey==`AustenderFrontendBucketXXXXXX`].OutputValue' \
  --output text)

# Sync frontend files
aws s3 sync frontend/ s3://$BUCKET/ --delete

# Invalidate CloudFront cache
DIST_ID=$(aws cloudfront list-distributions \
  --query 'DistributionList.Items[?Origins.Items[?Id==`S3-austender`]].Id' \
  --output text)
aws cloudfront create-invalidation --distribution-id $DIST_ID --paths "/*"
```

### Teardown

```bash
task infra:destroy       # or: cd infra && cdk destroy -f
```

### Cost estimate

For light usage (<1000 requests/day): effectively **$0/month** (Lambda free tier: 1M requests, CloudFront free tier: 1TB transfer, S3 free tier: 5GB).

---

## Path 4 — Container-based Cloud

For teams who prefer containers over serverless, or need long-running processes (e.g., warm caches, WebSocket MCP connections).

### Option A: Fly.io

Lowest-friction container host with a generous free tier.

**Additional prerequisites:** `flyctl` CLI (`curl -L https://fly.io/install.sh | sh`)

```bash
# First-time setup
fly launch --no-deploy           # creates fly.toml
fly secrets set OPENAI_API_KEY=sk-...

# Deploy
task fly:deploy                  # or: fly deploy

# Check status
fly status
fly logs
```

The `fly.toml` template configures:
- HTTP service on internal port 8080
- Health check on `GET /api/health`
- Auto-stop idle machines (cost savings)
- `ap-southeast-2` region (Sydney) by default

**Cost:** Free tier includes 3 shared-cpu-1x VMs with 256MB RAM. Sufficient for light usage.

### Option B: AWS ECS Fargate

For teams already invested in AWS who need container-based hosting.

```bash
# Build and push to ECR
aws ecr get-login-password | docker login --username AWS --password-stdin $ACCOUNT.dkr.ecr.$REGION.amazonaws.com
docker build -t austender-server .
docker tag austender-server:latest $ACCOUNT.dkr.ecr.$REGION.amazonaws.com/austender-server:latest
docker push $ACCOUNT.dkr.ecr.$REGION.amazonaws.com/austender-server:latest

# Deploy via your preferred IaC (CDK, Terraform, Copilot CLI, etc.)
```

**Cost:** Fargate pricing is per-vCPU-hour and per-GB-hour. A single 0.25 vCPU / 0.5GB task runs ~$9/month. Consider Fargate Spot for non-critical workloads.

### Option C: Google Cloud Run

```bash
# Build and push to Artifact Registry
gcloud builds submit --tag gcr.io/$PROJECT/austender-server

# Deploy
gcloud run deploy austender-server \
  --image gcr.io/$PROJECT/austender-server \
  --port 8080 \
  --region australia-southeast1 \
  --allow-unauthenticated \
  --set-env-vars AUSTENDER_MODE=local
```

**Cost:** Cloud Run bills per-request with a generous free tier (2M requests/month).

---

## Path 5 — CI/CD Automation

### Existing workflows

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `go_test_build.yml` | Push/PR to `main` | Runs `task coverage:stack`, uploads to Codecov |
| `collector_release.yml` | Tag `collector-v*` or `v*` | Cross-platform collector binaries → GitHub Release |

### Planned workflows

#### Serverless deploy (`deploy_serverless.yml`)

Trigger: push to `main` (gated by `AWS_ACCESS_KEY_ID` secret presence).

```
Steps: checkout → setup Go → task server:build → setup Node + CDK
     → cdk synth → cdk deploy → s3 sync frontend/ → cloudfront invalidate
```

Required GitHub Actions secrets:
- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `CDK_DEFAULT_ACCOUNT`
- `CDK_DEFAULT_REGION`
- `OPENAI_API_KEY` (for Lambda environment)

#### Container deploy (`deploy_container.yml`)

Trigger: push to `main` (gated by `FLY_API_TOKEN` or `GHCR_TOKEN` presence).

```
Steps: checkout → docker build → push to ghcr.io → (optional) fly deploy
```

Required secrets:
- `FLY_API_TOKEN` (for Fly.io deploy)
- GitHub token (automatic for GHCR push)

#### Scheduled collector (`scheduled_collector.yml`)

Trigger: weekly cron (`0 2 * * 1` — Monday 2am UTC).

```
Steps: checkout → setup Go → collector cache (each source) → upload artifacts
```

Keeps the Parquet lake warm without manual intervention.

### Adding CDK diff to PRs

Extend `go_test_build.yml` with a CDK synth step to catch infrastructure regressions:

```yaml
- name: CDK synth check
  working-directory: infra
  run: |
    npm install -g aws-cdk
    cdk synth --no-staging > /dev/null
```

---

## Collector Distribution

The collector CLI ships as standalone binaries for all major platforms via the `collector_release.yml` workflow.

### Supported targets

| OS | Arch | Archive |
|----|------|---------|
| Linux | amd64, arm64 | `.tar.gz` |
| macOS | amd64, arm64 | `.tar.gz` |
| Windows | amd64 | `.zip` |

### Creating a release

```bash
git tag collector-v1.2.3
git push origin collector-v1.2.3
# GitHub Actions builds all targets and publishes to Releases
```

Release notes are sourced from `docs/releases/<tag>.md` — create this file before tagging.

### Running the collector in Docker

For CI environments that need Chrome (VIC/WA sources use `chromedp`):

```bash
task collector:docker -- cache --source vic --lookback-period 2
```

This uses a Chromium-enabled base image suitable for headless scraping.

---

## Environment Variable Reference

| Variable | Default | Used By | Description |
|----------|---------|---------|-------------|
| `AUSTENDER_MODE` | `local` | Server | `local` for HTTP, `lambda` for Lambda |
| `AUSTENDER_CACHE_DIR` | `~/.cache/austender` | Collector, Server | Parquet lake + catalog location |
| `AUSTENDER_USE_CACHE` | `true` | Server | Enable Parquet-backed disk cache |
| `AUSTENDER_CACHE_TZ` | `Australia/Sydney` | Server | Timezone for daily cache bucketing |
| `AUSTENDER_OCDS_BASE_URL` | `https://api.tenders.gov.au/ocds` | Server | Upstream OCDS API base URL |
| `AUSTENDER_LOOKBACK_PERIOD` | `20` | Collector | Default lookback years |
| `OPENAI_API_KEY` | — | Server | OpenAI API key for LLM endpoint |
| `AWS_DEFAULT_REGION` | `ap-southeast-1` | Infra, CI | AWS target region |
| `CDK_DEFAULT_REGION` | `ap-southeast-1` | Infra | CDK target region |
| `CDK_DEFAULT_ACCOUNT` | auto-detected | Infra | AWS account ID |
| `FLY_API_TOKEN` | — | CI | Fly.io deploy token |
| `GOTOOLCHAIN` | `go1.25.5` | All | Pinned Go toolchain |

---

## Deployment Decision Guide

| Consideration | Local | Docker | AWS Serverless | Fly.io | ECS Fargate |
|---------------|-------|--------|----------------|--------|-------------|
| Setup effort | Minimal | Low | Medium | Low | High |
| Cost at rest | Free | Free | Free | Free | ~$9/mo |
| Cost under load | N/A | N/A | Pay-per-request | Scaled | Per-hour |
| Cold start | None | None | ~1-2s | ~1-2s | None |
| Long-running OK | Yes | Yes | No (15min max) | Yes | Yes |
| MCP WebSocket | Yes | Yes | No | Yes | Yes |
| Auto-scale | No | No | Yes | Yes | Yes |
| Needs AWS account | No | No | Yes | No | Yes |
| Chrome for VIC/WA | Host-only | Yes | No | Possible | Yes |

**Recommendations:**
- **Solo developer / demo:** Local or Docker
- **Low-traffic production API:** AWS Serverless (near-zero cost)
- **MCP-heavy / WebSocket clients:** Fly.io or ECS (Lambda doesn't support long-lived connections)
- **Enterprise / existing AWS org:** ECS Fargate behind ALB
- **Scheduled data collection:** GitHub Actions cron + artifact upload (any path)

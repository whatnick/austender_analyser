# Infra

Go CDK app that provisions the serverless runtime and static hosting for Austender Analyser.

## Resources
- AWS Lambda (Go) behind API Gateway using the packaged binary from `server/`.
- S3 bucket (private, BucketOwnerEnforced) for the static frontend artifacts.
- CloudFront distribution with Origin Access Control (OAC) pointing at the S3 bucket; `index.html` is served as the default root object.
- Parameterised outputs for bucket name, distribution ID, and API endpoint to support automation.

## Prerequisites
- Go 1.25+ with `cdk` CLI v2 installed.
- AWS credentials permitting Lambda, API Gateway, S3, CloudFront, and IAM operations.
- Region defaults to `ap-southeast-1`. Override via `AWS_DEFAULT_REGION` and `CDK_DEFAULT_REGION` if needed. `CDK_DEFAULT_ACCOUNT` is auto-detected when the AWS CLI is configured.

## Commands (Taskfile)
- Synth template: `task infra:synth`
- Deploy stack: `task infra:deploy`
- Destroy stack: `task infra:destroy`
- Run tests: `task infra:test`

The Taskfile wraps `cdk synth|deploy|destroy` and sets `--require-approval never` for non-interactive deploys. Scripts in `../hack/*.sh` mirror these commands for environments without Task.

## Deployment Flow
1. Build the server binary (`task server:build`) to produce `dist/main.zip`.
2. Ensure `AWS_DEFAULT_REGION`/`CDK_DEFAULT_REGION` point to the target account.
3. Run `task infra:deploy`. The CDK app uploads the Lambda package, provisions API Gateway + CloudFront, and outputs the public URLs.
4. Upload updated frontend assets to the provisioned S3 bucket (example helper coming soon). Trigger a CloudFront invalidation if cached content must refresh immediately.

## Testing
- `go test ./...` (or `task infra:test`) validates construct configuration without touching AWS.
- Populate env overrides (e.g., `AUSTENDER_STACK_NAME`, custom domain parameters) in tests to assert conditional logic.

## Notes
- Static website hosting on S3 is disabled; CloudFront serves all assets to enforce HTTPS and caching policies.
- Origin Access Identity has been replaced with Origin Access Control; ensure the target AWS account supports it (CDK handles this automatically).

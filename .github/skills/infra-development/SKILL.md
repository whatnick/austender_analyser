---
name: infra-development
description: 'Use when working on austender infrastructure in infra/, Go CDK stacks, Lambda packaging, API Gateway, S3, CloudFront, deployment workflow, or CI/CD changes tied to infrastructure and releases.'
argument-hint: 'Describe the AWS resource, deployment flow, or infra automation to change.'
---

# Infra Development

Repo-specific workflow for the Go CDK infrastructure and deployment path in `infra/`.

## When to Use
- Editing files in `infra/`.
- Changing AWS resources, stack synthesis, deployment flow, or packaging assumptions.
- Updating release automation, artifact expectations, or infra-related CI behavior.
- Adjusting Lambda packaging, CloudFront hosting, or S3 frontend delivery.

## Constraints
- Preserve the serverless-first deployment model unless explicitly asked to change it.
- Lambda packaging depends on the server build artifact in `dist/main.zip`; keep that flow intact.
- Static frontend hosting should remain compatible with S3 + CloudFront delivery.
- Avoid introducing AWS calls into tests; keep infra tests synthesis-focused.

## Workflow
1. Identify which runtime artifact the infra change depends on, usually `server/` output.
2. Update CDK constructs and any matching workflow or deployment docs together.
3. Keep account and region behavior explicit through env vars and Taskfile defaults.
4. If frontend delivery changes, account for CloudFront invalidation or asset refresh behavior.
5. If CI or release automation changes, verify the artifact names and paths remain consistent.

## Validation
- Run `task infra:test` or `cd infra && go test ./...`.
- Run `task infra:synth` when the change affects stack shape or deployment wiring.
- Validate dependent build paths, especially `task server:build`, when packaging assumptions change.

## References
- Infra overview: `infra/README.md`
- Root workflow commands: `Taskfile.yml`
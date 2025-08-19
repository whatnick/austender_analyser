## Stack Overview

This CDK app deploys:
- Lambda (Go) behind API Gateway
- S3 bucket for frontend (private, BucketOwnerEnforced)
- CloudFront distribution with S3 origin via OAC

Notes:
- Deprecated `S3Origin` has been replaced by `S3BucketOrigin.withOriginAccessControl`.
- Static website hosting is not used; CloudFront serves `index.html` as DefaultRootObject.
# Welcome to your CDK Go project!

This is a blank project for CDK development with Go.

The `cdk.json` file tells the CDK toolkit how to execute your app.

## Useful commands

 * `cdk deploy`      deploy this stack to your default AWS account/region
 * `cdk diff`        compare deployed stack with current state
 * `cdk synth`       emits the synthesized CloudFormation template
 * `go test`         run unit tests

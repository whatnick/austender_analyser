package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigateway"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfront"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfrontorigins"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/jsii-runtime-go"
)

func AddLambdaApigS3CloudfrontStack(stack awscdk.Stack) {
	// Lambda function for backend
	lambdaFn := awslambda.NewFunction(stack, jsii.String("AustenderLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_GO_1_X(),
		Handler: jsii.String("main"),
		Code:    awslambda.Code_FromAsset(jsii.String("../server"), nil),
	})

	// API Gateway REST API
	api := awsapigateway.NewLambdaRestApi(stack, jsii.String("AustenderApi"), &awsapigateway.LambdaRestApiProps{
		Handler: lambdaFn,
	})

	// S3 bucket for frontend (private + served via CloudFront using OAC)
	bucket := awss3.NewBucket(stack, jsii.String("AustenderFrontendBucket"), &awss3.BucketProps{
		// Don't enable static website hosting when using OAC; CloudFront serves index.html
		BlockPublicAccess: awss3.BlockPublicAccess_BLOCK_ALL(),
		ObjectOwnership:   awss3.ObjectOwnership_BUCKET_OWNER_ENFORCED,
	})

	// CloudFront distribution for S3 bucket using OAC (replaces deprecated S3Origin)
	s3Origin := awscloudfrontorigins.S3BucketOrigin_WithOriginAccessControl(bucket, nil)
	distribution := awscloudfront.NewDistribution(stack, jsii.String("AustenderDistribution"), &awscloudfront.DistributionProps{
		DefaultRootObject: jsii.String("index.html"),
		DefaultBehavior: &awscloudfront.BehaviorOptions{
			Origin:               s3Origin,
			ViewerProtocolPolicy: awscloudfront.ViewerProtocolPolicy_REDIRECT_TO_HTTPS,
		},
	})

	// Output API endpoint and CloudFront URL
	awscdk.NewCfnOutput(stack, jsii.String("ApiUrl"), &awscdk.CfnOutputProps{
		Value: api.Url(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("FrontendUrl"), &awscdk.CfnOutputProps{
		Value: distribution.DomainName(),
	})
}

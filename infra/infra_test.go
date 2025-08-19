package main

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

func TestInfra_ContainsCloudFrontAndS3OriginWithOAC(t *testing.T) {
	app := awscdk.NewApp(nil)
	stack := NewInfraStack(app, "TestInfra", nil)
	template := assertions.Template_FromStack(stack, nil)

	// S3 Bucket exists with BlockPublicAcls and BucketOwnerEnforced
	template.HasResourceProperties(jsii.String("AWS::S3::Bucket"), map[string]interface{}{
		"OwnershipControls": map[string]interface{}{
			"Rules": []interface{}{
				map[string]interface{}{
					"ObjectOwnership": "BucketOwnerEnforced",
				},
			},
		},
		"PublicAccessBlockConfiguration": map[string]interface{}{
			"BlockPublicAcls":  true,
			"BlockPublicPolicy": true,
			"IgnorePublicAcls": true,
			"RestrictPublicBuckets": true,
		},
	})

	// CloudFront Distribution exists
	template.ResourceCountIs(jsii.String("AWS::CloudFront::Distribution"), jsii.Number(1))

	// OAC is created (when using S3BucketOrigin.withOriginAccessControl)
	template.ResourceCountIs(jsii.String("AWS::CloudFront::OriginAccessControl"), jsii.Number(1))
}

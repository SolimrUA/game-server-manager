package main

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigateway"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscertificatemanager"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfront"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfrontorigins"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscognito"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53targets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctionstasks"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type ControlPanelStackProps struct {
	awscdk.StackProps
	Config Config
}

// NewControlPanelStack builds the control panel: the web site, the API behind it, and the Lambda that
// acts on the game servers. Nothing here holds state - see addApi and the Cognito block for what is
// referenced rather than declared.
func NewControlPanelStack(scope constructs.Construct, id string, props *ControlPanelStackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, &id, &props.StackProps)
	config := props.Config

	// The pool, its hosted-UI domain and its groups hold the accounts and are referenced by id. A pool
	// allows only one domain and one group of a given name; app clients it takes any number of.
	userPool := awscognito.UserPool_FromUserPoolId(stack, jsii.String("UserPool"),
		jsii.String(config.UserPoolId))

	// --- the web site ------------------------------------------------------
	distribution, controlPanelBucket := addControlPanelSite(stack, config)

	// Cognito accepts callbacks only for the host the control panel actually answers on.
	controlPanelUrl := jsii.String(fmt.Sprintf("https://%s", *distribution.DistributionDomainName()))
	if config.FrontEndDomain != "" {
		controlPanelUrl = jsii.String(fmt.Sprintf("https://%s", config.FrontEndDomain))
	}

	userPoolClient := awscognito.NewUserPoolClient(stack, jsii.String("UserPoolClient"), &awscognito.UserPoolClientProps{
		UserPool:       userPool,
		GenerateSecret: jsii.Bool(false),
		OAuth: &awscognito.OAuthSettings{
			Flows:        &awscognito.OAuthFlows{AuthorizationCodeGrant: jsii.Bool(true)},
			Scopes:       &[]awscognito.OAuthScope{awscognito.OAuthScope_OPENID(), awscognito.OAuthScope_COGNITO_ADMIN()},
			CallbackUrls: &[]*string{jsii.String(*controlPanelUrl + "/signed_in.html")},
			LogoutUrls:   &[]*string{jsii.String(*controlPanelUrl + "/logout.html")},
		},
		SupportedIdentityProviders: &[]awscognito.UserPoolClientIdentityProvider{
			awscognito.UserPoolClientIdentityProvider_COGNITO(),
		},
	})

	// --- who may do what, per user per server -------------------------------
	accessTable := awsdynamodb.NewTable(stack, jsii.String("AccessTable"), &awsdynamodb.TableProps{
		PartitionKey:  &awsdynamodb.Attribute{Name: jsii.String("userSub"), Type: awsdynamodb.AttributeType_STRING},
		SortKey:       &awsdynamodb.Attribute{Name: jsii.String("instanceId"), Type: awsdynamodb.AttributeType_STRING},
		BillingMode:   awsdynamodb.BillingMode_PAY_PER_REQUEST,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	updateDns := awslambda.NewFunction(stack, jsii.String("UpdateDnsLambda"), &awslambda.FunctionProps{
		Runtime:     awslambda.Runtime_PYTHON_3_12(),
		Handler:     jsii.String("lambda_function.lambda_handler"),
		Code:        awslambda.Code_FromAsset(jsii.String(config.UpdateDnsAssetDir), nil),
		MemorySize:  jsii.Number(128),
		Timeout:     awscdk.Duration_Seconds(jsii.Number(10)),
		Description: jsii.String("Points a game server's DNS record at its current public IP"),
	})
	updateDns.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:DescribeInstances"),
		Resources: jsii.Strings("*"),
	}))
	updateDns.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("route53:ChangeResourceRecordSets"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:route53:::hostedzone/%s", config.HostedZoneId)),
	}))

	// A fresh instance has no public IP for a moment. Retrying out here rather than inside the Lambda
	// keeps the wait off the Lambda's billed runtime.
	dnsTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("GetIpUpdateDns"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction:      updateDns,
		PayloadResponseOnly: jsii.Bool(true),
	})
	dnsTask.AddRetry(&awsstepfunctions.RetryProps{
		Errors:      jsii.Strings("noIpFound"),
		Interval:    awscdk.Duration_Seconds(jsii.Number(3)),
		MaxAttempts: jsii.Number(10),
		BackoffRate: jsii.Number(1),
	})
	dnsTask.AddRetry(&awsstepfunctions.RetryProps{
		Errors:      jsii.Strings("States.ALL"),
		Interval:    awscdk.Duration_Seconds(jsii.Number(5)),
		MaxAttempts: jsii.Number(5),
		BackoffRate: jsii.Number(2),
	})
	dnsStateMachine := awsstepfunctions.NewStateMachine(stack, jsii.String("UpdateDnsStateMachine"), &awsstepfunctions.StateMachineProps{
		DefinitionBody:   awsstepfunctions.DefinitionBody_FromChainable(dnsTask),
		StateMachineType: awsstepfunctions.StateMachineType_STANDARD,
		Comment:          jsii.String("Updates DNS while minimising Lambda execution runtime"),
	})

	statusBucketName := awscdk.Fn_ImportValue(jsii.String("common-infra-status-bucket-name"))

	controlLambda := awslambda.NewFunction(stack, jsii.String("ControlLambda"), &awslambda.FunctionProps{
		Runtime:    awslambda.Runtime_PYTHON_3_12(),
		Handler:    jsii.String("lambda_function.lambda_handler"),
		Code:       awslambda.Code_FromAsset(jsii.String(config.ControlLambdaAssetDir), nil),
		MemorySize: jsii.Number(128),
		// installMod and deleteMod wait on an SSM command; this stays under the API's own 15s integration
		// timeout so a timeout surfaces as this function's error rather than a generic gateway 504.
		Timeout:     awscdk.Duration_Seconds(jsii.Number(13)),
		Description: jsii.String("Acts on game server instances for the control panel"),
		Environment: &map[string]*string{
			"micro":           jsii.String("t3a.micro"),
			"small":           jsii.String("t3a.small"),
			"medium":          jsii.String("t3a.medium"),
			"large":           jsii.String("t3a.large"),
			"stepfunctionarn": dnsStateMachine.StateMachineArn(),
			"statusBucket":    statusBucketName,
			"accessTable":     accessTable.TableName(),
			"userPoolId":      userPool.UserPoolId(),
		},
	})

	accessTable.GrantReadWriteData(controlLambda)
	dnsStateMachine.GrantStartExecution(controlLambda)
	addControlLambdaPermissions(stack, controlLambda, userPool, statusBucketName, config)

	// --- the API ------------------------------------------------------------
	api := addApi(stack, controlLambda, userPool, config)

	// Everything the published config.js needs, and the values a Server stack is deployed with.
	outputs := map[string]*string{
		"ControlApiUrl":     api.Url(),
		"CognitoUserPoolId": userPool.UserPoolId(),
		"CognitoClientID":   userPoolClient.UserPoolClientId(),
		"CognitoDomainName": jsii.String(fmt.Sprintf("%s.auth.%s.amazoncognito.com", config.CognitoDomainPrefix, config.AwsRegion)),
		"ControlPanelUrl":   controlPanelUrl,
		"CloudfrontUrl":     jsii.String(fmt.Sprintf("https://%s", *distribution.DistributionDomainName())),
		"S3Bucket":          controlPanelBucket.BucketArn(),
		"TagName":           jsii.String(config.IdTagName),
		"TagValue":          jsii.String(config.IdTagValue),
	}
	for name, value := range outputs {
		awscdk.NewCfnOutput(stack, jsii.String(name), &awscdk.CfnOutputProps{Value: value})
	}
	return stack
}

// addApi wires every entry in Endpoints to the control Lambda behind the Cognito authorizer.
func addApi(stack awscdk.Stack, fn awslambda.IFunction, userPool awscognito.IUserPool, config Config) awsapigateway.RestApi {
	api := awsapigateway.NewRestApi(stack, jsii.String("ControlApi"), &awsapigateway.RestApiProps{
		RestApiName: jsii.String("Gaming control API"),
		Description: jsii.String("Control API for the gaming server manager"),
		DeployOptions: &awsapigateway.StageOptions{
			StageName: jsii.String("prod"),
		},
		DefaultCorsPreflightOptions: &awsapigateway.CorsOptions{
			AllowOrigins:     awsapigateway.Cors_ALL_ORIGINS(),
			AllowMethods:     jsii.Strings("GET", "POST", "OPTIONS"),
			AllowHeaders:     jsii.Strings("Content-Type", "X-Amz-Date", "Authorization", "X-Api-Key", "X-Amz-Security-Token"),
			AllowCredentials: jsii.Bool(true),
		},
	})

	authorizer := awsapigateway.NewCognitoUserPoolsAuthorizer(stack, jsii.String("CognitoAuthoriser"), &awsapigateway.CognitoUserPoolsAuthorizerProps{
		CognitoUserPools: &[]awscognito.IUserPool{userPool},
	})

	for _, endpoint := range Endpoints {
		integration := awsapigateway.NewLambdaIntegration(fn, &awsapigateway.LambdaIntegrationOptions{
			Proxy: jsii.Bool(false),
			RequestTemplates: &map[string]*string{
				"application/json": jsii.String(endpoint.RequestTemplate()),
			},
			IntegrationResponses: &[]*awsapigateway.IntegrationResponse{{
				StatusCode: jsii.String("200"),
				ResponseParameters: &map[string]*string{
					"method.response.header.Access-Control-Allow-Origin": jsii.String("'*'"),
				},
			}},
			Timeout: awscdk.Duration_Millis(jsii.Number(15000)),
		})

		resource := api.Root().AddResource(jsii.String(endpoint.Path), nil)
		resource.AddMethod(jsii.String(endpoint.Method()), integration, &awsapigateway.MethodOptions{
			AuthorizationType: awsapigateway.AuthorizationType_COGNITO,
			Authorizer:        authorizer,
			MethodResponses: &[]*awsapigateway.MethodResponse{{
				StatusCode: jsii.String("200"),
				ResponseParameters: &map[string]*bool{
					"method.response.header.Access-Control-Allow-Origin": jsii.Bool(false),
				},
			}},
		})
	}

	return api
}

// addControlPanelSite serves the control panel from a private bucket through CloudFront.
// CdkAssetPublisher fills the bucket, using this stack's outputs.
func addControlPanelSite(stack awscdk.Stack, config Config) (awscloudfront.Distribution, awss3.Bucket) {
	bucket := awss3.NewBucket(stack, jsii.String("WebBucket"), &awss3.BucketProps{
		BlockPublicAccess: awss3.BlockPublicAccess_BLOCK_ALL(),
		Encryption:        awss3.BucketEncryption_S3_MANAGED,
		EnforceSSL:        jsii.Bool(true),
		RemovalPolicy:     awscdk.RemovalPolicy_DESTROY,
		AutoDeleteObjects: jsii.Bool(true),
	})

	distributionProps := &awscloudfront.DistributionProps{
		DefaultRootObject: jsii.String("index.html"),
		DefaultBehavior: &awscloudfront.BehaviorOptions{
			Origin:               awscloudfrontorigins.S3BucketOrigin_WithOriginAccessControl(bucket, nil),
			ViewerProtocolPolicy: awscloudfront.ViewerProtocolPolicy_REDIRECT_TO_HTTPS,
		},
	}

	// Optional, because CloudFront refuses an alternate domain name another distribution already serves.
	if config.FrontEndDomain != "" {
		// Referenced by ARN: CloudFront requires the certificate in us-east-1.
		distributionProps.DomainNames = jsii.Strings(config.FrontEndDomain)
		distributionProps.Certificate = awscertificatemanager.Certificate_FromCertificateArn(
			stack, jsii.String("Certificate"), jsii.String(config.CertificateArn))
	}

	distribution := awscloudfront.NewDistribution(stack, jsii.String("CloudFront"), distributionProps)

	if config.FrontEndDomain != "" {
		zone := awsroute53.HostedZone_FromHostedZoneAttributes(stack, jsii.String("HostedZone"), &awsroute53.HostedZoneAttributes{
			HostedZoneId: jsii.String(config.HostedZoneId),
			ZoneName:     jsii.String(config.HostedZoneName),
		})
		awsroute53.NewARecord(stack, jsii.String("ControlPanelAlias"), &awsroute53.ARecordProps{
			Zone:       zone,
			RecordName: jsii.String(config.FrontEndDomain),
			Target:     awsroute53.RecordTarget_FromAlias(awsroute53targets.NewCloudFrontTarget(distribution)),
		})
	}

	return distribution, bucket
}

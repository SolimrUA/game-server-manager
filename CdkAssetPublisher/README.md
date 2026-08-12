# CDK Asset Publisher

This Go CDK app packages local deployment assets and publishes them to S3:

- all files under `Bash/` (one "game cartridge" install script per game server)
- all files under `FrontEnd/`
- zipped Lambda packages from `Lambda/*.py`, each with `lambda_function.py` inside the ZIP

It also writes three generated CloudFormation deployment templates, one per stack in `cfn/`:

```text
build/mcCommonInfra.assets.yaml
build/mcServerStack.assets.yaml
build/mcControlPanel.assets.yaml
```

Each generated template references the configured S3 bucket instead of GitHub where relevant (the Common template needs no substitutions, Server only needs its Bash cartridge URL rewritten, Control Panel needs the frontend/Lambda URL rewrites).
The original solution fetches the Bash and frontend files by HTTPS, so this app grants public `s3:GetObject` to the configured asset prefix. Keep this bucket/prefix for deployment assets only.

## Configure

Create a local config file using the commented sample in `config.go`, or export the same values in your shell:

```bash
$EDITOR .env
```

```text
AWS_ACCOUNT=123456789012
AWS_REGION=eu-central-1
ASSET_BUCKET_NAME=your-unique-bucket-name
CREATE_ASSET_BUCKET=true
ASSET_KEY_PREFIX=personal-game-server-manager/v1
```

`.env` is ignored by git so your account and bucket values stay local. Shell environment variables take precedence over `.env`. Set `CREATE_ASSET_BUCKET=false` if the bucket already exists and should only be used as a deployment target. If you use an existing bucket, make sure its account-level and bucket-level public access settings allow the generated prefix policy.

## Publish Assets

From this directory:

```bash
go mod tidy
cdk bootstrap aws://ACCOUNT/REGION
cdk deploy
```

The CDK deploy uploads assets into:

```text
s3://<AssetBucketName>/<AssetKeyPrefix>/
```

## Deploy the solution

After `cdk deploy`, create the three CloudFormation stacks from the generated templates, **in this order**:

1. `../build/mcCommonInfra.assets.yaml` - VPC/networking, deployed once per account+region.
2. `../build/mcControlPanel.assets.yaml` - Cognito, control API, start/stop/DNS Lambdas, CloudFront site. Deployed once.
3. `../build/mcServerStack.assets.yaml` - one game server. Deploy again for each additional server (e.g. once for Valheim, again for Vintage Story).

`IdTagName`/`IdTagValue` must be entered identically on the Control Panel stack and every Server stack - that tag is how the Control Panel discovers which EC2 instances to manage. `HostedZoneId` must likewise match between the Control Panel stack and any Server stack that sets a `Domain`.

# CDK Asset Publisher

This Go CDK app packages local deployment assets and publishes them to S3:

- `Bash/valheim.sh`
- all files under `FrontEnd/`
- zipped Lambda packages from `Lambda/*.py`, each with `lambda_function.py` inside the ZIP

It also writes a generated CloudFormation deployment template:

```text
build/mcCFNGamingServerSolution.assets.yaml
```

That generated template references the configured S3 bucket instead of GitHub.
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

## Deploy Game Server

After `cdk deploy`, create the game-server CloudFormation stack from:

```text
../build/mcCFNGamingServerSolution.assets.yaml
```

That template is the original solution template patched to use your S3-hosted assets.

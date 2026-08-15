## Personal Gamer Server Manager

Hosting your own personal gaming server is increasingly common given all the benefits and flexibility it provides, however, doing so in a secure, flexible, and cost-effective manner is not simple. To achieve low cost many people host a server on their home computer, requiring them to open up their home network firewall ports in the process and exposing their computer to the wider internet and the associated security risks. Playing a game while also hosting the server for it can be a heavy task for any computer, so often players see the best performance while running a server and client on separate machines. To overcome these challenges players often look to dedicated game-server companies which provide varying degrees of control over the underlying server such as requiring fixed server sizes, giving limited access to mods, or simply charging you a flat monthly fee no matter how much you utilize the server.

In the below blog post we will show you how to achieve both a low cost and high security solution while also providing the added benefit of flexibility to resize the server from a single core and 0.5 GiB of memory all the way to the biggest servers AWS has to offer and back down again.

More details and instructions on this solution can be found on the AWS Gametech blog here: https://aws.amazon.com/blogs/gametech//hosting-your-own-dedicated-valheim-server-in-the-cloud/

This project is based on [aws-samples/personal-game-server-manager](https://github.com/aws-samples/personal-game-server-manager). It started as a fork but has since been substantially rewritten (multi-stack architecture, multi-server support, the CDK asset publisher, and more), so it's now maintained here as a standalone repository rather than a fork.

## Architecture

The solution is split into three CloudFormation templates under [`cfn/`](cfn/):

- **Common** ([`cfn/common-infra.yaml`](cfn/common-infra.yaml)) - shared VPC/networking and the shared server-status S3 bucket. Deploy once per account+region.
- **Control Panel** ([`cfn/control-panel.yaml`](cfn/control-panel.yaml)) - Cognito login, control API, start/stop/DNS Lambdas, the CloudFront web site. Deploy once.
- **Server** ([`cfn/server-stack.yaml`](cfn/server-stack.yaml)) - one EC2 game server and everything scoped to it (Security Group, backups, auto-shutdown, status reporting). Deploy again for each game server you want to run.

Each server also reports live player count, game version, and installed mods to the control panel - see [`docs/server-status.md`](docs/server-status.md) for the data flow and how to add reporting for a new game.

The Control Panel has no CloudFormation-level dependency on any Server stack - it discovers which EC2 instances to manage purely by an EC2 tag (`IdTagName`/`IdTagValue`) at runtime. That tag must be entered identically on the Control Panel stack and every Server stack. `HostedZoneId` must likewise match between the Control Panel stack and any Server stack using a custom `Domain`.

## Deploying

Deploy in this order, from the repo root, with `aws cloudformation deploy`. The `--stack-name` values below are a fixed convention - copy them as-is (`game-server-<stack type>-cfn`, and `game-server-<game>-cfn` per server). Everything under `--parameter-overrides` is what you'll actually need to edit; placeholders are wrapped in `<...>`.

### 1. Common - once per account+region

```bash
aws cloudformation deploy \
  --template-file cfn/common-infra.yaml \
  --stack-name game-server-common-cfn
```

### 2. Control Panel - once

```bash
aws cloudformation deploy \
  --template-file cfn/control-panel.yaml \
  --stack-name game-server-controlpanel-cfn \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      FrontEndDomain=<OPTIONAL_CONTROL_SITE_DOMAIN>
```

`FrontEndDomain` is optional - omit it (or pass `FrontEndDomain=""`) to only use the default CloudFront domain.

### 3. Server - once per game

Each game gets its own short deploy guide with the exact command for that game's ports and cartridge script:

- [Valheim](docs/valheim.md)
- [Vintage Story](docs/vintagestory.md)

`GameName` names that server's resources (its EC2 `Name` tag becomes `game-server-<GameName>-ec2`) and should match the game in the stack name, e.g. `game-server-valheim-cfn`.

If you have the older single-template version of this solution deployed, note that splitting into three stacks is a breaking change - CloudFormation can't migrate resources out of a live stack into new ones. Back up anything you care about, delete the old stack, then deploy the stacks above.

## Publishing your own copy of the assets (optional, recommended for real use)

By default, `GameServer` (the game's install script), the Lambda code, and the web control site's static files are all fetched straight from this repo's `main` branch on GitHub at deploy time. That's fine for a quick test, but it means every deploy depends on a live call to `raw.githubusercontent.com`, and every game server install runs whatever is on `main` *right now* rather than a version you've reviewed and pinned.

`CdkAssetPublisher/` is a small Go CDK app that instead uploads your **local** copy of these files to your own S3 bucket and rewrites the three templates to reference that bucket, so nothing is fetched from GitHub at deploy time:

- all files under `Bash/` (one install script per game)
- all files under `FrontEnd/`
- zipped Lambda packages from `Lambda/*.py`

### Configure

From `CdkAssetPublisher/`, create a local `.env` (ignored by git) or export the same values in your shell:

```text
AWS_ACCOUNT=123456789012
AWS_REGION=eu-central-1
ASSET_BUCKET_NAME=your-unique-bucket-name
CREATE_ASSET_BUCKET=true
ASSET_KEY_PREFIX=personal-game-server-manager/v1
```

Set `CREATE_ASSET_BUCKET=false` if the bucket already exists and should only be used as a deployment target. If you use an existing bucket, make sure its account-level and bucket-level public access settings allow the generated prefix policy (the app grants public `s3:GetObject` on that one prefix only, since your EC2 instances and CloudFront need to fetch these files over HTTPS - keep this bucket/prefix for deployment assets only).

### Publish

```bash
cd CdkAssetPublisher
go mod tidy
cdk bootstrap aws://ACCOUNT/REGION
cdk deploy
```

This uploads your local files to `s3://<AssetBucketName>/<AssetKeyPrefix>/` and writes three patched templates:

```text
build/common-infra.assets.yaml
build/server-stack.assets.yaml
build/control-panel.assets.yaml
```

Use these in place of the `cfn/*.yaml` files in the [Deploying](#deploying) commands above (same stack names, same parameters - the Common template needs no rewriting, Server gets its `GameServer` default rewritten to your S3 copy, Control Panel gets its frontend/Lambda references rewritten).

## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the LICENSE file.

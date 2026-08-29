## Personal Gamer Server Manager

Hosting your own personal gaming server is increasingly common given all the benefits and flexibility it provides, however, doing so in a secure, flexible, and cost-effective manner is not simple. To achieve low cost many people host a server on their home computer, requiring them to open up their home network firewall ports in the process and exposing their computer to the wider internet and the associated security risks. Playing a game while also hosting the server for it can be a heavy task for any computer, so often players see the best performance while running a server and client on separate machines. To overcome these challenges players often look to dedicated game-server companies which provide varying degrees of control over the underlying server such as requiring fixed server sizes, giving limited access to mods, or simply charging you a flat monthly fee no matter how much you utilize the server.

In the below blog post we will show you how to achieve both a low cost and high security solution while also providing the added benefit of flexibility to resize the server from a single core and 0.5 GiB of memory all the way to the biggest servers AWS has to offer and back down again.

More details and instructions on this solution can be found on the AWS Gametech blog here: https://aws.amazon.com/blogs/gametech//hosting-your-own-dedicated-valheim-server-in-the-cloud/

This project is based on [aws-samples/personal-game-server-manager](https://github.com/aws-samples/personal-game-server-manager). It started as a fork but has since been substantially rewritten (multi-stack architecture, multi-server support, the CDK asset publisher, and more), so it's now maintained here as a standalone repository rather than a fork.

## Architecture

The solution is split into three CloudFormation templates under [`cfn/`](cfn/):

- **Common** ([`cfn/common-infra.yaml`](cfn/common-infra.yaml)) - shared VPC/networking and the shared server-status S3 bucket. Deploy once per account+region.
- **Control Panel** ([`cfn/control-panel.yaml`](cfn/control-panel.yaml)) - Cognito login, control API, start/stop/DNS Lambdas, the CloudFront web site. Deploy once.
- **Server** ([`cfn/server-stack.yaml`](cfn/server-stack.yaml)) - one EC2 game server and everything scoped to it (Security Group, backups, auto-shutdown, status reporting, a persistent EBS volume for world/mod data that outlives the instance). Deploy again for each game server you want to run.

Each server also reports live player count, game version, and installed mods to the control panel - see [`docs/server-status.md`](docs/server-status.md) for the data flow and how to add reporting for a new game.

The Control Panel has no CloudFormation-level dependency on any Server stack - it discovers which EC2 instances to manage purely by an EC2 tag (`IdTagName`/`IdTagValue`) at runtime. That tag must be entered identically on the Control Panel stack and every Server stack. `HostedZoneId` must likewise match between the Control Panel stack and any Server stack using a custom `Domain`.

## Deploying

None of these stacks fetch anything from the public internet at deploy time - the game install scripts, Lambda code, and web site files all come from an S3 bucket you control, published by `CdkAssetPublisher/`. That's a deliberate choice: nothing here should be downloading and running a script pulled live from GitHub as part of a CloudFormation deploy, and it also means every game server install runs a version of the script you've actually reviewed and pinned, not whatever happens to be on `main` right now.

Each Server stack goes one step further and verifies a script's content against a SHA256 hash pinned at deploy time (`GameServerSha256`, computed by `CdkAssetPublisher` - see step 2) before ever executing it. So even if write access to that bucket were ever compromised, an instance won't run anything that doesn't match what you actually approved the last time you deployed.

Deploy in this order, from the repo root, with `aws cloudformation deploy`. The `--stack-name` values below are a fixed convention - copy them as-is (`game-server-<stack type>-cfn`, and `game-server-<game>-cfn` per server). Everything under `--parameter-overrides` is what you'll actually need to edit; placeholders are wrapped in `<...>`.

### 1. Common - once per account+region

```bash
aws cloudformation deploy \
  --template-file cfn/common-infra.yaml \
  --stack-name game-server-common-cfn
```

### 2. Publish your assets - once per account+region

`CdkAssetPublisher/` is a small Go CDK app that uploads your **local** copy of the game install scripts, Lambda code, and web site files to your own S3 bucket:

- all files under `scripts/` (one install script per game)
- all files under `FrontEnd/`
- zipped Lambda packages from `Lambda/*.py`

From `CdkAssetPublisher/`, create a local `.env` (ignored by git) or export the same values in your shell:

```text
AWS_ACCOUNT=123456789012
AWS_REGION=eu-central-1
ASSET_BUCKET_NAME=your-unique-bucket-name
CREATE_ASSET_BUCKET=true
ASSET_KEY_PREFIX=personal-game-server-manager/v1
```

Set `CREATE_ASSET_BUCKET=false` if the bucket already exists and should only be used as a deployment target. If you use an existing bucket, make sure its account-level and bucket-level public access settings allow the generated prefix policy (the app grants public `s3:GetObject` on that one prefix only, since your EC2 instances and CloudFront need to fetch these files over HTTPS - keep this bucket/prefix for deployment assets only).

```bash
cd CdkAssetPublisher
go mod tidy
cdk bootstrap aws://ACCOUNT/REGION
cdk deploy
```

This uploads your local files to `s3://<AssetBucketName>/<AssetKeyPrefix>/` and prints the outputs you'll need for the next two steps:

- `AssetBucketName` / `AssetKeyPrefix` - pass straight through as the Control Panel's `AssetsBucketName`/`AssetsKeyPrefix` parameters below.
- `ScriptsBaseUrl` - the base URL for published game install scripts. A given game's `GameServer` parameter is this plus its script's path, e.g. `<ScriptsBaseUrl>/valheim.sh` or `<ScriptsBaseUrl>/VintageStory/install.sh`.
- `StartStopLambdaKey` / `UpdateDnsLambdaKey` - content-hashed S3 keys for the two Lambda zips, e.g. `Lambda/gaming_server_start_stop-v1_0.<hash>.zip`. Pass these as the Control Panel's parameters of the same name. They're hashed (not fixed filenames) so that changing the Lambda code always produces a different value here - CloudFormation only redeploys `AWS::Lambda::Function` code when this value itself changes, not when the object at an unchanged key does, so a fixed name would let code fixes silently fail to deploy.
- `ValheimSha256` / `VintageStoryInstallSha256` / `VintageStoryStatusAgentSha256` - SHA256 digests of each script's current content. Pass the relevant one(s) as a Server stack's `GameServerSha256`/`GameServerCompanionSha256` (see each game's deploy guide below) - the instance verifies the script it downloads matches before ever running it.

Re-run `cdk deploy` here any time you change a script, Lambda code, or the front-end files. The front-end/game-script changes take effect on the next Control Panel/Server deploy automatically (same bucket/prefix); Lambda code changes need the Control Panel re-deployed with the new `StartStopLambdaKey`/`UpdateDnsLambdaKey` values from this step's output.

### 3. Control Panel - once, and again whenever the Lambda code changes

```bash
aws cloudformation deploy \
  --template-file cfn/control-panel.yaml \
  --stack-name game-server-controlpanel-cfn \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
      AssetsBucketName=<AssetBucketName FROM STEP 2> \
      AssetsKeyPrefix=<AssetKeyPrefix FROM STEP 2> \
      StartStopLambdaKey=<StartStopLambdaKey FROM STEP 2> \
      UpdateDnsLambdaKey=<UpdateDnsLambdaKey FROM STEP 2> \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      FrontEndDomain=<OPTIONAL_CONTROL_SITE_DOMAIN>
```

`FrontEndDomain` is optional - omit it (or pass `FrontEndDomain=""`) to only use the default CloudFront domain.

### 4. Server - once per game

Each game gets its own short deploy guide with the exact command for that game's ports and cartridge script:

- [Valheim](docs/valheim.md)
- [Vintage Story](docs/vintagestory.md)

`GameName` names that server's resources (its EC2 `Name` tag becomes `game-server-<GameName>-ec2`) and should match the game in the stack name, e.g. `game-server-valheim-cfn`.

If you have the older single-template version of this solution deployed, note that splitting into three stacks is a breaking change - CloudFormation can't migrate resources out of a live stack into new ones. Back up anything you care about, delete the old stack, then deploy the stacks above.

### Redeploying a server without losing its world

A Server stack's world/mod data lives on its own EBS volume (`GameDataVolume`), independent of the EC2 instance - deleting or replacing the instance never touches it, and the volume is retained even if the stack itself is deleted. The instance's own `UserData` only ever runs once per instance (a cloud-init property, not something CloudFormation controls), so picking up an updated install script or template change always means a fresh instance - the `CreateInstance` parameter (`true`/`false`, default `true`) does that without deleting the whole stack, which would take the data volume down with it:

```bash
# 1. stop the game cleanly first (control panel Stop button, or):
aws ec2 stop-instances --instance-ids <INSTANCE_ID>
aws ec2 wait instance-stopped --instance-ids <INSTANCE_ID>

# 2. if a script changed, republish it first to get a fresh hash:
(cd CdkAssetPublisher && cdk deploy)

# 3. tear down just the instance - the data volume, backups, IAM, etc. all stay
aws cloudformation deploy --template-file cfn/server-stack.yaml \
  --stack-name game-server-<game>-cfn --capabilities CAPABILITY_IAM \
  --parameter-overrides CreateInstance=false

# 4. bring up a fresh instance - the volume reattaches (not reformats)
aws cloudformation deploy --template-file cfn/server-stack.yaml \
  --stack-name game-server-<game>-cfn --capabilities CAPABILITY_IAM \
  --parameter-overrides CreateInstance=true \
      GameServerSha256=<ValheimSha256 or VintageStoryInstallSha256 FROM STEP 2>
```

Any parameter left out of `--parameter-overrides` keeps its previous value (`aws cloudformation deploy` fills in `UsePreviousValue` for anything you don't pass), so step 4 only needs whatever actually changed - typically just the hash from a fresh publish.

## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the LICENSE file.

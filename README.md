## Personal Game Server Manager

Run your own dedicated game servers on AWS, on hardware you resize from a single core and 0.5 GiB of
memory up to the largest instances AWS offers and back down again - without opening ports on your home
network, and paying only for the hours the server is actually up.

A web control panel (Cognito login, CloudFront) starts and stops each server, resizes it, backs it up,
downloads its world, and manages its mods. Servers shut themselves down when nobody has played for a
while, and a persistent EBS volume keeps each world independent of the instance running it.

Valheim and Vintage Story are supported today; [`docs/server-status.md`](docs/server-status.md) covers
adding another game.

Based on [aws-samples/personal-game-server-manager](https://github.com/aws-samples/personal-game-server-manager),
and on the [AWS GameTech blog post](https://aws.amazon.com/blogs/gametech//hosting-your-own-dedicated-valheim-server-in-the-cloud/)
behind it.

## Architecture

Four stacks, none of which contains a game server:

- **Common** ([`cfn/common-infra.yaml`](cfn/common-infra.yaml)) - shared VPC/networking and the shared server-status S3 bucket. Deploy once per account+region.
- **Asset Publisher** ([`CdkAssetPublisher/`](CdkAssetPublisher/)) - uploads your local game scripts, Lambda code and web site to a bucket you own.
- **Fleet** ([`cdk/fleet.go`](cdk/fleet.go)) - what every server shares: the instance role, one Security Group per game, one backup plan, and the schedule that stops idle servers.
- **Control Panel** ([`cdk/controlpanel.go`](cdk/controlpanel.go)) - Cognito login, control API, start/stop/DNS Lambdas, the CloudFront site.

The servers themselves are created at runtime from the control panel by an admin, not declared in any
template. Each gets its own EBS data volume, DNS record and join password, and is picked up by the
fleet's backups and idle shutdown through its `managed-by` tag. The games it can create, and their
ports and defaults, come from [`cdk/templates.go`](cdk/templates.go).

Each server also reports live player count, game version, and installed mods to the control panel - see [`docs/server-status.md`](docs/server-status.md) for the data flow and how to add reporting for a new game.

Access is per user, per server, assigned from the control panel's own Access page rather than at deploy time - see [`docs/access.md`](docs/access.md).

The control panel finds the servers it manages by an EC2 tag (`ID_TAG_NAME`/`ID_TAG_VALUE` in its config), applied to every server it creates.

## Deploying

None of these stacks fetch anything from the public internet at deploy time. The game install scripts, Lambda code, and web site files all come from an S3 bucket you control, published by `CdkAssetPublisher/`, so every install runs a version of the script you reviewed and pinned rather than whatever is on `main`.

Each new server also verifies a script against a SHA256 hash the Fleet stack pinned at deploy time before executing it, so even write access to that bucket can't get anything executed that you didn't approve.

Deploy in this order, from the repo root. The `--stack-name` values below are a fixed convention - copy them as-is.

### 1. Common - once per account+region

```bash
aws cloudformation deploy \
  --template-file cfn/common-infra.yaml \
  --stack-name game-server-common-cfn
```

### 2. Publish your assets - once per account+region

`CdkAssetPublisher/` is a small Go CDK app that uploads your **local** copy of the game install scripts and Lambda code to your own S3 bucket:

- all files under `scripts/` (one install script per game)
- zipped Lambda packages from `Lambda/*.py`

It also publishes the web site, but straight to the bucket CloudFront serves rather than to the assets bucket - see [step 4](#4-publish-the-web-site). On a first install there is no Control Panel yet, so it says it is skipping the web site and carries on.

From `CdkAssetPublisher/`, create a local `.env` (ignored by git) or export the same values in your shell:

```text
AWS_ACCOUNT=123456789012
AWS_REGION=eu-central-1
ASSET_BUCKET_NAME=your-unique-bucket-name
CREATE_ASSET_BUCKET=true
ASSET_KEY_PREFIX=personal-game-server-manager/v1
```

Set `CREATE_ASSET_BUCKET=false` if the bucket already exists and should only be used as a deployment target. Its account- and bucket-level public access settings must allow the generated prefix policy: the app grants public `s3:GetObject` on that one prefix, since your EC2 instances and CloudFront fetch these files over HTTPS. Keep the prefix for deployment assets only.

```bash
cd CdkAssetPublisher
go mod tidy
cdk bootstrap aws://ACCOUNT/REGION
cdk deploy
```

This uploads your local files to `s3://<AssetBucketName>/<AssetKeyPrefix>/` and prints the outputs you'll need for the next two steps:

- `ScriptsBaseUrl` - the base URL the published install scripts sit under.
- `ValheimInstallSha256` / `ValheimStatusAgentSha256` / `VintageStoryInstallSha256` / `VintageStoryStatusAgentSha256` - SHA256 digests of each script's current content. A new server verifies each script it downloads against these before running it.

The Fleet stack reads these outputs at synth time, so re-run `cdk deploy` here whenever you change a
game script, then deploy `cdk/` again to pick up the new digest.

### 3. Fleet and Control Panel - once

```bash
cd cdk
cdk deploy --all
```

Configuration lives in `cdk/.env` - copy the block at the top of [`cdk/config.go`](cdk/config.go). The custom domain is optional; see [`cdk/README.md`](cdk/README.md).

### 4. Publish the control panel

Run the publisher again, this time with a Control Panel to read from:

```bash
cd CdkAssetPublisher
cdk deploy
```

This reads the Control Panel stack's outputs, writes them into `js/config.js`'s placeholders, uploads the result to the bucket CloudFront serves, and invalidates the distribution so the new files are served immediately. The staged copy is left in `build/controlpanel/` if you want to check what went up. Re-run it after any change under `FrontEnd/`; nothing else needs redeploying.

### 5. Create a server

Sign in to the control panel as a member of the Cognito `admins` group and use **New server**. Pick the
game, give the server an id (lowercase letters, digits and dashes - it names its DNS record, its data
volume and its join password), and optionally restore it from an earlier server's archive snapshot
instead of starting an empty world.

Per-game notes:

- [Valheim](docs/valheim.md)
- [Vintage Story](docs/vintagestory.md)

Whichever game you create:

- It installs itself on first boot, which takes a few minutes. The control panel shows the install
  result, and uploads the instance's own log on failure so you never need to reach the instance.
- It stops itself once it's had no players for two consecutive 15-minute checks. Start it again from
  the control panel, which is also what creates or updates its DNS record.
- Its join password is generated once and kept in SSM Parameter Store as a SecureString, keyed by
  server id, so recreating a server under the same id reuses the password players already have:

  ```bash
  aws ssm get-parameter --name game-password-<server id> --with-decryption --query Parameter.Value --output text
  ```

### Rebuilding a server without losing its world

Deleting a server from the control panel snapshots its data volume first and tags the snapshot with
the server id, then terminates the instance. Creating a new server and choosing that snapshot under
**Restore from** brings the world back - on a fresh instance, with whatever the current install script
does. The snapshot is also how you take a server down while keeping its world for later, which is
worth doing if you are near the server limit (`MAX_SERVERS`, 10 by default).

A running server's data volume is independent of the instance: stopping, starting or resizing never
touches it.

## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the LICENSE file.

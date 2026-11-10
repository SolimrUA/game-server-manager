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

None of these stacks fetch anything from the public internet at deploy time. The game install scripts and web site files come from an S3 bucket you control, published from this repository, and the Lambdas are compiled from this repository at deploy time - so every install runs a version you reviewed and pinned rather than whatever is on `main`.

Each new server is given one SHA256 hash the control panel stack pinned at deploy time. That hash covers the list of hashes for every script the install fetches, and each script is checked against its entry before anything runs it, with a script the list doesn't name refused outright - so even write access to that bucket can't get anything executed that you didn't approve.

Deploy in this order, from the repo root. The `--stack-name` values below are a fixed convention - copy them as-is.

### 1. Common - once per account+region

```bash
aws cloudformation deploy \
  --template-file cfn/common-infra.yaml \
  --stack-name game-server-common-cfn
```

### 2. Everything else - once

```bash
cd cdk
go mod tidy
cdk bootstrap aws://ACCOUNT/REGION
cdk deploy --all
```

This deploys three stacks: the assets bucket holding the game scripts, the fleet every server shares,
and the control panel with its web site. Each script is digested as it is published, and a new server
checks what it downloads against that digest before running it.

Configuration lives in `cdk/.env` - copy the block at the top of [`cdk/config.go`](cdk/config.go). The
custom domain is optional; see [`cdk/README.md`](cdk/README.md). Set `CREATE_ASSET_BUCKET=false` if the
assets bucket already exists and should only be published into; its account- and bucket-level public
access settings must then allow the prefix policy, since your EC2 instances fetch scripts over HTTPS
with no credentials of their own. Keep that prefix for deployment assets only.

Re-run the same command after changing anything under `scripts/` or `FrontEnd/`.

### 3. Create a server

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
- Its join password lives in SSM Parameter Store as a SecureString named `game-password-<server id>`.
  The instance brings its game config into line with that parameter before the game starts, so the
  parameter is the password the server enforces. Owners read and change it under **Join Password** in
  the control panel; a new one applies the next time the game starts, which is what **Reboot Game**
  is for. A server restored from another server's snapshot inherits that server's password.

### Rebuilding a server without losing its world

Deleting a server terminates the instance, leaves its data volume behind and stops backing that volume
up. What brings a world back is a backup, so take one with **Backup Now** first unless the newest
scheduled one is recent enough: creating a server and choosing a backup under **Restore from** rebuilds
the world on a fresh instance, with whatever the current install script does. That is also how you take
a server down while keeping its world, worth doing if you are near the server limit (`MAX_SERVERS`, 10
by default).

A running server's data volume is independent of the instance: stopping, starting or resizing never
touches it.

### The worlds a server has held

Restoring a backup builds a new data volume from it, so a server rebuilt more than once has a volume
per world it has held, and **Restore from** offers every backup marked primary. The admin-only **Backups**
page lists every volume currently or previously attached to a server, with the backups taken from
each, and is where they are managed: keeping a backup or giving it an expiry, marking one primary or
defunct, deleting backups or a volume outright, and stopping backups of a volume set aside.

**Mount to Server** puts a stopped server back onto one of its own earlier volumes without a rebuild.
The volume has to be free, from the same game, and in the server's availability zone; a world from
elsewhere is reached by creating a server from its backup instead.

## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the LICENSE file.

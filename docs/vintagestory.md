# Deploying a Vintage Story server

Requires the Common stack, [published assets](../README.md#2-publish-your-assets---once-per-account-region), and the Control Panel stack already deployed - see the root [README.md](../README.md#deploying).

Vintage Story needs both TCP and UDP on port 42420 (see [`scripts/VintageStory/install.sh`](../scripts/VintageStory/install.sh)), which is different from the Server template's Valheim-oriented UDP defaults, so all four port parameters are set explicitly below.

```bash
aws cloudformation deploy \
  --template-file cfn/server-stack.yaml \
  --stack-name game-server-vintagestory-cfn \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
      GameName=vintagestory \
      GameServer=<ScriptsBaseUrl FROM CdkAssetPublisher>/VintageStory/install.sh \
      GameServerSha256=<VintageStoryInstallSha256 FROM CdkAssetPublisher> \
      GameServerCompanionSha256=<VintageStoryStatusAgentSha256 FROM CdkAssetPublisher> \
      KeyName=<YOUR_EC2_KEY_PAIR> \
      InstanceType=t3a.medium \
      GamingTCPTrafficPortStart=42420 \
      GamingTCPTrafficPortEnd=42420 \
      GamingUDPTrafficPortStart=42420 \
      GamingUDPTrafficPortEnd=42420 \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      Domain=<YOUR_VINTAGESTORY_SUBDOMAIN> \
      ShutdownTimeHours="None - do not auto shut-down" \
      ShutdownTimeMins="Not Applicable"
```

Notes:

- `GameServer` has no default and must always be supplied - point it at [`scripts/VintageStory/install.sh`](../scripts/VintageStory/install.sh) as published to your own S3 bucket by `CdkAssetPublisher` (its `ScriptsBaseUrl` output plus `/VintageStory/install.sh`). CloudFormation never fetches this from GitHub. `GameServer` must point at `install.sh` specifically (not `status_agent.py`) - the install script fetches its companion `status_agent.py` from the same directory at boot, so both files need to stay side by side wherever you host them (which they will, as long as you're pointing at the `CdkAssetPublisher`-published copy).
- `GameServerSha256` and `GameServerCompanionSha256` are both required (`CdkAssetPublisher`'s `VintageStoryInstallSha256` and `VintageStoryStatusAgentSha256` outputs) - the instance verifies both scripts against these hashes before ever running them. Re-run `cdk deploy` in `CdkAssetPublisher/` after any script change to get fresh values.
- `Domain` is optional - pass `Domain=""` if you don't want a custom DNS name for this server.
- `HostedZoneId` must match the value used on your Control Panel stack.
- The install script pins a specific Vintage Story version (`VSVERSION` near the top of `scripts/VintageStory/install.sh`) - bump it there if you want a newer release.
- The stack waits for the install to actually finish before reporting success - `aws cloudformation deploy` can take several minutes and will fail with a real error if the install fails, rather than reporting success regardless.
- After the stack finishes, the instance installs Vintage Story on first boot and then **shuts itself down**. Start it from the control panel when you're ready to play - that's also what triggers the DNS record to be created/updated, so every server's first-ever appearance online goes through the same start flow (rather than the record lagging behind an instance that was already running).
- World save, mods, and `serverconfig.json` live on their own EBS volume (`DataVolumeSize` parameter, default 10 GiB), independent of the instance - deleting or replacing the instance (see [redeploying without losing your world](../README.md#redeploying-a-server-without-losing-its-world) in the root README) never touches it. The volume is only ever formatted the first time it's used; a reused one keeps its data, including any manual edits to `serverconfig.json`, exactly as it was left.
- The join password is generated once and stored in SSM Parameter Store (`game-password-<stack name>`, SecureString) - reinstalling the server (e.g. deleting and redeploying this stack) reuses it rather than generating a new one, since that parameter isn't owned by this stack and survives its deletion. Retrieve it with `aws ssm get-parameter --name game-password-<stack name> --with-decryption --query Parameter.Value --output text`.
- The install also sets up a `vintagestory-status-agent` systemd service that reports player count, version, and mods to the control panel every 5 seconds (only pushing on change) - see [`docs/server-status.md`](server-status.md). Player count comes from the game's own `/stats` console command, not log parsing; if it isn't tracking correctly, check the regex in [`scripts/VintageStory/status_agent.py`](../scripts/VintageStory/status_agent.py) against what `/stats` actually prints on your server.
- Run `server.sh` commands (`stop`, `restart`, `command ...`) as the `vintagestory` user (`sudo -u vintagestory /home/vintagestory/server/server.sh restart`), not as `ubuntu` directly - `screen` (which `server.sh` uses to run the game) scopes sessions per-user, so a command run as a different user won't find the session systemd created, even though `ubuntu` is in the `vintagestory` group.

## Uploading your world save or mods

The server's data lives under `/home/vintagestory/data` on the EC2 instance - `Saves/` for world files, `Mods/`
for mods. To copy files there from your machine, you need SSH access; the security group only allows this through
[EC2 Instance Connect](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-connect.html) (not open SSH),
so push a short-lived key first:

```bash
ssh-keygen -t rsa -b 2048 -f /tmp/vs-key -N ""
aws ec2-instance-connect send-ssh-public-key \
  --instance-id <YOUR_INSTANCE_ID> \
  --availability-zone <YOUR_AZ, e.g. eu-central-1a> \
  --instance-os-user ubuntu \
  --ssh-public-key file:///tmp/vs-key.pub
```

An `ed25519` key here gets rejected by the AWS CLI (`Invalid length for parameter SSHPublicKey`) - use RSA as above.

That key is valid for about a minute, which is enough to start an `scp`/`rsync` copy (the transfer itself can keep
running once started). **Stop the server first** (`sudo -u vintagestory /home/vintagestory/server/server.sh stop`,
or the control panel's Stop button) so nothing's mid-write while you copy:

```bash
# world save
scp -i /tmp/vs-key -r ./MyWorld.vcdbs ubuntu@<INSTANCE_PUBLIC_IP>:/tmp/
ssh -i /tmp/vs-key ubuntu@<INSTANCE_PUBLIC_IP> \
  'sudo mv /tmp/MyWorld.vcdbs /home/vintagestory/data/Saves/ && sudo chown vintagestory:vintagestory /home/vintagestory/data/Saves/MyWorld.vcdbs'

# a mod (zip or folder, whichever the mod ships as)
scp -i /tmp/vs-key ./mymod.zip ubuntu@<INSTANCE_PUBLIC_IP>:/tmp/
ssh -i /tmp/vs-key ubuntu@<INSTANCE_PUBLIC_IP> \
  'sudo mv /tmp/mymod.zip /home/vintagestory/data/Mods/ && sudo chown vintagestory:vintagestory /home/vintagestory/data/Mods/mymod.zip'
```

Which save the server actually loads on startup is controlled by `serverconfig.json` in that same data folder -
check Vintage Story's own server documentation for the current field name, since it's changed across versions.
Once that points at your uploaded save, start the server from the control panel as usual.

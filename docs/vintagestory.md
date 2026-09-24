# Deploying a Vintage Story server

Requires the Common stack, [published assets](../README.md#2-publish-your-assets---once-per-account-region), and the Control Panel stack already deployed - see the root [README.md](../README.md#deploying).

Vintage Story needs both TCP and UDP on port 42420, which is not what the template defaults to, so all four port parameters are set explicitly below.

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
      GameVersion=1.22.7 \
      DataPath=/home/vintagestory/data \
      WorldDownloadPaths=Saves,Mods,Playerdata \
      WorldDownloadCorePaths=Saves \
      KeyName=<YOUR_EC2_KEY_PAIR> \
      InstanceType=t3a.medium \
      GamingTCPTrafficPortStart=42420 \
      GamingTCPTrafficPortEnd=42420 \
      GamingUDPTrafficPortStart=42420 \
      GamingUDPTrafficPortEnd=42420 \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      Domain=<YOUR_VINTAGESTORY_SUBDOMAIN>
```

Notes:

- `GameServer` must point at [`install.sh`](../scripts/VintageStory/install.sh) specifically, not `status_agent.py`: the install script fetches its companion from the same directory at boot, so both files have to stay side by side wherever you host them.
- `GameVersion` is required, and is the exact version installed - see [the download page](https://account.vintagestory.at/downloads) for what's current. A `CreateInstance` cycle reinstalls from scratch, so keep it in step with what the server is actually running, including after an in-place "Update Game".
- A `vintagestory-status-agent` systemd service reports player count, version, mods, and the game's own last-autosave time to the control panel (see [`docs/server-status.md`](server-status.md)). Those come from the game's `/stats` console command, so if the player count looks wrong, check the regex in [`status_agent.py`](../scripts/VintageStory/status_agent.py) against what `/stats` prints on your server.
- Run `server.sh` commands as the `vintagestory` user (`sudo -u vintagestory /home/vintagestory/server/server.sh restart`), not as `ubuntu`: `screen` scopes sessions per-user, so another user won't find the session systemd created, group membership notwithstanding.

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

Which save the server loads on startup is set in `serverconfig.json` in that same data folder - check Vintage
Story's own server documentation for the current field name, which has changed across versions. Once it points at
your uploaded save, start the server from the control panel as usual.

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
- `Domain` is optional - pass `Domain=""` if you don't want a custom DNS name for this server.
- `HostedZoneId` must match the value used on your Control Panel stack.
- The install script pins a specific Vintage Story version (`VSVERSION` near the top of `scripts/VintageStory/install.sh`) - bump it there if you want a newer release.
- After the stack finishes, the instance installs Vintage Story on first boot and then **shuts itself down**. Start it from the control panel when you're ready to play - that's also what triggers the DNS record to be created/updated, so every server's first-ever appearance online goes through the same start flow (rather than the record lagging behind an instance that was already running).
- The install also sets up a `vintagestory-status-agent` systemd service that reports player count, version, and mods to the control panel every 5 seconds (only pushing on change) - see [`docs/server-status.md`](server-status.md). If player counts don't track correctly, check `server-main.log`'s actual join/leave line format against the regexes in [`scripts/VintageStory/status_agent.py`](../scripts/VintageStory/status_agent.py) and adjust.

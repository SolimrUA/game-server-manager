# Deploying a Valheim server

Requires the Common stack, [published assets](../README.md#2-publish-your-assets---once-per-account-region), and the Control Panel stack already deployed - see the root [README.md](../README.md#deploying).

Valheim only needs UDP (ports 2456-2458, which is the Server template's default), so TCP is left empty.

```bash
aws cloudformation deploy \
  --template-file cfn/server-stack.yaml \
  --stack-name game-server-valheim-cfn \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
      GameName=valheim \
      GameServer=<ScriptsBaseUrl FROM CdkAssetPublisher>/valheim.sh \
      KeyName=<YOUR_EC2_KEY_PAIR> \
      InstanceType=t3a.medium \
      GamingTCPTrafficPortStart="" \
      GamingTCPTrafficPortEnd="" \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      Domain=<YOUR_VALHEIM_SUBDOMAIN> \
      ShutdownTimeHours="None - do not auto shut-down" \
      ShutdownTimeMins="Not Applicable"
```

Notes:

- `GameServer` has no default and must always be supplied - point it at [`scripts/valheim.sh`](../scripts/valheim.sh) as published to your own S3 bucket by `CdkAssetPublisher` (its `ScriptsBaseUrl` output plus `/valheim.sh`). CloudFormation never fetches this from GitHub.
- `Domain` is optional - pass `Domain=""` if you don't want a custom DNS name for this server (you'll get a fresh IP every time you stop/start it instead).
- `HostedZoneId` must match the value used on your Control Panel stack.
- `GamingUDPTrafficPortStart`/`GamingUDPTrafficPortEnd` are omitted - they default to `2456`/`2458`, which is what Valheim needs.
- After the stack finishes, the instance installs Valheim on first boot and then **shuts itself down**. Start it from the control panel when you're ready to play - that's also what triggers the DNS record to be created/updated, so every server's first-ever appearance online goes through the same start flow (rather than the record lagging behind an instance that was already running).

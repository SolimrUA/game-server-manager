# Deploying a Vintage Story server

Requires the Common and Control Panel stacks already deployed - see the root [README.md](../README.md#deploying).

Vintage Story needs both TCP and UDP on port 42420 (see [`Bash/vintagestory.sh`](../Bash/vintagestory.sh)), which is different from the Server template's Valheim-oriented UDP defaults, so all four port parameters are set explicitly below.

```bash
aws cloudformation deploy \
  --template-file cfn/server-stack.yaml \
  --stack-name game-server-vintagestory-cfn \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
      GameName=vintagestory \
      GameServer=https://raw.githubusercontent.com/<YOUR_FORK>/personal-game-server-manager/main/Bash/vintagestory.sh \
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

- `GameServer` must be overridden - the template's default points at `valheim.sh`, not this game. Replace `<YOUR_FORK>` with wherever you've pushed this repo (raw.githubusercontent.com only serves files that are actually pushed to that branch). If you've [published your own assets to S3](../README.md#publishing-your-own-copy-of-the-assets-optional-recommended-for-real-use), point this at your S3 copy instead (or use the generated `build/server-stack.assets.yaml` template, which already defaults to your S3-hosted Valheim script but still needs `GameServer` overridden for Vintage Story).
- `Domain` is optional - pass `Domain=""` if you don't want a custom DNS name for this server.
- `HostedZoneId` must match the value used on your Control Panel stack.
- The install script pins a specific Vintage Story version (`VSVERSION` near the top of `Bash/vintagestory.sh`) - bump it there if you want a newer release.

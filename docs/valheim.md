# Deploying a Valheim server

Requires the Common and Control Panel stacks already deployed - see the root [README.md](../README.md#deploying).

Valheim only needs UDP (ports 2456-2458, which is the Server template's default), so TCP is left empty.

```bash
aws cloudformation deploy \
  --template-file cfn/server-stack.yaml \
  --stack-name game-server-valheim-cfn \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
      GameName=valheim \
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

- `GameServer` is omitted above - the template's default already points at [`Bash/valheim.sh`](../Bash/valheim.sh) on GitHub. If you've [published your own assets to S3](../README.md#publishing-your-own-copy-of-the-assets-optional-recommended-for-real-use), use that generated `build/server-stack.assets.yaml` template instead and you can still omit `GameServer` - it'll already point at your S3 copy.
- `Domain` is optional - pass `Domain=""` if you don't want a custom DNS name for this server (you'll get a fresh IP every time you stop/start it instead).
- `HostedZoneId` must match the value used on your Control Panel stack.
- `GamingUDPTrafficPortStart`/`GamingUDPTrafficPortEnd` are omitted - they default to `2456`/`2458`, which is what Valheim needs.

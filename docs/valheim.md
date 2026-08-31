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
      GameServer=<ScriptsBaseUrl FROM CdkAssetPublisher>/Valheim/install.sh \
      GameServerSha256=<ValheimInstallSha256 FROM CdkAssetPublisher> \
      GameServerCompanionSha256=<ValheimStatusAgentSha256 FROM CdkAssetPublisher> \
      KeyName=<YOUR_EC2_KEY_PAIR> \
      InstanceType=t3a.medium \
      GamingTCPTrafficPortStart="" \
      GamingTCPTrafficPortEnd="" \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      Domain=<YOUR_VALHEIM_SUBDOMAIN>
```

Notes:

- `GameServer` has no default and must always be supplied - point it at [`scripts/Valheim/install.sh`](../scripts/Valheim/install.sh) as published to your own S3 bucket by `CdkAssetPublisher` (its `ScriptsBaseUrl` output plus `/Valheim/install.sh`). CloudFormation never fetches this from GitHub.
- `Domain` is optional - pass `Domain=""` if you don't want a custom DNS name for this server (you'll get a fresh IP every time you stop/start it instead).
- `HostedZoneId` must match the value used on your Control Panel stack.
- `GamingUDPTrafficPortStart`/`GamingUDPTrafficPortEnd` are omitted - they default to `2456`/`2458`, which is what Valheim needs.
- The stack waits for the install to actually finish before reporting success - `aws cloudformation deploy` can take several minutes and will fail with a real error if the install fails, rather than reporting success regardless.
- After the stack finishes, the instance installs Valheim on first boot and then **shuts itself down**. Start it from the control panel when you're ready to play - that's also what triggers the DNS record to be created/updated, so every server's first-ever appearance online goes through the same start flow (rather than the record lagging behind an instance that was already running).
- The join password is generated once and stored in SSM Parameter Store (`game-password-<stack name>`, SecureString) - reinstalling the server (e.g. deleting and redeploying this stack) reuses it rather than generating a new one, since that parameter isn't owned by this stack and survives its deletion. Retrieve it with `aws ssm get-parameter --name game-password-<stack name> --with-decryption --query Parameter.Value --output text`.
- The install also sets up a `valheim-status-agent` systemd service that reports player count, version, and mods to the control panel every 5 seconds (only pushing on change) - see [`docs/server-status.md`](server-status.md). It shells into the game's own docker container (`docker compose exec valheim odin status --json`) rather than parsing logs, since Odin (the docker image's launcher) already exposes this directly.
- Valheim runs via `docker compose` (see [`scripts/Valheim/install.sh`](../scripts/Valheim/install.sh) for the generated `docker-compose.yml`) - mods (e.g. ValheimPlus via BepInEx) are configured through that file's `environment` section (`TYPE=BepInEx` and a `MODS=` list of Thunderstore package identifiers or direct URLs), not by uploading files. See [mbround18/valheim-docker's mod docs](https://mbround18.github.io/valheim-docker/tutorials/getting_started_with_mods.html) for the exact syntax - environment variable names are case-sensitive (`TYPE`, not `Type`).

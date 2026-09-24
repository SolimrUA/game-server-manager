# Deploying a Valheim server

Requires the Common stack, [published assets](../README.md#2-publish-your-assets---once-per-account-region), and the Control Panel stack already deployed - see the root [README.md](../README.md#deploying).

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
      DataPath=/usr/games/serverconfig/valheim \
      WorldDownloadPaths=saves,backups \
      WorldDownloadCorePaths=saves \
      KeyName=<YOUR_EC2_KEY_PAIR> \
      InstanceType=t3a.medium \
      GamingTCPTrafficPortStart="" \
      GamingTCPTrafficPortEnd="" \
      HostedZoneId=<YOUR_HOSTED_ZONE_ID> \
      Domain=<YOUR_VALHEIM_SUBDOMAIN>
```

Notes:

- Valheim needs UDP 2456-2458 and no TCP, which is what the template defaults to, so the UDP parameters are omitted and the TCP pair set empty.
- `GameVersion` is omitted: Valheim's docker image updates itself daily, so a pinned version means nothing to it.
- A `valheim-status-agent` systemd service reports player count, version, and mods to the control panel (see [`docs/server-status.md`](server-status.md)) by shelling into the game's docker container: `docker compose exec valheim odin status --json`.
- Valheim runs via `docker compose` (see [`scripts/Valheim/install.sh`](../scripts/Valheim/install.sh) for the generated `docker-compose.yml`) - mods (e.g. ValheimPlus via BepInEx) are configured through that file's `environment` section (`TYPE=BepInEx` and a `MODS=` list of Thunderstore package identifiers or direct URLs), not by uploading files. See [mbround18/valheim-docker's mod docs](https://mbround18.github.io/valheim-docker/tutorials/getting_started_with_mods.html) for the exact syntax - environment variable names are case-sensitive (`TYPE`, not `Type`).

# Valheim servers

Create one from the control panel's **New server** form, picking Valheim - see the root
[README.md](../README.md#5-create-a-server).

- Valheim needs UDP 2456-2458 and no TCP. The Fleet stack opens exactly that for every Valheim server.
- There is no version to pick: Valheim's docker image updates itself daily.
- A `valheim-status-agent` systemd service reports player count, version, and mods to the control panel
  (see [`docs/server-status.md`](server-status.md)) by shelling into the game's docker container:
  `docker compose exec valheim odin status --json`.
- Valheim runs via `docker compose` (see [`scripts/Valheim/install.sh`](../scripts/Valheim/install.sh)
  for the generated `docker-compose.yml`). Mods such as ValheimPlus are configured through that file's
  `environment` section (`TYPE=BepInEx` and a `MODS=` list of Thunderstore package identifiers or
  direct URLs), not by uploading files. See
  [mbround18/valheim-docker's mod docs](https://mbround18.github.io/valheim-docker/tutorials/getting_started_with_mods.html)
  for the exact syntax - environment variable names are case-sensitive (`TYPE`, not `Type`).

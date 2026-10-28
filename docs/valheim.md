# Valheim servers

Create one from the control panel's **New server** form, picking Valheim - see the root
[README.md](../README.md#5-create-a-server).

- Valheim needs UDP 2456-2458 and no TCP. The Fleet stack opens exactly that for every Valheim server.
- There is no version to pick: Valheim's docker image updates itself daily.
- A `valheim-status-agent` systemd service reports player count, version, and mods to the control panel
  (see [`docs/server-status.md`](server-status.md)) by shelling into the game's docker container:
  `docker compose exec valheim odin status --json`.
- A `valheim-config.service` systemd unit sets `PASSWORD` and `NAME` in `docker-compose.yml` from what
  the control panel holds, before the compose unit starts. There is no `NAME` entry until someone names
  the server, which leaves the image's own default. A password or name that didn't take effect is this
  unit's journal to check.
- Valheim runs via `docker compose` (see [`scripts/Valheim/install.sh`](../scripts/Valheim/install.sh)
  for the generated `docker-compose.yml`). Mods such as ValheimPlus are configured through that file's
  `environment` section (`TYPE=BepInEx` and a `MODS=` list of Thunderstore package identifiers or
  direct URLs), not by uploading files. See
  [mbround18/valheim-docker's mod docs](https://mbround18.github.io/valheim-docker/tutorials/getting_started_with_mods.html)
  for the exact syntax - environment variable names are case-sensitive (`TYPE`, not `Type`).
- Removing a mod from that `MODS` list is what uninstalls it: Odin reconciles the installed files
  against the list and deletes whatever is no longer named. The image only reconciles on startup when
  `MODS` is non-empty, so the control panel runs the reconcile itself after editing the list - without
  that, removing the last mod would leave it installed and still loaded.

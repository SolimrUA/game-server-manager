# Vintage Story servers

Create one from the control panel's **New server** form, picking Vintage Story - see the root
[README.md](../README.md#5-create-a-server).

- Vintage Story needs TCP and UDP on port 42420. The Fleet stack opens exactly that for every Vintage
  Story server.
- The version is required, and is the exact version installed - see
  [the download page](https://account.vintagestory.at/downloads) for what's current.
- A `vintagestory-status-agent` systemd service reports player count, version, mods, and the game's own
  last-autosave time to the control panel (see [`docs/server-status.md`](server-status.md)). Those come
  from the game's `/stats` console command, so if the player count looks wrong, check the regex in
  [`status_agent.py`](../scripts/VintageStory/status_agent.py) against what `/stats` prints on your
  server.
- Run `server.sh` commands as the `vintagestory` user
  (`sudo -u vintagestory /home/vintagestory/server/server.sh restart`), not as `ubuntu`: `screen` scopes
  sessions per-user, so another user won't find the session systemd created, group membership
  notwithstanding.

## Getting a world onto a server

The data volume holds the world under `Saves/` and mods under `Mods/`, at
`/home/vintagestory/data`. Mods are installed and removed from the control panel. To bring an existing
world across, create the server from the snapshot of the server that held it, under **Restore from** -
the install script keeps whatever data it finds on the volume rather than overwriting it.

Which save the server loads is set in `serverconfig.json` in that same folder; check Vintage Story's own
server documentation for the current field name, which has changed across versions.

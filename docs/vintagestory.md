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
- A `vintagestory-config.service` systemd unit sets `Password`, `ServerName` and, when one has been
  asked for, the new world's settings in `serverconfig.json` from what the control panel holds, before
  the game starts. A password, name or world that didn't take effect is this unit's journal to check.
- The **World** tab generates a new world on the next game start. Its presets are the files in
  [`worlds/vintagestory/`](../Lambda/go/internal/games/worlds/vintagestory), each named after its
  file, and adding one is adding a file. A preset holds only what it changes, because the instance
  merges it over the config the server already has.
- `vsWorldKeys` names what a preset is allowed to write. `WorldConfiguration` is named as a whole, so
  every world option inside it reaches the game, down to the ones a later release adds; a setting
  beside it has to be named. The join password, port, address and save path are not named, so nothing
  in a preset or typed into **Customise** can reach them. The world's name and seed come from the
  form's own fields. The save file is named after the world, and the world the server was on stays on
  the volume, though the control panel has no way back to it.
- **Player Role** under Actions runs `/player <name> role <code>` on the running server, which is how
  a player is made an in-game admin. The role codes are the ones in the server's own `Roles`, and the
  game has to be up because the command goes through its console.
- A mod is installed for a game version, and the control panel sends the one the server reports. A mod
  whose newest release lists only older versions is installed by naming one of those in the game
  version field beside the mod id.
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

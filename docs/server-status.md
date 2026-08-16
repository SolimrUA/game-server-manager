# Server status data (players, mods, version)

Each Server stack's EC2 instance is responsible for reporting its own live game state - current/max players,
game version, and installed mods - by writing a small JSON file to the shared status bucket
(`common-infra-status-bucket-name`, created in [`cfn/common-infra.yaml`](../cfn/common-infra.yaml)).

## Where it's written

`s3://<StatusBucketName>/status/<GameName>.json`, where `<GameName>` is that server stack's `GameName` parameter
(e.g. `vintagestory`, `valheim`). This is why `GameName` must be unique per server stack: it names the EC2 instance
and doubles as the status object's key.

The EC2 instance role (`GamingServerIamRole` in `cfn/server-stack.yaml`) is granted `s3:PutObject` scoped to
exactly that one key. Nothing else in the bucket is writable by the instance.

## Who writes it

A per-game "status agent" process, installed by that game's cartridge script (e.g.
[`scripts/VintageStory/install.sh`](../scripts/VintageStory/install.sh), which fetches its companion
[`status_agent.py`](../scripts/VintageStory/status_agent.py) from alongside itself and runs it as a systemd service),
running alongside the actual game server. It re-checks state on a short interval and only re-uploads the JSON when
something actually changed, to keep S3 costs and API calls minimal. What "re-checks state" means is inherently
game-specific - Vintage Story's agent asks the game process directly (via its console, see below) rather than
guessing from logs, since that's what the game's own tooling does too.

## Sending the game server console commands

Vintage Story has no network API - the only way to control a running server is through its console, and the only
way to reach that console on a headless box is `server.sh`'s `command` action. `server.sh` is the game's own
launcher script, bundled inside its server download (not something this repo ships) and extracted to
`/home/vintagestory/server/server.sh` alongside the game binary; it runs the server inside a detached `screen`
session and can inject text into that session's stdin on request. `status_agent.py` uses this to run `/stats` for
the player count; the same mechanism works for any other server console command, e.g.:

```bash
sudo -u vintagestory /home/vintagestory/server/server.sh command "announce Restarting in 5 minutes"
```

## Who reads it

The Control Panel's `getinfo` Lambda (`Lambda/gaming_server_start_stop-v1_0.py`) reads the object for each running
instance (using the instance's `game-name` tag to build the key) and merges it into the `getinfo` API response as
`GameStatus`. The front-end (`FrontEnd/js/index.js`) reads `GameStatus` straight off that response - it never talks
to S3 directly.

## Schema

```json
{
  "gameName": "vintagestory",
  "version": "1.22.6",
  "serviceStatus": "active",
  "players": {
    "current": 2,
    "max": 16
  },
  "mods": [
    { "name": "primitivesurvival", "version": "3.5.0" },
    { "name": "wildcraft", "version": null }
  ],
  "updatedAt": "2026-08-15T12:34:56Z"
}
```

- `serviceStatus` - whether the game process itself is actually up: `active`, `activating`, `inactive`,
  `deactivating`, `failed`, or `unknown` (the values `systemctl is-active` uses, though an agent can derive this any
  way that's accurate for that game - Vintage Story's checks for the live process directly rather than trusting
  systemd, since its process supervisor is a detached `screen` session systemd doesn't track precisely). This is
  deliberately separate from the EC2 instance's own running/stopped state, which the front-end gets straight from
  `ec2:DescribeInstances`, not from this file: an instance can be `running` while the game is still starting up,
  crash-looping (`failed`), or not installed yet. The front-end shows both, as separate State and Game columns, so
  "server started but the game didn't come up" reads differently from "not started yet".
- `players.current` / `players.max` - integers. `max` may be `null` if the agent couldn't determine it.
- `mods` - array of `{name, version}`. `version` is `null` when it couldn't be determined (e.g. a mod without a
  parseable manifest). Empty array if the game has no mod support or none are installed.
- `updatedAt` - UTC ISO-8601 timestamp of when the agent last computed this snapshot (not necessarily when it was
  last *pushed*, since unchanged snapshots aren't re-uploaded).

Any field can be `null`/omitted if a given game's agent can't determine it - the front-end renders `—` for missing
data rather than erroring.

## Adding status reporting for another game

1. Put the game's cartridge script in its own folder under `scripts/` (e.g. `scripts/VintageStory/`) alongside a status
   agent script, following the same split used for Vintage Story. The server stack invokes the cartridge script as
   `./install.sh "$GameServer"`, so `$1` is the script's own source URL - use it to fetch sibling files (like the
   status agent) from wherever `install.sh` itself was served from. In practice this is always your S3-published
   copy (see the root README) - `GameServer` has no default and CloudFormation never fetches from GitHub.
2. Have that status agent write this JSON shape to the path above on an interval, pushing only on change - including
   `serviceStatus` from whatever process supervisor runs the game (e.g. `systemctl is-active <unit>` for a systemd
   service), so the front-end can distinguish "EC2 running, game not up yet" from "both running".
3. Nothing else needs to change - the S3 key convention, IAM policy, and Lambda/front-end read path are already
   game-agnostic.

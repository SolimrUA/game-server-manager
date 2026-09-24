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

A per-game "status agent" process, installed as a systemd service by that game's cartridge script, which fetches
it from alongside itself. It re-checks state on a short interval and re-uploads only when something changed. How it
re-checks is game-specific: [Vintage Story's](../scripts/VintageStory/status_agent.py) asks the game through its
console (see below), [Valheim's](../scripts/Valheim/status_agent.py) shells into the docker container, where Odin
already exposes all of it.

## Sending the Vintage Story server console commands

Vintage Story has no network API, so the only way to control a running server is its console - reachable on a
headless box through `server.sh`'s `command` action. `server.sh` is the game's own launcher, bundled in its server
download and extracted to `/home/vintagestory/server/server.sh`; it runs the server inside a detached `screen`
session and injects text into that session's stdin on request. `status_agent.py` runs `/stats` this way, and any
other console command works the same:

```bash
sudo -u vintagestory /home/vintagestory/server/server.sh command "announce Restarting in 5 minutes"
```

## Who reads it

The Control Panel's `getinfo` Lambda (`Lambda/gaming_server_start_stop-v1_0.py`) reads the object for each running
instance (using the instance's `game-name` tag to build the key) and merges it into the `getinfo` API response as
`GameStatus`. The front-end (`FrontEnd/js/index.js`) reads `GameStatus` straight off that response - it never talks
to S3 directly. One field, `serverName`, is also promoted to a top-level `ServerName` on the instance object,
where the front-end's Server Name column expects it.

## Schema

```json
{
  "gameName": "vintagestory",
  "serverName": "Geeks Are Here",
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
  "lastSaveTime": "2026-08-15T12:30:00Z",
  "updatedAt": "2026-08-15T12:34:56Z"
}
```

- `serverName` - the display name players see when connecting/browsing for the server (Vintage Story:
  `serverconfig.json`'s `ServerName`; Valheim: the `NAME` env var, read back via `odin status`'s own `name`
  field). `null` if blank/unset. Promoted to the top-level `ServerName` field - see above.
- `serviceStatus` - whether the game process is up: `active`, `activating`, `inactive`, `deactivating`, `failed`,
  or `unknown` (the values `systemctl is-active` uses, though an agent may derive them any way that's accurate for
  its game). Separate from the EC2 instance's own running/stopped state, which the control panel reads from
  `ec2:DescribeInstances`: an instance can be `running` while the game is still starting, crash-looping, or not
  installed.
- `players.current` / `players.max` - integers. `max` may be `null` if the agent couldn't determine it.
- `mods` - array of `{name, version}`. `version` is `null` when it couldn't be determined (e.g. a mod without a
  parseable manifest). Empty array if the game has no mod support or none are installed.
- `lastSaveTime` - UTC ISO-8601 timestamp of when the game itself last wrote its world data. Distinct from the
  Control Panel's "Last Backup" column (`LastBackupTime`), which comes from AWS Backup's EBS snapshot.
- `updatedAt` - UTC ISO-8601 timestamp of when the agent last computed this snapshot (not necessarily when it was
  last *pushed*, since unchanged snapshots aren't re-uploaded).

Any field can be `null`/omitted if a given game's agent can't determine it - the front-end renders `—` for missing
data rather than erroring.

## Adding status reporting for another game

Vintage Story and Valheim implement this pattern two different ways - game console vs. shelling into a docker
container. For a third game:

1. Put the game's cartridge script in its own folder under `scripts/`, alongside a status agent script. The server
   stack invokes the cartridge as `./install.sh "$GameServer"`, so `$1` is the script's own source URL - use it to
   fetch the status agent from wherever `install.sh` itself was served from.
2. Have that agent write this JSON shape to the path above on an interval, pushing only on change - including
   `serviceStatus` from whatever supervises the game process, so "EC2 running, game not up yet" is distinguishable
   from "both running".
3. Have the setup script write any values the agent needs (bucket name, status key) under
   `/etc/game-server-manager/`, not `/tmp`, which this AMI clears on every reboot - the agent re-reads them on
   every restart, so a `/tmp` value is there on first boot and gone on every one after.
4. Nothing else needs to change - the S3 key convention, IAM policy, and Lambda/front-end read path are already
   game-agnostic.

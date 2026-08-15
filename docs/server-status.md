# Server status data (players, mods, version)

Each Server stack's EC2 instance is responsible for reporting its own live game state - current/max players,
game version, and installed mods - by writing a small JSON file to the shared status bucket
(`common-infra-status-bucket-name`, created in [`cfn/common-infra.yaml`](../cfn/common-infra.yaml)).

## Where it's written

`s3://<StatusBucketName>/status/<GameName>.json`, where `<GameName>` is that server stack's `GameName` parameter
(e.g. `vintagestory`, `valheim`). This is why `GameName` must be unique per server stack - it's already used to
name the EC2 instance and now also doubles as the status object's key.

The EC2 instance role (`GamingServerIamRole` in `cfn/server-stack.yaml`) is granted `s3:PutObject` scoped to
exactly that one key. Nothing else in the bucket is writable by the instance.

## Who writes it

A per-game "status agent" process, installed by that game's cartridge script (e.g.
[`Bash/vintagestory.sh`](../Bash/vintagestory.sh)), running alongside the actual game server. It re-checks state
(player count via log tailing, mods via folder inspection, version from the pinned install version) on a short
interval and only re-uploads the JSON when something actually changed, to keep S3 costs and API calls minimal.

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

- `players.current` / `players.max` - integers. `max` may be `null` if the agent couldn't determine it.
- `mods` - array of `{name, version}`. `version` is `null` when it couldn't be determined (e.g. a mod without a
  parseable manifest). Empty array if the game has no mod support or none are installed.
- `updatedAt` - UTC ISO-8601 timestamp of when the agent last computed this snapshot (not necessarily when it was
  last *pushed*, since unchanged snapshots aren't re-uploaded).

Any field can be `null`/omitted if a given game's agent can't determine it - the front-end renders `—` for missing
data rather than erroring.

## Adding status reporting for another game

1. Have the cartridge script drop a status agent that writes this JSON shape to the path above on an interval,
   pushing only on change.
2. Nothing else needs to change - the S3 key convention, IAM policy, and Lambda/front-end read path are already
   game-agnostic.

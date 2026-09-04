# Game Servers page: front-end data requirements

The Game Servers table (`FrontEnd/index.html` + `FrontEnd/js/index.js`) was redesigned to group actions
into an Overview/Actions/Mods panel and to show one combined "State" per server instead of separate
State/Game Status/Auto-Shutdown columns. This documents what the front-end now expects from `getinfo`,
and which parts are purely a front-end reshuffle that need nothing from the back-end.

## No back-end changes required for most of this

The redesign is a front-end restructuring of fields the `getinfo` Lambda (`Lambda/gaming_server_start_stop-v1_0.py`)
already returns - `State`, `GameName`, `DomainName`, `PublicIpAddress`, `InstanceType`, `LastBackupTime`,
`IdleShutdownStatus`, `UserLifecycleAllowed`, `UserDownloadsAllowed`, and `GameStatus` (see
[`docs/server-status.md`](./server-status.md) for that shape). The Start/Stop, Resize, Backup Now, Download
World, and Pause/Resume Auto-Shutdown buttons - whether shown as quick actions in the table row or repeated
in the Actions tab - all call the same existing endpoints with the same parameters as before. No new
endpoints, no new Lambda commands.

## New field needed: Server Name

The table has a "Server Name" column and the Overview tab has a matching field. Neither the front-end nor
`getinfo` has this data yet - it's rendered as a placeholder dash (`—`) for every instance until it exists.

When it's ready, the front-end expects a top-level string on each instance object in the `getinfo` response,
following the existing `PascalCase` convention (`State`, `GameName`, `InstanceType`, ...) - suggest
`ServerName`. Treat it as optional/nullable the same way other fields are: the front-end already renders `—`
for anything missing, so a partial rollout (some instances have it, some don't) needs no special handling.

## Combined "State" column - computed client-side, no back-end change

The table used to show State (EC2), Game Status, and Auto-Shutdown as three separate columns. They're now
folded into one State badge per row (`mcCombinedState()` in `FrontEnd/js/index.js`), computed entirely from
fields `getinfo` already returns - EC2's `State`, `GameStatus.serviceStatus`, and `IdleShutdownStatus`. The
full underlying breakdown (raw EC2 State, Game State, Auto-Shutdown) still appears in the Overview tab for
anyone who wants it. The mapping, for reference:

| EC2 `State`                          | `GameStatus.serviceStatus`   | `IdleShutdownStatus`  | Combined label      |
|---------------------------------------|-------------------------------|------------------------|----------------------|
| `stopped`                             | -                              | -                       | `Stopped`            |
| `pending`                             | -                              | -                       | `Starting (EC2)`     |
| `stopping` / `shutting-down`          | -                              | -                       | `Stopping (EC2)`     |
| `running`                             | `failed`                       | -                       | `Error`              |
| `running`                             | `inactive`                     | -                       | `Stopped (Game)`     |
| `running`                             | anything else not `active`     | -                       | `Starting (Game)`    |
| `running`                             | `active`                       | `triggered-once`        | `Running (Idle)`     |
| `running`                             | `active`                       | anything else            | `Running`            |

Auto-shutdown merely being *enabled* (as opposed to actually having recorded an idle check) doesn't surface
in this combined label - only `triggered-once` does, since that's the point at which the next idle check
will actually stop the instance. `IdleShutdownStatus` on its own (`Paused`/`Active`/`Active (Idle)`) is still
shown as-is in the Overview tab's Auto-Shutdown field.

One gap worth knowing about: there's no numeric "time until shutdown" today - `IdleShutdownStatus` is a
3-value enum, not a countdown. If a future revision wants to show something like "18 minutes until shutdown",
that needs a new field (e.g. a timestamp for when the idle check will next fire), not just enum values.

## Resize dropdown now shows real EC2 types - the value sent is unchanged

The Capacity dropdown now *labels* its options with real instance types (`t3a.micro`, `t3a.small`,
`t3a.medium`, `t3a.large`) instead of the old friendly names (Micro/Small/Medium/Large). This is display-only:
the `<option>` **value** submitted to `resize/...&resize=<value>` is still the old lowercase slug
(`micro`/`small`/`medium`/`large`), unchanged.

This was deliberate, not an oversight - the resize Lambda doesn't accept an EC2 instance type string
directly. It uses the value as an environment-variable key:

```python
InstanceType={'Value': os.environ[event['reSizeType']]},
```

and those env vars are a fixed micro/small/medium/large → t3a.* map (`cfn/control-panel.yaml`, `StartStopLambda`
→ `Environment.Variables`). Sending anything other than one of those four slugs would throw a `KeyError`.

Two things worth knowing if the back-end evolves this:

- **`xlarge`/`2xlarge` aren't offered in the dropdown**, even though the *initial* server-creation form
  (`cfn/server-stack.yaml`'s `InstanceType` parameter) allows them. The resize Lambda has no env var for
  them, so they'd fail today. If you want resize to support them, add `xlarge`/`2xlarge` entries to that
  `Environment.Variables` block (mapping to `t3a.xlarge`/`t3a.2xlarge`) and the front-end's
  `MC_RESIZE_OPTIONS` list can be extended to match.
- The env var map is global to the Lambda, not per-instance-family. A comment in the Lambda notes ARM
  (T3g) instances can't resize to x86 (T3a) types - if a T3g-based server stack is ever added, resize would
  need to key off the instance's actual family rather than always resolving to `t3a.*`.

## Permission model - unchanged

`UserLifecycleAllowed` and `UserDownloadsAllowed` (per-instance tags, read in `getInfo()`) still gate the
Start/Stop and Download World buttons exactly as before, now applied consistently to both the table's quick
action and the Actions tab's duplicate of the same button. Resize, Backup Now, and Pause/Resume Auto-Shutdown
remain admin-only (`mcIsAdmin`, from the caller's `cognito:groups` claim) - no per-user override for those.

# Who can do what

Every Control Panel action is authorised per user, per server. Admins are a Cognito group; everyone else
holds an access template on each server they can reach, assigned from the Control Panel's Access page.

## Templates

A template names a set of capabilities. Templates are defined in
[`Lambda/go/internal/access/access.go`](../Lambda/go/internal/access/access.go), so changing one changes
it for everyone holding it.

| Template | Capabilities |
|---|---|
| Player | `view`, `lifecycle`, `download` |
| Owner | `view`, `lifecycle`, `download`, `content`, `backup` |
| Admin (the `admins` Cognito group) | all of the above, plus `autoshutdown` and `resize` |

Admin is a group rather than a template: it applies to every server at once and can't be assigned per
server. `autoshutdown` and `resize` are admin-only because they change what the server costs to run.

| Capability | Commands |
|---|---|
| `view` | `getInfo` |
| `lifecycle` | `start`, `stop` |
| `download` | `startWorldDownload`, `getWorldDownloadStatus` |
| `content` | `installMod`, `deleteMod`, `restartGame`, `updateGame` |
| `backup` | `backupNow` |
| `autoshutdown` | `pauseIdleShutdown`, `resumeIdleShutdown` |
| `resize` | `reSize` |

## Where assignments live

One DynamoDB item per user per server, in the table the Control Panel stack creates
([`cdk/controlpanel.go`](../cdk/controlpanel.go)): partition key `userSub`, sort key `instanceId`, and a
`role` attribute holding the template name. No item means no access, so revoking deletes the item and
the table only ever holds real grants.

Users themselves are only ever in Cognito. The table holds no copy of them beyond an `email` on each item
for readability, and a user's display name is Cognito's own `name` attribute - a standard optional
attribute, which is why storing one needs no change to the pool's schema.

## How a request is authorised

`getInfo` resolves the caller's template on every tagged instance in one query, drops the instances where
that yields no `view`, and returns the resolved capabilities on each instance it keeps. Every other
command then checks the capability its name maps to (`CAPABILITY_BY_COMMAND`) against the target
instance's capabilities.

Dropping unviewable instances from `getInfo` is what makes the rest safe: a caller who can't see a server
can't name its instance id either, so a command aimed at it is refused before any capability is consulted.

The front end reads those capabilities straight off the `getInfo` response (`mcCan()` in
[`FrontEnd/js/index.js`](../FrontEnd/js/index.js)) to decide which controls to render, and never learns
what a template contains. It is not a security boundary - the Lambda re-checks every request.

## Adding a template

Add it to `CAPABILITIES_BY_TEMPLATE`, and to `MC_TEMPLATES`/`MC_TEMPLATE_LABELS` in
[`FrontEnd/js/access.js`](../FrontEnd/js/access.js) so it appears in each cell's dropdown. Stored
assignments need no migration: the item already holds the template's name as a string.

# Who can do what

Every Control Panel action is authorised per user, per server. Admins are a Cognito group; everyone else
holds an access template on each server they can reach, assigned from the Control Panel's Access page
or by the defaults when they first sign in.

## Templates

A template names a set of capabilities. Templates are defined in
[`Lambda/go/internal/access/access.go`](../Lambda/go/internal/access/access.go), so changing one changes
it for everyone holding it.

| Template | Capabilities |
|---|---|
| Player | `view`, `lifecycle`, `download` |
| Owner | `view`, `lifecycle`, `download`, `content`, `backup`, `password` |
| Admin (the `admins` Cognito group) | all of the above, plus `autoshutdown` and `resize` |

Admin is a group rather than a template: it applies to every server at once and can't be assigned per
server. `autoshutdown` and `resize` are admin-only because they change what the server costs to run.

| Capability | Commands |
|---|---|
| `view` | `getInfo` |
| `lifecycle` | `start`, `stop` |
| `download` | `startWorldDownload`, `getWorldDownloadStatus` |
| `content` | `installMod`, `deleteMod`, `restartGame`, `updateGame`, `setServerName`, `getWorldDefaults`, `newWorld`, `setPlayerRole` |
| `backup` | `backupNow` |
| `password` | `getGamePassword`, `setGamePassword` |
| `autoshutdown` | `pauseIdleShutdown`, `resumeIdleShutdown` |
| `resize` | `reSize` |

The Backups page is admin-only and has no template: `getWorlds`, `setBackupExpiry`, `setBackupPrimary`, `deleteBackup`,
`deleteVolumeBackups`, `useVolume`, `retireVolume` and `deleteVolume`.

## Where assignments live

One DynamoDB item per user per server, in the table the Control Panel stack creates
([`cdk/controlpanel.go`](../cdk/controlpanel.go)): partition key `userSub`, sort key `serverId`, and a
`role` attribute holding the template name. An empty `role` grants nothing, and revoking writes one
rather than deleting the item, so a user with no items at all is one nobody has decided anything
about.

Deleting a server removes every grant on it.

Users themselves are only ever in Cognito. The table holds no copy of them beyond an `email` on each item
for readability, and a user's display name is Cognito's own `name` attribute - a standard optional
attribute, which is why storing one needs no change to the pool's schema.

## What a new user starts with

The first request from a user with no items at all applies the defaults: a template on every server,
and the permission to create one. They are written as ordinary grants, once, so a user who is taken
off a server afterwards stays off it.

Admins set them on the Access page, stored under the partition `#defaults`. A default is Player or
nothing. Changing it leaves everyone already granted as they are; **↑ Player** and **↓ None** on each
row are what move an existing user.

## Creating a server

Creating a server is an admin's to do, except that an admin can tick **May create** against a user on
the Access page. That lets them create one server: creating it spends the permission and makes them its
Owner. A new server also takes the defaults for everyone who already holds access to every other
server. Deleting stays with admins, so the server outlives the permit until an admin removes it.

The permission is an item in the same table under the sort key `#create-permit`, with a `canCreate`
attribute. It is spent with a conditional update, so two requests at once cannot both spend the same
one.

Such a user sees the **Restore from** list narrowed to servers they already hold access to, because
restoring a backup copies that world onto a server they would then own.

## How a request is authorised

`getInfo` resolves the caller's template on every tagged instance in one query, drops the instances where
that yields no `view`, and returns the resolved capabilities on each instance it keeps. Every other
command then checks the capability it names (the `needs` field of its entry in `commands`) against the
target instance's capabilities.

Dropping unviewable instances from `getInfo` is what makes the rest safe: a caller who can't see a server
can't name its instance id either, so a command aimed at it is refused before any capability is consulted.

The front end reads those capabilities straight off the `getInfo` response (`mcCan()` in
[`FrontEnd/js/index.js`](../FrontEnd/js/index.js)) to decide which controls to render, and never learns
what a template contains. It is not a security boundary - the Lambda re-checks every request.

## Adding a template

Add it to the `templates` map, and to `MC_TEMPLATES`/`MC_TEMPLATE_LABELS` in
[`FrontEnd/js/access.js`](../FrontEnd/js/access.js) so it appears in each cell's dropdown.
`MC_DEFAULT_TEMPLATES` is the shorter list of what may be handed to a new user. Stored assignments need
no migration: the item already holds the template's name as a string.

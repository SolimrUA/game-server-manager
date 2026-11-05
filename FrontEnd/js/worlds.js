// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

var mcWorlds = [];

function mcWorldExpiry(backup) {
  if (!backup.expiresAt) return '<span class="mcWorldKept">Current</span>';
  return '<span class="mcMuted">' + escapeHtml(mcWorldDate(backup.expiresAt)) + '</span>';
}

function mcWorldDate(iso) {
  if (!iso) return '';
  return iso.replace('T', ' ').replace(/:\d\d(\.\d+)?Z?$/, '').replace('Z', '');
}

function mcWorldVolumeState(world) {
  if (world.state === 'deleted') return 'volume deleted, backups kept';
  if (world.instanceId) return 'in use by ' + world.instanceId;
  if (world.backedUp) return 'live world, not attached';
  return 'retired';
}

//The Lambda checks these again, and the availability zone besides.
function mcWorldCanUse(world) {
  return world.state !== 'deleted' && !world.instanceId && world.serverState === 'stopped';
}

function mcWorldStatus(backup) {
  if (backup.deleteRequested) return '<span class="mcMuted">Deleting</span>';
  if (backup.primary) return 'Primary';
  return '<span class="mcMuted">Defunct</span>';
}

//When every backup is already on its way out, the delete button offers a repeat of the request.
function mcWorldDeleteAsked(world) {
  if (!world.backups.length) return '';
  var asked = '';
  for (var i = 0; i < world.backups.length; i++) {
    if (!world.backups[i].deleteRequested) return '';
    if (world.backups[i].deleteRequested > asked) asked = world.backups[i].deleteRequested;
  }
  return asked;
}

function mcWorldBackupRow(backup) {
  var going = !!backup.deleteRequested;
  //A backup already handed to the vault shows when it was asked for, the only sign there is until
  //it goes.
  //Retry, because the vault can accept a delete and never carry it out.
  var actions = going
    ? '<span class="mcMuted">asked ' + escapeHtml(mcWorldDate(backup.deleteRequested)) + '</span>' +
      '<button type="button" class="btn danger mcWorldDeleteBackup" data-snap="' +
        escapeHtml(backup.snapshotId) + '">Retry</button>'
    : '<button type="button" class="btn mcWorldStatusBtn" data-snap="' + escapeHtml(backup.snapshotId) +
        '" data-primary="' + (backup.primary ? '0' : '1') + '">' +
        (backup.primary ? 'Make defunct' : 'Make primary') + '</button>' +
      '<button type="button" class="btn mcWorldKeep" data-snap="' + escapeHtml(backup.snapshotId) +
        '">Keep</button>' +
      '<button type="button" class="btn mcWorldExpire" data-snap="' + escapeHtml(backup.snapshotId) +
        '">Expire...</button>' +
      '<button type="button" class="btn danger mcWorldDeleteBackup" data-snap="' +
        escapeHtml(backup.snapshotId) + '"' + (backup.primary ? ' disabled' : '') +
        ' title="' + (backup.primary ? 'Primary backups cannot be deleted' : 'Delete this backup') +
        '">Delete</button>';
  return '<tr' + (going ? ' class="mcWorldGoing"' : '') + '>' +
    '<td>' + escapeHtml(mcWorldDate(backup.startTime)) + '</td>' +
    '<td class="mcMuted">' + escapeHtml(backup.snapshotId) + '</td>' +
    '<td>' + backup.sizeGib + ' GiB</td>' +
    '<td>' + mcWorldExpiry(backup) + '</td>' +
    '<td>' + mcWorldStatus(backup) + '</td>' +
    '<td class="mcWorldActions">' + actions + '</td>' +
    '</tr>';
}

function mcWorldBlock(world) {
  world.backups = world.backups || [];
  var rows = world.backups.map(mcWorldBackupRow).join('');
  if (!rows) rows = '<tr><td colspan="6" class="mcMuted">No backups yet</td></tr>';

  //A volume has to stop being backed up before the disk can go: while it is, a server is using it.
  var detached = world.state !== 'deleted' && !world.instanceId;
  var canRetire = detached && world.backedUp;
  var canDelete = detached && !world.backedUp;
  var volumeLabel = world.volumeId
    ? escapeHtml(world.volumeId) : '<span class="mcMuted">volume gone</span>';

  return '<div class="mcWorldVolume">' +
    '<div class="mcWorldVolumeHead">' +
      '<span class="mcWorldVolumeId">' + volumeLabel + '</span>' +
      '<span class="mcMuted">' + world.sizeGib + ' GiB &middot; ' +
        escapeHtml(mcWorldVolumeState(world)) +
        (world.created ? ' &middot; created ' + escapeHtml(mcWorldDate(world.created)) : '') +
        '</span>' +
      '<span class="mcWorldVolumeActions">' +
        (mcWorldCanUse(world) ? '<button type="button" class="btn primary mcWorldUse" data-volume="' +
          escapeHtml(world.volumeId) + '">Mount to Server</button>' : '') +
        (canRetire ? '<button type="button" class="btn mcWorldRetire" data-volume="' +
          escapeHtml(world.volumeId) + '">Stop backups</button>' : '') +
        (canDelete ? '<button type="button" class="btn mcWorldDeleteVolume" data-volume="' +
          escapeHtml(world.volumeId) + '">Delete volume</button>' : '') +
        mcWorldDeleteButton(world) +
      '</span>' +
    '</div>' +
    '<table class="mcServerTable mcWorldTable">' +
      '<thead><tr><th>Created</th><th>Id</th><th>Size</th><th>Expires</th>' +
      '<th>Status</th><th>Actions</th></tr></thead>' +
      '<tbody>' + rows + '</tbody>' +
    '</table>' +
    '</div>';
}

function mcWorldDeleteButton(world) {
  if (!world.backups.length) return '';
  //Relabelled rather than disabled: a delete the vault dropped is only recovered by asking again.
  var asked = mcWorldDeleteAsked(world);
  return '<button type="button" class="btn danger mcWorldDeleteBackups" data-volume="' +
    escapeHtml(world.volumeId) + '" data-server="' + escapeHtml(world.serverId) + '">' +
    (asked ? 'Retry deleting backups' : 'Delete all backups') + '</button>';
}

function renderWorlds() {
  var host = document.getElementById('mcWorlds');
  if (!host) return;
  if (!mcWorlds.length) {
    host.innerHTML = '<p class="mcMuted">No worlds are backed up yet.</p>';
    return;
  }

  var html = '';
  var server = null;
  mcWorlds.forEach(function (world) {
    if (world.serverId !== server) {
      server = world.serverId;
      html += '<h2 class="mcWorldServer">' + escapeHtml(server || 'Unnamed server') +
        '<span class="mcMuted mcWorldGame">' + escapeHtml(world.game || '') + '</span></h2>';
    }
    html += mcWorldBlock(world);
  });
  host.innerHTML = html;
  mcWireWorldButtons();
}

function mcWireWorldButtons() {
  document.querySelectorAll('.mcWorldStatusBtn').forEach(function (b) {
    b.onclick = function () {
      mcSetBackupPrimary(b.getAttribute('data-snap'), b.getAttribute('data-primary') === '1');
    };
  });
  document.querySelectorAll('.mcWorldKeep').forEach(function (b) {
    b.onclick = function () { mcSetBackupExpiry(b.getAttribute('data-snap'), 0); };
  });
  document.querySelectorAll('.mcWorldExpire').forEach(function (b) {
    b.onclick = function () { mcAskBackupExpiry(b.getAttribute('data-snap')); };
  });
  document.querySelectorAll('.mcWorldUse').forEach(function (b) {
    b.onclick = function () { mcConfirmUseVolume(b.getAttribute('data-volume')); };
  });
  document.querySelectorAll('.mcWorldRetire').forEach(function (b) {
    b.onclick = function () { mcConfirmRetireVolume(b.getAttribute('data-volume')); };
  });
  document.querySelectorAll('.mcWorldDeleteBackup').forEach(function (b) {
    b.onclick = function () { mcConfirmDeleteBackup(b.getAttribute('data-snap')); };
  });
  document.querySelectorAll('.mcWorldDeleteVolume').forEach(function (b) {
    b.onclick = function () { mcConfirmDeleteVolume(b.getAttribute('data-volume')); };
  });
  document.querySelectorAll('.mcWorldDeleteBackups').forEach(function (b) {
    b.onclick = function () {
      mcConfirmDeleteBackups(b.getAttribute('data-volume'), b.getAttribute('data-server'));
    };
  });
}

//An expiry is counted from when the backup was taken, which is what AWS Backup measures it against.
function mcAskBackupExpiry(snapshotId) {
  var days = prompt('Delete this backup how many days after it was taken?', '30');
  if (days === null) return;
  var n = parseInt(days, 10);
  if (!(n > 0)) {
    showAlert('That is not a number of days');
    return;
  }
  mcSetBackupExpiry(snapshotId, n);
}

function mcConfirmDeleteBackup(snapshotId) {
  if (!confirm('Delete backup ' + snapshotId + '? This cannot be undone.')) return;
  mcWorldPost('deletebackup', 'snapshotid=' + encodeURIComponent(snapshotId), {});
}

function mcConfirmUseVolume(volumeId) {
  if (!confirm('Put this server on ' + volumeId + '? Its current world is detached and stops being backed up.')) return;
  mcWorldPost('usevolume', 'volumeid=' + encodeURIComponent(volumeId), {});
}

function mcConfirmRetireVolume(volumeId) {
  if (!confirm('Stop backups of ' + volumeId + '?')) return;
  mcWorldPost('retirevolume', 'volumeid=' + encodeURIComponent(volumeId), {});
}

function mcConfirmDeleteVolume(volumeId) {
  if (!confirm('Delete volume ' + volumeId + '? This cannot be undone.')) return;
  mcWorldPost('deletevolume', 'volumeid=' + encodeURIComponent(volumeId), {});
}

//Typed rather than clicked: the one action here that loses a world outright.
function mcConfirmDeleteBackups(volumeId, serverId) {
  var typed = prompt('This deletes every backup of this world and cannot be undone.\n' +
    'Type the server id (' + serverId + ') to confirm:');
  if (typed === null) return;
  //Trimmed, because a copied id brings a space with it.
  if (typed.trim() !== serverId) {
    showAlert('That is not the server id, so nothing was deleted');
    return;
  }
  mcWorldPost('deletevolumebackups', 'volumeid=' + encodeURIComponent(volumeId), {});
}

function mcSetBackupExpiry(snapshotId, days) {
  mcWorldPost('setbackupexpiry', 'snapshotid=' + encodeURIComponent(snapshotId), { expiryDays: days });
}

function mcSetBackupPrimary(snapshotId, primary) {
  mcWorldPost('setbackupprimary', 'snapshotid=' + encodeURIComponent(snapshotId), { primary: primary });
}


async function mcWorldPost(path, params, body) {
  var jwt = await getJwt();
  try {
    const response = await fetch(API_URL + path + '/' + query_string + '&' + params, {
      method: 'post',
      headers: new Headers({ 'Authorization': jwt, 'Content-Type': 'application/json' }),
      body: JSON.stringify(body)
    });
    var data = await response.json();
    showAlert(Array.isArray(data) ? data[0] : data);
  } catch (e) {
    console.log(e);
    showAlert('That did not work');
  }
  //Reloaded rather than assumed.
  await loadWorlds();
}

async function loadWorlds() {
  var jwt = await getJwt();
  try {
    const response = await fetch(API_URL + 'worlds/' + query_string, {
      method: 'get',
      headers: new Headers({ 'Authorization': jwt })
    });
    var data = await response.json();
    if (!Array.isArray(data)) {
      showAlert('Could not load the worlds');
      return;
    }
    mcWorlds = data;
    renderWorlds();
  } catch (e) {
    console.log(e);
    showAlert('Could not load the worlds');
  }
}

async function initWorlds() {
  const { Auth } = aws_amplify_auth;
  const { Amplify } = aws_amplify_core;

  updatelinks();
  Amplify.configure(aws_auth_config);

  try {
    await Auth.currentAuthenticatedUser();
  } catch (e) {
    Auth.federatedSignIn({ provider: 'COGNITO' });
    return;
  }
  await updateAuthButtons();

  await loadWorlds();
}

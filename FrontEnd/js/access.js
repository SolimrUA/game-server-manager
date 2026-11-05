// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

//Access templates, in the order they appear in each cell's dropdown. The capabilities each one grants
//live in the Lambda - this page only ever names a template.
var MC_TEMPLATES = ['player', 'owner'];
var MC_TEMPLATE_LABELS = { player: 'Player', owner: 'Owner' };
//Owner is not something to hand to everyone who signs in, so it isn't offered as a default.
var MC_DEFAULT_TEMPLATES = ['player'];

var mcAccessUsers = [];
var mcAccessServers = [];
//Saved state as it came back from getAccess, keyed "<userSub>|<serverId>", against which edits are
//diffed - so Save sends only what actually changed and Revert needs no reload.
var mcAccessSaved = {};
var mcAccessEdited = {};
var mcAccessNames = {};
//Who may create a server, by sub, in the same saved-vs-edited shape as the grants.
var mcAccessPermitSaved = {};
var mcAccessPermitEdited = {};
var mcDefaultsSaved = { role: '', canCreate: false };
var mcDefaultsEdited = null;

function mcAccessKey(userSub, serverId) {
  return userSub + '|' + serverId;
}

function mcAccessRole(userSub, serverId) {
  var key = mcAccessKey(userSub, serverId);
  return mcAccessEdited.hasOwnProperty(key) ? mcAccessEdited[key] : (mcAccessSaved[key] || '');
}

function mcAccessIsDirty(userSub, serverId) {
  var key = mcAccessKey(userSub, serverId);
  return mcAccessEdited.hasOwnProperty(key) && mcAccessEdited[key] !== (mcAccessSaved[key] || '');
}

//A typed-but-unsaved name has to look unsaved, the same way an edited cell does - rendering it plain
//reads as already applied, and the next load silently drops it.
function mcAccessNameIsDirty(user) {
  var key = 'name:' + user.username;
  return mcAccessNames.hasOwnProperty(key) && mcAccessNames[key] !== (user.name || '');
}

function mcAccessDisplayName(user) {
  var key = 'name:' + user.username;
  var edited = mcAccessNames.hasOwnProperty(key) ? mcAccessNames[key] : null;
  return (edited !== null ? edited : (user.name || '')) || user.email || user.sub;
}

function mcAccessPermit(userSub) {
  return mcAccessPermitEdited.hasOwnProperty(userSub)
    ? mcAccessPermitEdited[userSub] : !!mcAccessPermitSaved[userSub];
}

function mcAccessPermitIsDirty(userSub) {
  return mcAccessPermitEdited.hasOwnProperty(userSub)
    && mcAccessPermitEdited[userSub] !== !!mcAccessPermitSaved[userSub];
}

function mcDefaults() {
  return mcDefaultsEdited || mcDefaultsSaved;
}

function mcDefaultsAreDirty() {
  return mcDefaultsEdited !== null &&
    (mcDefaultsEdited.role !== mcDefaultsSaved.role ||
     mcDefaultsEdited.canCreate !== mcDefaultsSaved.canCreate);
}

function mcAccessHasPendingChanges() {
  var pending = mcAccessPendingChanges();
  return pending.changes.length > 0 || pending.names.length > 0 || pending.permits.length > 0 ||
    mcDefaultsAreDirty();
}

function renderDefaults() {
  var role = document.getElementById('mcDefaultRole');
  var create = document.getElementById('mcDefaultCreate');
  if (!role || !create) return;

  var current = mcDefaults();
  var options = '<option value="">nothing</option>';
  MC_DEFAULT_TEMPLATES.forEach(function (t) {
    options += '<option value="' + t + '"' + (current.role === t ? ' selected' : '') + '>' +
      MC_TEMPLATE_LABELS[t] + '</option>';
  });
  role.innerHTML = options;
  role.className = mcDefaultsAreDirty() ? 'mcAccessDirty' : '';
  create.checked = current.canCreate;

  role.onchange = function () {
    mcDefaultsEdited = { role: role.value, canCreate: create.checked };
    renderDefaults();
    mcAccessUpdateButtons();
  };
  create.onchange = role.onchange;
}

function mcAccessPendingChanges() {
  var changes = [];
  Object.keys(mcAccessEdited).forEach(function (key) {
    if (mcAccessEdited[key] === (mcAccessSaved[key] || '')) return;
    var parts = key.split('|');
    var user = mcAccessUsers.filter(function (u) { return u.sub === parts[0]; })[0];
    changes.push({
      userSub: parts[0],
      serverId: parts[1],
      role: mcAccessEdited[key] || null,
      //Stored alongside the grant so the table is readable on its own in the console.
      email: user ? user.email : null
    });
  });
  var names = [];
  Object.keys(mcAccessNames).forEach(function (key) {
    var username = key.slice('name:'.length);
    var user = mcAccessUsers.filter(function (u) { return u.username === username; })[0];
    if (!user || mcAccessNames[key] === (user.name || '')) return;
    names.push({ username: username, name: mcAccessNames[key] });
  });
  var permits = [];
  Object.keys(mcAccessPermitEdited).forEach(function (sub) {
    if (!mcAccessPermitIsDirty(sub)) return;
    permits.push({ userSub: sub, canCreate: mcAccessPermitEdited[sub] });
  });
  var pending = { changes: changes, names: names, permits: permits };
  if (mcDefaultsAreDirty()) pending.defaults = mcDefaultsEdited;
  return pending;
}

function mcAccessUpdateButtons() {
  var dirty = mcAccessHasPendingChanges();
  var save = document.getElementById('mcAccessSave');
  var revert = document.getElementById('mcAccessRevert');
  if (save) save.disabled = !dirty;
  if (revert) revert.disabled = !dirty;
}

function mcAccessCellHtml(user, server) {
  if (user.isAdmin) {
    return '<td class="mcAccessCell mcAccessAdminCell"><span class="mcBadge running">admin</span></td>';
  }
  var role = mcAccessRole(user.sub, server.ServerId);
  var options = '<option value=""' + (role ? '' : ' selected') + '>&mdash;</option>' +
    MC_TEMPLATES.map(function (t) {
      return '<option value="' + t + '"' + (role === t ? ' selected' : '') + '>' + MC_TEMPLATE_LABELS[t] + '</option>';
    }).join('');
  var dirty = mcAccessIsDirty(user.sub, server.ServerId) ? ' mcAccessDirty' : '';
  return '<td class="mcAccessCell">' +
    '<select class="mcAccessSelect' + dirty + '" data-sub="' + escapeHtml(user.sub) + '"' +
    ' data-server="' + escapeHtml(server.ServerId) + '">' + options + '</select>' +
    '</td>';
}

//An admin can always create one, so there is nothing to grant them.
function mcAccessPermitCellHtml(user) {
  if (user.isAdmin) {
    return '<td class="mcAccessCell mcAccessAdminCell"><span class="mcBadge running">admin</span></td>';
  }
  var dirty = mcAccessPermitIsDirty(user.sub) ? ' mcAccessDirty' : '';
  return '<td class="mcAccessCell">' +
    '<input type="checkbox" class="mcAccessPermit' + dirty + '" data-sub="' + escapeHtml(user.sub) + '"' +
    (mcAccessPermit(user.sub) ? ' checked' : '') + '>' +
    '</td>';
}

function mcAccessServerLabel(server) {
  var game = mcGameDisplayName(server.GameName);
  var name = server.ServerName || server.ServerId;
  if (name) {
    return escapeHtml(name) + '<span class="mcMuted mcAccessServerGame">' + escapeHtml(game) + '</span>';
  }
  return escapeHtml(game);
}

function mcAccessRowHasGaps(user) {
  return mcAccessServers.some(function (s) { return !mcAccessRole(user.sub, s.ServerId); });
}

function mcAccessRowHasAny(user) {
  return mcAccessPermit(user.sub) ||
    mcAccessServers.some(function (s) { return !!mcAccessRole(user.sub, s.ServerId); });
}

//Both are always shown, so a row's actions don't move about as its access changes.
function mcAccessRowActions(user) {
  var up = !user.isAdmin && mcAccessRowHasGaps(user);
  var down = !user.isAdmin && mcAccessRowHasAny(user);
  return '<button type="button" class="btn mcAccessUpgrade" data-sub="' + escapeHtml(user.sub) + '"' +
      (up ? '' : ' disabled') + ' title="' +
      (up ? 'Give this user Player on every server they have no access to'
          : 'This user already has access to every server') + '">&uarr; Player</button>' +
    '<button type="button" class="btn mcAccessDowngrade" data-sub="' + escapeHtml(user.sub) + '"' +
      (down ? '' : ' disabled') + ' title="' +
      (down ? 'Take this user off every server, and off creating one'
            : 'This user has no access to take away') +
      '">&darr; None</button>';
}

function renderAccessGrid() {
  var headRow = document.getElementById('mcAccessHeadRow');
  var body = document.getElementById('mcAccessBody');
  if (!headRow || !body) return;

  headRow.innerHTML = '<th>User</th>' +
    '<th class="mcAccessPermitHead" title="Lets this user create one server, which they then own">' +
    'May create</th>' +
    mcAccessServers.map(function (s) {
      return '<th>' + mcAccessServerLabel(s) + '</th>';
    }).join('') + '<th></th>';

  if (mcAccessUsers.length === 0) {
    body.innerHTML = '<tr class="mcLoadingRow"><td colspan="' + (mcAccessServers.length + 3) + '">No users found</td></tr>';
    mcAccessUpdateButtons();
    return;
  }

  body.innerHTML = '';
  mcAccessUsers.forEach(function (user) {
    var row = document.createElement('tr');
    var rename = user.isAdmin ? '' :
      '<button type="button" class="mcAccessRename" title="Set a display name" data-username="' +
      escapeHtml(user.username) + '">&#9998;</button>';
    var nameDirty = mcAccessNameIsDirty(user);
    row.innerHTML =
      '<td class="mcAccessUserCell">' +
        '<span class="mcAccessUserName' + (nameDirty ? ' mcAccessDirty' : '') + '"' +
        (nameDirty ? ' title="Unsaved - press Save to store this name"' : '') + '>' +
        escapeHtml(mcAccessDisplayName(user)) + '</span>' +
        (nameDirty ? '<span class="mcAccessUnsaved">unsaved</span>' : '') + rename +
        (user.name || mcAccessNames.hasOwnProperty('name:' + user.username)
          ? '<span class="mcMuted mcAccessUserEmail">' + escapeHtml(user.email || '') + '</span>' : '') +
      '</td>' +
      mcAccessPermitCellHtml(user) +
      mcAccessServers.map(function (s) { return mcAccessCellHtml(user, s); }).join('') +
      '<td class="mcAccessRowActions">' + mcAccessRowActions(user) + '</td>';
    body.appendChild(row);
  });

  body.querySelectorAll('.mcAccessSelect').forEach(function (select) {
    select.onchange = function () {
      mcAccessEdited[mcAccessKey(select.dataset.sub, select.dataset.server)] = select.value;
      renderAccessGrid();
    };
  });

  body.querySelectorAll('.mcAccessPermit').forEach(function (box) {
    box.onchange = function () {
      mcAccessPermitEdited[box.dataset.sub] = box.checked;
      renderAccessGrid();
    };
  });

  body.querySelectorAll('.mcAccessUpgrade').forEach(function (button) {
    button.onclick = function () {
      var sub = button.dataset.sub;
      mcAccessServers.forEach(function (s) {
        if (!mcAccessRole(sub, s.ServerId)) mcAccessEdited[mcAccessKey(sub, s.ServerId)] = 'player';
      });
      renderAccessGrid();
    };
  });

  body.querySelectorAll('.mcAccessDowngrade').forEach(function (button) {
    button.onclick = function () {
      var sub = button.dataset.sub;
      mcAccessServers.forEach(function (s) {
        if (mcAccessRole(sub, s.ServerId)) mcAccessEdited[mcAccessKey(sub, s.ServerId)] = '';
      });
      if (mcAccessPermit(sub)) mcAccessPermitEdited[sub] = false;
      renderAccessGrid();
    };
  });

  body.querySelectorAll('.mcAccessRename').forEach(function (button) {
    button.onclick = function () {
      var username = button.dataset.username;
      var user = mcAccessUsers.filter(function (u) { return u.username === username; })[0];
      if (!user) return;
      var current = mcAccessNames.hasOwnProperty('name:' + username) ? mcAccessNames['name:' + username] : (user.name || '');
      var next = prompt('Display name for ' + (user.email || username), current);
      if (next === null) return;
      mcAccessNames['name:' + username] = next.trim();
      renderAccessGrid();
    };
  });

  mcAccessUpdateButtons();
}

async function loadAccess() {
  var jwt = await getJwt();
  try {
    const response = await fetch(API_URL + 'access/' + query_string, {
      method: 'get',
      headers: new Headers({ 'Authorization': jwt })
    });
    var data = await response.json();
    if (!data || !data.users) {
      showAlert('Could not load access');
      return;
    }
    mcAccessUsers = data.users;
    mcAccessServers = data.servers || [];
    mcAccessSaved = {};
    (data.grants || []).forEach(function (g) {
      mcAccessSaved[mcAccessKey(g.userSub, g.serverId)] = g.role || '';
    });
    mcAccessPermitSaved = {};
    (data.permits || []).forEach(function (sub) { mcAccessPermitSaved[sub] = true; });
    mcDefaultsSaved = data.defaults || { role: '', canCreate: false };
    mcDefaultsEdited = null;
    mcAccessEdited = {};
    mcAccessNames = {};
    mcAccessPermitEdited = {};
    renderAccessGrid();
    renderDefaults();
  } catch (e) {
    console.log(e);
    showAlert('Could not load access');
  }
}

async function saveAccess() {
  var pending = mcAccessPendingChanges();
  if (!mcAccessHasPendingChanges()) return;
  var save = document.getElementById('mcAccessSave');
  if (save) save.disabled = true;
  var jwt = await getJwt();
  try {
    const response = await fetch(API_URL + 'saveaccess/' + query_string, {
      method: 'post',
      headers: new Headers({ 'Authorization': jwt, 'Content-Type': 'application/json' }),
      body: JSON.stringify(pending)
    });
    var msgdata = await response.json();
    showAlert(msgdata[0]);
  } catch (e) {
    console.log(e);
    showAlert('Could not save access');
  }
  //Reloaded rather than assumed: whatever the Lambda actually committed is what the grid should show.
  await loadAccess();
}

function revertAccess() {
  mcAccessEdited = {};
  mcAccessNames = {};
  mcAccessPermitEdited = {};
  mcDefaultsEdited = null;
  renderAccessGrid();
  renderDefaults();
}

async function initAccess() {
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

  document.getElementById('mcAccessSave').onclick = saveAccess;
  document.getElementById('mcAccessRevert').onclick = revertAccess;

  await loadAccess();
}

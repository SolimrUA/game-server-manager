// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

//Access templates, in the order they appear in each cell's dropdown. The capabilities each one grants
//live in the Lambda - this page only ever names a template.
var MC_TEMPLATES = ['player', 'owner'];
var MC_TEMPLATE_LABELS = { player: 'Player', owner: 'Owner' };

var mcAccessUsers = [];
var mcAccessServers = [];
//Saved state as it came back from getAccess, keyed "<userSub>|<instanceId>", against which edits are
//diffed - so Save sends only what actually changed and Revert needs no reload.
var mcAccessSaved = {};
var mcAccessEdited = {};
var mcAccessNames = {};

function mcAccessKey(userSub, instanceId) {
  return userSub + '|' + instanceId;
}

function mcAccessRole(userSub, instanceId) {
  var key = mcAccessKey(userSub, instanceId);
  return mcAccessEdited.hasOwnProperty(key) ? mcAccessEdited[key] : (mcAccessSaved[key] || '');
}

function mcAccessIsDirty(userSub, instanceId) {
  var key = mcAccessKey(userSub, instanceId);
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

function mcAccessHasPendingChanges() {
  var pending = mcAccessPendingChanges();
  return pending.changes.length > 0 || pending.names.length > 0;
}

function mcAccessPendingChanges() {
  var changes = [];
  Object.keys(mcAccessEdited).forEach(function (key) {
    if (mcAccessEdited[key] === (mcAccessSaved[key] || '')) return;
    var parts = key.split('|');
    var user = mcAccessUsers.filter(function (u) { return u.sub === parts[0]; })[0];
    changes.push({
      userSub: parts[0],
      instanceId: parts[1],
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
  return { changes: changes, names: names };
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
  var role = mcAccessRole(user.sub, server.InstanceId);
  var options = '<option value=""' + (role ? '' : ' selected') + '>&mdash;</option>' +
    MC_TEMPLATES.map(function (t) {
      return '<option value="' + t + '"' + (role === t ? ' selected' : '') + '>' + MC_TEMPLATE_LABELS[t] + '</option>';
    }).join('');
  var dirty = mcAccessIsDirty(user.sub, server.InstanceId) ? ' mcAccessDirty' : '';
  return '<td class="mcAccessCell">' +
    '<select class="mcAccessSelect' + dirty + '" data-sub="' + escapeHtml(user.sub) + '"' +
    ' data-instance="' + escapeHtml(server.InstanceId) + '">' + options + '</select>' +
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

//A row of nothing but "—" is a user who has never been granted anything, which is the common case right
//after someone is added to the pool - so offer the whole row in one click rather than a cell at a time.
function mcAccessRowHasGaps(user) {
  return mcAccessServers.some(function (s) { return !mcAccessRole(user.sub, s.InstanceId); });
}

function renderAccessGrid() {
  var headRow = document.getElementById('mcAccessHeadRow');
  var body = document.getElementById('mcAccessBody');
  if (!headRow || !body) return;

  headRow.innerHTML = '<th>User</th>' +
    mcAccessServers.map(function (s) {
      return '<th>' + mcAccessServerLabel(s) + '</th>';
    }).join('') + '<th></th>';

  if (mcAccessUsers.length === 0) {
    body.innerHTML = '<tr class="mcLoadingRow"><td colspan="' + (mcAccessServers.length + 2) + '">No users found</td></tr>';
    mcAccessUpdateButtons();
    return;
  }

  body.innerHTML = '';
  mcAccessUsers.forEach(function (user) {
    var row = document.createElement('tr');
    var rename = user.isAdmin ? '' :
      '<button type="button" class="mcAccessRename" title="Set a display name" data-username="' +
      escapeHtml(user.username) + '">&#9998;</button>';
    var upgrade = (!user.isAdmin && mcAccessRowHasGaps(user)) ?
      '<button type="button" class="btn mcAccessUpgrade" data-sub="' + escapeHtml(user.sub) +
      '" title="Give this user Player on every server they have no access to">&uarr; Player</button>' : '';
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
      mcAccessServers.map(function (s) { return mcAccessCellHtml(user, s); }).join('') +
      '<td class="mcAccessRowActions">' + upgrade + '</td>';
    body.appendChild(row);
  });

  body.querySelectorAll('.mcAccessSelect').forEach(function (select) {
    select.onchange = function () {
      mcAccessEdited[mcAccessKey(select.dataset.sub, select.dataset.instance)] = select.value;
      renderAccessGrid();
    };
  });

  body.querySelectorAll('.mcAccessUpgrade').forEach(function (button) {
    button.onclick = function () {
      var sub = button.dataset.sub;
      mcAccessServers.forEach(function (s) {
        if (!mcAccessRole(sub, s.InstanceId)) mcAccessEdited[mcAccessKey(sub, s.InstanceId)] = 'player';
      });
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
      mcAccessSaved[mcAccessKey(g.userSub, g.instanceId)] = g.role || '';
    });
    mcAccessEdited = {};
    mcAccessNames = {};
    renderAccessGrid();
  } catch (e) {
    console.log(e);
    showAlert('Could not load access');
  }
}

async function saveAccess() {
  var pending = mcAccessPendingChanges();
  if (pending.changes.length === 0 && pending.names.length === 0) return;
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
  renderAccessGrid();
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

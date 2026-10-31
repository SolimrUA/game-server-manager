// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

//Only drives which buttons render - the Lambda re-checks every request itself and is the real security
//boundary.
var mcIsAdmin = false;
// Whether this caller may create a server, which every reply reports. A permit is spent by the
// server it creates, so this turns itself off.
var mcCanCreateServer = false;
// Whether each server's join password is on screen rather than masked, by instance id. The table
// rebuilds itself every few seconds, and the field's contents are carried across with it.
var mcPasswordShown = {};

var mcServerOptions = null;
var mcServerOptionsPromise = null;

//Fetched once and shared: the request is slow enough to be felt if it starts on the first click.
function mcLoadServerOptions() {
  if (!mcServerOptionsPromise) {
    mcServerOptionsPromise = (async function () {
      var jwt = await getJwt();
      const response = await fetch(API_URL + 'serveroptions/' + query_string, {
        method: 'get', headers: new Headers({ 'Authorization': jwt })
      });
      mcServerOptions = await response.json();
      return mcServerOptions;
    })().catch(function (e) {
      mcServerOptionsPromise = null;
      throw e;
    });
  }
  return mcServerOptionsPromise;
}

function mcForgetServerOptions() {
  mcServerOptions = null;
  mcServerOptionsPromise = null;
}

function mcServerOptionsForm() {
  return {
    form: document.getElementById('mcNewServerForm'),
    game: document.getElementById('mcNewServerGame'),
    id: document.getElementById('mcNewServerId'),
    version: document.getElementById('mcNewServerVersion'),
    type: document.getElementById('mcNewServerType'),
    volume: document.getElementById('mcNewServerVolume'),
    reuse: document.getElementById("mcNewServerReuse"),
    hint: document.getElementById('mcNewServerHint')
  };
}

function mcSelectedTemplate() {
  var f = mcServerOptionsForm();
  return (mcServerOptions.templates || []).filter(function (t) { return t.game === f.game.value; })[0];
}

//A game version is only asked for where the install needs an exact one, and starting from a backup
//makes the disk size come from that backup instead.
function mcSyncNewServerForm() {
  var f = mcServerOptionsForm();
  var t = mcSelectedTemplate();
  if (!t) return;
  mcRenderBackupOptions(t.game);
  f.version.parentElement.style.display = t.requiresGameVersion ? '' : 'none';
  f.volume.parentElement.style.display = f.reuse.value ? 'none' : '';
  //A hidden field can't be required: the browser refuses to submit and has nothing to point at.
  f.version.required = t.requiresGameVersion;
  f.volume.required = !f.reuse.value;
  f.volume.placeholder = String(t.defaultVolumeSizeGib);
  f.hint.textContent = f.reuse.value
    ? 'The new server starts from that backup. Whatever it came from is left alone.'
    : 'A name becomes the server\'s DNS record and cannot be changed later.';
}

//A backup holds one game's world, so only that game's are offered. One whose game can't be told is
//offered everywhere rather than hidden.
function mcRenderBackupOptions(game) {
  var f = mcServerOptionsForm();
  var previous = f.reuse.value;
  var usable = (mcServerOptions.backups || []).filter(function (b) {
    return !b.game || b.game === game;
  });
  f.reuse.innerHTML = '<option value="">Nothing - a fresh world</option>' +
    usable.map(function (b) {
      return '<option value="' + b.snapshotId + '">' + escapeHtml(b.serverId || b.snapshotId) +
        ' - ' + mcRelativeTimeText(b.startTime) + '</option>';
    }).join('');
  f.reuse.value = usable.some(function (b) { return b.snapshotId === previous; }) ? previous : '';
}

function renderNewServerForm() {
  var f = mcServerOptionsForm();
  f.game.innerHTML = (mcServerOptions.templates || []).map(function (t) {
    return '<option value="' + escapeHtml(t.game) + '">' + escapeHtml(t.displayName) + '</option>';
  }).join('');
  f.type.innerHTML = (mcServerOptions.instanceTypes || []).map(function (x) {
    return '<option value="' + x + '"' + (x === 't3a.medium' ? ' selected' : '') + '>' + x + '</option>';
  }).join('');
  f.game.onchange = mcSyncNewServerForm;
  f.reuse.onchange = mcSyncNewServerForm;
  mcSyncNewServerForm();
}

// mcSetCanCreateServer shows or hides the New server button, and closes the form behind it when the
// permission goes away - which is what spending a permit on a server looks like here.
function mcSetCanCreateServer(canCreate) {
  if (canCreate === mcCanCreateServer) return;
  mcCanCreateServer = canCreate;

  var button = document.getElementById('mcNewServerBtn');
  if (button) button.style.display = canCreate ? '' : 'none';
  if (canCreate) {
    mcLoadServerOptions().catch(function (e) { console.log(e); });
    return;
  }
  mcForgetServerOptions();
  toggleNewServerForm(false);
}

async function toggleNewServerForm(show) {
  var f = mcServerOptionsForm();
  if (!show) { f.form.style.display = 'none'; return; }
  var button = document.getElementById('mcNewServerBtn');
  if (!mcServerOptions) {
    button.disabled = true;
    try {
      await mcLoadServerOptions();
    } catch (e) {
      console.log(e);
      showAlert('Could not load the options for a new server');
      return;
    } finally {
      button.disabled = false;
    }
  }
  renderNewServerForm();
  f.form.style.display = '';
  f.id.focus();
}

async function createServer() {
  var f = mcServerOptionsForm();
  var create = document.getElementById('mcNewServerCreate');
  create.disabled = true;
  var jwt = await getJwt();
  try {
    const response = await fetch(API_URL + 'createserver/' + query_string +
      '&game=' + encodeURIComponent(f.game.value) +
      '&serverid=' + encodeURIComponent(f.id.value) +
      '&instancetype=' + encodeURIComponent(f.type.value) +
      '&gameversion=' + encodeURIComponent(f.version.value) +
      '&volumesize=' + encodeURIComponent(f.reuse.value ? '' : f.volume.value) +
      '&snapshotid=' + encodeURIComponent(f.reuse.value), {
      method: 'get', headers: new Headers({ 'Authorization': jwt })
    });
    var msgdata = await response.json();
    showAlert(msgdata[0]);
    //Reloaded so the next form offers the backup this may have produced.
    mcForgetServerOptions();
    toggleNewServerForm(false);
    f.id.value = '';
    refreshData();
  } catch (e) {
    console.log(e);
    showAlert('Could not create the server');
  }
  create.disabled = false;
}

async function deleteServer(instanceId, label) {
  if (!confirm('Delete ' + label + '?\n\nIts backups are kept, so the server can be recreated from one.')) return;
  var jwt = await getJwt();
  try {
    const response = await fetch(API_URL + 'deleteserver/' + query_string + '&instanceid=' + instanceId, {
      method: 'get', headers: new Headers({ 'Authorization': jwt })
    });
    var msgdata = await response.json();
    showAlert(msgdata[0]);
    mcForgetServerOptions();
    refreshData();
  } catch (e) {
    console.log(e);
    showAlert('Could not delete the server');
  }
}

//getInfo resolves the caller's access template per server and returns the capabilities it grants, so the
//front end never needs to know what a template contains.
function mcCan(instance, capability) {
  var capabilities = (instance && instance['Capabilities']) || [];
  return capabilities.indexOf(capability) !== -1;
}

async function mcInfo(url, idToken) {

    var mcInfodata = API_URL + "getinfo/" + query_string;

    const response = await fetch(mcInfodata, {
      method: 'get',
      headers: new Headers({
        'Authorization': idToken
      })
    });

    var data = await response.json();
    renderTable(data);
}

var MC_GAME_DISPLAY_NAMES = {
  'valheim': 'Valheim',
  'vintagestory': 'Vintage Story'
};
function mcGameDisplayName(gameName) {
  if (!gameName) return '—';
  if (MC_GAME_DISPLAY_NAMES[gameName]) return MC_GAME_DISPLAY_NAMES[gameName];
  return gameName.charAt(0).toUpperCase() + gameName.slice(1);
}

var MC_MOD_SOURCE_HINT = {
  'vintagestory': { placeholder: 'e.g. carrycapacity', label: 'Mod ID or slug from mods.vintagestory.at' },
  'valheim': { placeholder: 'e.g. Smoothbrain-EquipmentAndQuickSlots', label: 'Full package name from thunderstore.io (Namespace-Name)' }
};
function mcModSourceHint(gameName) {
  return MC_MOD_SOURCE_HINT[gameName] || { placeholder: 'mod identifier', label: 'Mod identifier' };
}

// Every reading of serviceStatus lives here, so the row badge and the Overview panel cannot drift
// apart. The install status is checked first because the control Lambda only reports one while the
// game has yet to say anything at all. `key` names the state for callers that treat some specially.
function mcGameState(instance) {
  if (instance['InstallStatus'] === 'failed') return { key: 'install-failed', text: 'Install Failed', badgeClass: 'error' };
  if (instance['InstallStatus'] === 'installing') return { key: 'installing', text: 'Installing', badgeClass: 'pending' };
  var serviceStatus = instance['GameStatus'] && instance['GameStatus'].serviceStatus;
  // Nothing reported yet - the agent pushes its first snapshot a few seconds into the boot.
  if (!serviceStatus) return { key: 'starting', text: 'Starting', badgeClass: 'pending' };
  switch (serviceStatus) {
    case 'active': return { key: 'running', text: 'Running', badgeClass: 'running' };
    case 'activating': return { key: 'starting', text: 'Starting', badgeClass: 'pending' };
    case 'deactivating': return { key: 'stopping', text: 'Stopping', badgeClass: 'pending' };
    case 'inactive': return { key: 'stopped', text: 'Stopped', badgeClass: 'stopped' };
    case 'failed': return { key: 'failed', text: 'Error', badgeClass: 'error' };
    // An agent that cannot tell says so, rather than being read as a game on its way up.
    default: return { key: 'unknown', text: 'Unknown', badgeClass: 'unknown' };
  }
}

function mcGameStatus(instance) {
  if (instance['State'] !== 'running') {
    return { text: '—', badgeClass: null };
  }
  return mcGameState(instance);
}

// Below this much of a disk left, a server is close enough to breaking that its row says so without
// having to be opened.
var MC_DISK_WARN_FRACTION = 0.15;

// Reported one disk at a time, because which one is short decides the fix: a full data volume needs
// a resize or a clear-out, a full root is usually logs or docker images.
var MC_DISK_LABELS = [['root', 'Root'], ['data', 'Data']];

function mcDiskParts(disk) {
  var parts = [];
  MC_DISK_LABELS.forEach(function (entry) {
    var d = disk && disk[entry[0]];
    if (!d || d.totalGib == null || d.freeGib == null || d.totalGib <= 0) return;
    parts.push({ label: entry[1], free: d.freeGib, total: d.totalGib, fraction: d.freeGib / d.totalGib });
  });
  return parts;
}

function mcDiskText(disk) {
  var text = mcDiskParts(disk).map(function (p) {
    return p.label + ' ' + Math.round(p.fraction * 100) + '%';
  }).join(', ');
  return text ? escapeHtml(text) : '\u2014';
}

// A stopped server's last report says nothing about the disk of the one that boots next.
function mcLowDisks(instance) {
  if (instance['State'] !== 'running') return [];
  var disk = instance['GameStatus'] && instance['GameStatus'].disk;
  return mcDiskParts(disk).filter(function (p) { return p.fraction < MC_DISK_WARN_FRACTION; });
}

function mcDiskWarnHtml(instance) {
  var low = mcLowDisks(instance);
  if (!low.length) return '';
  var detail = low.map(function (p) {
    return p.label + ': ' + p.free.toFixed(1) + ' of ' + p.total.toFixed(1) + ' GiB free';
  }).join(', ');
  return '<span class="mcBadge error mcDiskWarn" title="' + escapeHtml(detail) + '">Disk low</span>';
}

// 'triggered-once' means one empty check is already recorded, so the next one stops the instance.
function mcIdleShutdownStatus(instance) {
  var status = instance['IdleShutdownStatus'];
  if (status === 'disabled') return { text: 'Paused', badgeClass: 'stopped' };
  if (status === 'triggered-once') return { text: 'Active (Idle)', badgeClass: 'pending' };
  if (status === 'enabled') return { text: 'Active', badgeClass: 'running' };
  return { text: '—', badgeClass: null };
}

var MC_SCOPED_GAME_STATES = ['starting', 'stopping', 'stopped'];

// Auto-shutdown merely being enabled isn't worth surfacing here; it stays in the Overview tab.
function mcCombinedState(instance) {
  var ec2State = instance['State'];
  if (ec2State !== 'running') {
    if (ec2State === 'stopped') return { text: 'Stopped', badgeClass: 'stopped' };
    var label = ec2State ? ec2State.charAt(0).toUpperCase() + ec2State.slice(1).replace(/-/g, ' ') : 'Unknown';
    return { text: label + ' (EC2)', badgeClass: 'pending' };
  }
  var game = mcGameState(instance);
  if (game.key === 'running' && instance['IdleShutdownStatus'] === 'triggered-once') {
    return { text: 'Running (Idle)', badgeClass: 'idle' };
  }
  // This column carries one value for a whole server, so the states a reader could take for the
  // instance's own say which they are about.
  if (MC_SCOPED_GAME_STATES.indexOf(game.key) !== -1) {
    return { text: game.text + ' (Game)', badgeClass: game.badgeClass };
  }
  return game;
}

// The control Lambda takes a slug, not an instance type: it resolves one through a fixed
// micro/small/medium/large -> t3a.* map of environment variables, so a size with no mapping there
// can't be offered here.
var MC_RESIZE_OPTIONS = [
  { value: 'micro', type: 't3a.micro' },
  { value: 'small', type: 't3a.small' },
  { value: 'medium', type: 't3a.medium' },
  { value: 'large', type: 't3a.large' }
];
function mcResizeOptionsHtml(currentInstanceType) {
  return MC_RESIZE_OPTIONS.map(function (opt) {
    var selected = opt.type === currentInstanceType ? ' selected' : '';
    return '<option value="' + opt.value + '"' + selected + '>' + opt.type + '</option>';
  }).join('');
}

// `permissionDenied` keeps the button visible but inert, so the table's columns don't shift per row
// depending on who's looking. The Actions tab omits it entirely instead.
function mcLifecycleButtonHtml(ec2State, permissionDenied) {
  var label, cls;
  if (ec2State === 'stopped') { label = 'Start'; cls = 'start'; }
  else if (ec2State === 'running') { label = 'Stop'; cls = 'stop'; }
  else { label = ec2State === 'pending' ? 'Starting…' : 'Stopping…'; cls = 'disabled'; }
  var disabled = permissionDenied || cls === 'disabled';
  var title = permissionDenied ? ' title="You don\'t have permission to control this server"' : '';
  return '<button class="btn ' + cls + ' mcLifecycleBtn"' + (disabled ? ' disabled' : '') + title + '>' + label + '</button>';
}

function mcRestartGameButtonHtml(enabled) {
  var title = enabled ? 'Restart just the game process, not the EC2 instance' : 'Start the server to restart the game';
  return '<button type="button" class="btn mcRestartGameBtn"' + (enabled ? '' : ' disabled') + ' title="' + title + '">Restart Game</button>';
}

function mcUpdateGameButtonHtml(enabled) {
  var title = enabled ? 'Update the game server software to the latest version' : 'Start the server to update the game';
  return '<button type="button" class="btn mcUpdateGameBtn"' + (enabled ? '' : ' disabled') + ' title="' + title + '">Update Game</button>';
}

// The SSM command zips to a fixed /tmp path, so only one download can run per instance - any download
// in progress disables every download button for it, not just the one that was clicked.
function mcDownloadWorldButtonHtml(instanceId, scope, label, permissionDenied) {
  var inProgress = worldDownloads[instanceId] && worldDownloads[instanceId].status === 'InProgress';
  var disabled = inProgress || permissionDenied;
  var title = permissionDenied ? ' title="You don\'t have permission to download this server\'s world"' : '';
  return '<button class="btn mcDownloadWorldBtn" data-scope="' + scope + '" data-label="' + escapeHtml(label) + '"' +
    (disabled ? ' disabled' : '') + title + '>' + (inProgress ? 'Downloading…' : escapeHtml(label)) + '</button>';
}

var MC_COPY_ICON_SVG = '<svg width="14" height="14" viewBox="0 0 14 14" fill="none">' +
  '<rect x="4" y="4" width="8" height="9" rx="1.5" stroke="currentColor" stroke-width="1.3"/>' +
  '<path d="M3 9.5V2.5C3 1.94772 3.44772 1.5 4 1.5H9" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>' +
  '</svg>';

var MC_DELETE_ICON_SVG = '<svg width="11" height="11" viewBox="0 0 10 10" fill="none">' +
  '<path d="M1 1L9 9M9 1L1 9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>' +
  '</svg>';

async function mcCopyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (e) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e2) { ok = false; }
    document.body.removeChild(ta);
    return ok;
  }
}

function mcSwitchTab(detailRow, tabName) {
  detailRow.querySelectorAll('.mcTab').forEach(function (t) {
    t.classList.toggle('active', t.dataset.tab === tabName);
  });
  detailRow.querySelectorAll('.mcTabPanel').forEach(function (p) {
    p.classList.toggle('hidden', p.dataset.tabPanel !== tabName);
  });
}

function mcRelativeTimeText(timestamp) {
  if (!timestamp) return '—';
  var diffMinutes = Math.floor((Date.now() - new Date(timestamp).getTime()) / (1000 * 60));
  if (diffMinutes < 1) return '<1m ago';
  if (diffMinutes < 60) return diffMinutes + 'm ago';
  var diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) return diffHours + 'h ago';
  return Math.floor(diffHours / 24) + 'd ago';
}

// Kept outside the DOM because the periodic refresh tears down and rebuilds the whole table body,
// wiping any in-progress status a couple of seconds after it appeared.
var worldDownloads = {};

function mcWorldDownloadStatusHtml(instanceId) {
  var dl = worldDownloads[instanceId];
  if (!dl) return '';
  var scopeLabel = dl.scope === 'core' ? 'core files' : 'full';
  if (dl.status === 'Success') return '<a href="' + dl.url + '" target="_blank">Download (' + escapeHtml(scopeLabel) + ')</a>';
  if (dl.status === 'Failed') return 'Failed: ' + escapeHtml(dl.error || 'unknown error');
  if (dl.status === 'RetryPrompt') return '<a href="javascript:void(0)" class="mcRetryWorldDownloadCheck">Still processing - click to check again</a>';
  return 'Preparing ' + scopeLabel + ' archive…' + (dl.attempt ? ' (' + dl.attempt + ')' : '');
}

// So a status change doesn't have to wait for the next periodic refresh.
function mcRefreshWorldDownloadStatusUi(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  var inProgress = worldDownloads[instanceId] && worldDownloads[instanceId].status === 'InProgress';

  [row, detailRow].forEach(function (container) {
    if (!container) return;
    container.querySelectorAll('.mcDownloadWorldBtn').forEach(function (btn) {
      btn.disabled = inProgress;
      btn.textContent = inProgress ? 'Downloading…' : btn.dataset.label;
    });
  });

  var span = detailRow && detailRow.querySelector('.mcWorldDownloadStatus');
  if (!span) return;
  span.innerHTML = mcWorldDownloadStatusHtml(instanceId);
  var retryLink = span.querySelector('.mcRetryWorldDownloadCheck');
  if (retryLink) retryLink.onclick = function (e) { e.stopPropagation(); retryWorldDownload(instanceId); };
}

function escapeHtml(text) {
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function renderModsList(mods, canDelete, enabled) {
  if (!mods || mods.length === 0) {
    return '<p class="mcModsEmpty">No mod info available yet.</p>';
  }
  return '<ul class="mcModsList">' + mods.map(function (mod) {
    var version = mod.version ? ' <span class="mcModVersion">v' + escapeHtml(mod.version) + '</span>' : '';
    var title = enabled ? 'Remove this mod' : 'Start the server to remove mods';
    var deleteBtn = canDelete ?
      ' <button type="button" class="mcModDeleteBtn" data-mod-name="' + escapeHtml(mod.name) + '"' + (enabled ? '' : ' disabled') +
      ' title="' + title + '">' + MC_DELETE_ICON_SVG + '</button>' : '';
    return '<li>' + escapeHtml(mod.name) + version + deleteBtn + '</li>';
  }).join('') + '</ul>';
}

function mcInstallModFormHtml(gameName, enabled, gameVersion) {
  var hint = mcModSourceHint(gameName);
  //Only Vintage Story resolves a mod against a game version, and a release that lists only older ones
  //is reached by naming one of them here.
  var version = gameName === 'vintagestory'
    ? '<input type="text" class="mcModGameVersionInput" placeholder="' + escapeHtml(gameVersion || 'game version') +
      '" title="Game version to install for - defaults to the one this server reports"' + (enabled ? '' : ' disabled') + '>'
    : '';
  return '<div class="mcInstallModForm">' +
    '<input type="text" class="mcModRefInput" placeholder="' + escapeHtml(hint.placeholder) + '"' + (enabled ? '' : ' disabled') + '>' +
    version +
    '<button type="button" class="btn mcInstallModBtn"' + (enabled ? '' : ' disabled') + '>Install</button>' +
    '</div>' +
    '<p class="mcModSourceHint">' + escapeHtml(hint.label) + (enabled ? '' : ' — start the server to install or remove mods') + '</p>';
}

// mcEditingInTable reports whether the cursor is in something in the table that holds what someone
// has entered.
function mcEditingInTable(tbody) {
  var active = document.activeElement;
  if (!active || !tbody || !tbody.contains(active)) return false;
  return ['INPUT', 'SELECT', 'TEXTAREA'].indexOf(active.tagName) !== -1;
}

// mcControlKey names a control by where the markup puts it, so the same control is found again in
// the panel that replaces this one. seen counts duplicates, which keeps a repeated class apart.
function mcControlKey(el, seen) {
  var key = el.tagName + '|' + (el.className || '');
  seen[key] = (seen[key] || 0) + 1;
  return key + '|' + seen[key];
}

function mcEachControl(panel, visit) {
  if (!panel) return;
  var seen = {};
  panel.querySelectorAll('input, select, textarea').forEach(function (el) {
    visit(el, mcControlKey(el, seen));
  });
}

// mcCaptureControls records every control in a panel, so that a rebuild can put back what was in
// them. Adding a field anywhere needs nothing here: it is found by being a control.
function mcCaptureControls(panel) {
  var state = {};
  mcEachControl(panel, function (el, key) {
    state[key] = {
      value: el.value, checked: el.checked, focused: el === document.activeElement,
      start: el.selectionStart, end: el.selectionEnd,
      scrollTop: el.scrollTop, scrollLeft: el.scrollLeft
    };
  });
  return state;
}

function mcRestoreControlValues(panel, state) {
  mcEachControl(panel, function (el, key) {
    var was = state[key];
    if (!was) return;
    el.value = was.value;
    el.checked = was.checked;
  });
}

// mcRestoreControlFocus puts back where someone was looking in a control, which only means anything
// once it is on the page.
function mcRestoreControlFocus(panel, state) {
  mcEachControl(panel, function (el, key) {
    var was = state[key];
    if (!was) return;
    if (was.focused) el.focus();
    //Putting the text back puts the caret at the end of it, focused or not.
    if (was.start != null && typeof el.setSelectionRange === 'function') {
      el.setSelectionRange(was.start, was.end);
    }
    //Putting the text back scrolls a field larger than its box to the end, and the caret alone
    //doesn't bring it back.
    el.scrollTop = was.scrollTop;
    el.scrollLeft = was.scrollLeft;
  });
}

async function renderTable(data) {
    var instances = (data && data[1] && data[1]["Instances"]) || [];
    mcSetCanCreateServer(!!(data && data[1] && data[1]["CanCreateServer"]));
    var tbody = document.getElementById('mcServerTableBody');
    // Nothing is worth replacing a control someone has their cursor in: a half-typed value, a
    // dropdown mid-choice, a config scrolled to the line being edited. Restoring all of that
    // afterwards is possible but never quite right, so the refresh waits instead. Only form controls
    // hold the table back - a focused button cannot, or walking away from one would freeze it.
    if (mcEditingInTable(tbody)) return;
    var previouslySelectedRow = tbody.querySelector('tr.mcServerRow.selected');
    var previouslySelectedInstanceId = previouslySelectedRow ? previouslySelectedRow.dataset.instanceId : null;
    // Scoped to the selected row's own panel: querying the whole tbody would find whichever row comes
    // first (still on its default Overview tab), and every refresh would snap the open row back to it.
    var previouslyActiveDetail = previouslySelectedRow && previouslySelectedRow.nextElementSibling;
    var previouslyActiveTabEl = previouslyActiveDetail && previouslyActiveDetail.querySelector('.mcTab.active');
    var previouslyActiveTab = previouslyActiveTabEl ? previouslyActiveTabEl.dataset.tab : 'overview';
    // Everything someone may have typed, chosen or scrolled in the open panel, captured by what the
    // markup makes of each control rather than by a list that has to be kept in step with it.
    var previousControls = mcCaptureControls(previouslyActiveDetail);

    if (instances.length === 0) {
      tbody.innerHTML = '<tr class="mcLoadingRow"><td colspan="8">No gaming server instances found</td></tr>';
      return;
    }

    tbody.innerHTML = '';
    instances.forEach(function (instance) {
      var instanceId = instance['InstanceId'];
      var ec2State = instance['State'];
      var dns = instance['DomainName'] && instance['DomainName'] !== 'No domain tag found' ? instance['DomainName'] : '—';
      var status = instance['GameStatus'];
      var combinedState = mcCombinedState(instance);
      var gameStatus = mcGameStatus(instance);
      var idleStatus = mcIdleShutdownStatus(instance);
      var idlePaused = instance['IdleShutdownStatus'] === 'disabled';
      var canLifecycle = mcCan(instance, 'lifecycle');
      var canDownload = mcCan(instance, 'download');
      var canContent = mcCan(instance, 'content');
      var canBackup = mcCan(instance, 'backup');
      var canPassword = mcCan(instance, 'password');
      var canAutoShutdown = mcCan(instance, 'autoshutdown');
      var canResize = mcCan(instance, 'resize');
      var modsRunning = ec2State === 'running';
      var playersText = status && status.players && status.players.current != null
        ? status.players.current + (status.players.max != null ? ' / ' + status.players.max : '')
        : '—';
      var versionText = status && status.version ? escapeHtml(status.version) : '—';
      var lastSaveText = mcRelativeTimeText(status && status.lastSaveTime);

      var row = document.createElement('tr');
      row.className = 'mcServerRow';
      row.dataset.instanceId = instanceId;
      row.onclick = function () { toggleServerRow(row); };

      row.innerHTML =
        '<td><span class="mcDnsCell"' + (dns !== '—' ? ' data-dns-value="' + escapeHtml(dns) + '" title="Click to copy"' : '') + '>' +
          '<span class="mcDnsDot" data-dns-dot></span>' + escapeHtml(dns) +
          (dns !== '—' ? '<span class="mcCopyDns">' + MC_COPY_ICON_SVG + '</span>' : '') +
        '</span></td>' +
        '<td>' + mcGameDisplayName(instance['GameName']) + '</td>' +
        '<td' + (instance['ServerName'] ? '>' : ' class="mcMuted">') +
          escapeHtml(instance['ServerName'] || '—') + '</td>' +
        '<td><span class="mcBadge ' + combinedState.badgeClass + '">' + escapeHtml(combinedState.text) + '</span>' + mcDiskWarnHtml(instance) + '</td>' +
        '<td>' + playersText + '</td>' +
        '<td>' + lastSaveText + '</td>' +
        '<td>' + mcLifecycleButtonHtml(ec2State, !canLifecycle) + '</td>' +
        '<td>' + mcDownloadWorldButtonHtml(instanceId, 'core', 'Download (Just World)', !canDownload) + '</td>';

      var dnsCell = row.querySelector('.mcDnsCell[data-dns-value]');
      if (dnsCell) dnsCell.onclick = function (e) {
        e.stopPropagation();
        var icon = dnsCell.querySelector('.mcCopyDns');
        var original = icon.innerHTML;
        mcCopyToClipboard(dnsCell.dataset.dnsValue).then(function (ok) {
          icon.textContent = ok ? 'Copied' : 'Copy failed';
          setTimeout(function () { icon.innerHTML = original; }, 1200);
        });
      };

      //Only Vintage Story keeps its world settings somewhere the panel can write.
      var canWorld = canContent && instance['GameName'] === 'vintagestory';

      var actionClusters =
        (canLifecycle ? mcActionClusterHtml('Power', mcLifecycleButtonHtml(ec2State)) : '') +
        (canResize ?
          mcActionClusterHtml('Capacity',
            '<select class="mcResizeSelect">' + mcResizeOptionsHtml(instance['InstanceType']) + '</select>' +
            '<button class="btn primary">Resize</button>') : '') +
        ((canBackup || canDownload || canContent) ?
          mcActionClusterHtml('Data',
            (canBackup ? '<button class="btn mcBackupNowBtn">Backup Now</button>' : '') +
            (canDownload ?
              mcDownloadWorldButtonHtml(instanceId, 'full', 'Download (All Data)') +
              mcDownloadWorldButtonHtml(instanceId, 'core', 'Download (Just World)') +
              '<span class="mcWorldDownloadStatus">' + mcWorldDownloadStatusHtml(instanceId) + '</span>' : '') +
            // Grouped under Data, not Power: these act on the server's content, not its power state.
            (canContent ? mcRestartGameButtonHtml(modsRunning) + mcUpdateGameButtonHtml(modsRunning) : '')) : '') +
        (canPassword ? mcPasswordClusterHtml() : '') +
        (canWorld ? mcPlayerRoleClusterHtml(modsRunning) : '') +
        (canContent ? mcServerNameClusterHtml(instance['ServerName']) : '') +
        (canAutoShutdown ?
          mcActionClusterHtml('Automation',
            '<button class="btn mcIdleShutdownToggleBtn">' + (idlePaused ? 'Resume Auto-Shutdown' : 'Pause Auto-Shutdown') + '</button>' +
            '<span class="mcMuted">Auto-shutdown: ' + escapeHtml(idleStatus.text) + '</span>') : '') +
        (mcIsAdmin ?
          mcActionClusterHtml('Server',
            '<button type="button" class="btn danger mcDeleteServerBtn">Delete Server</button>') : '');

      var overviewHtml =
        mcKvHtml('Game', mcGameDisplayName(instance['GameName'])) +
        mcKvHtml('Server Name', escapeHtml(instance['ServerName'] || '—')) +
        mcKvHtml('EC2 State', escapeHtml(ec2State || '—')) +
        mcKvHtml('Game State', escapeHtml(gameStatus.text)) +
        mcKvHtml('Auto-Shutdown', escapeHtml(idleStatus.text)) +
        mcKvHtml('Players', playersText) +
        mcKvHtml('Last Save', lastSaveText) +
        mcKvHtml('Free disk space', mcDiskText(status && status.disk)) +
        mcKvHtml('Type', escapeHtml(instance['InstanceType'] || '—')) +
        mcKvHtml('IP Address', escapeHtml(instance['PublicIpAddress'] || '—')) +
        mcKvHtml('Version', versionText) +
        mcKvHtml('Last Backup', mcRelativeTimeText(instance['LastBackupTime']));

      var detailRow = document.createElement('tr');
      detailRow.className = 'mcServerDetail hidden';
      detailRow.innerHTML =
        '<td colspan="8">' +
        '<div class="mcTabsNav">' +
          '<div class="mcTab" data-tab="overview">Overview</div>' +
          '<div class="mcTab" data-tab="actions">Actions</div>' +
          '<div class="mcTab" data-tab="mods">Mods</div>' +
          (canWorld ? '<div class="mcTab" data-tab="world">World</div>' : '') +
        '</div>' +
        '<div class="mcTabPanel mcKvGrid" data-tab-panel="overview">' + overviewHtml + '</div>' +
        '<div class="mcTabPanel mcActionClusters" data-tab-panel="actions">' + actionClusters + '</div>' +
        (canWorld ? '<div class="mcTabPanel mcWorldPanel" data-tab-panel="world">' +
          mcWorldPanelHtml() + '</div>' : '') +
        '<div class="mcTabPanel mcModsPanel" data-tab-panel="mods">' +
          renderModsList(status && status.mods, canContent, modsRunning) +
          (canContent ?
            mcInstallModFormHtml(instance['GameName'], modsRunning,
              instance['GameStatus'] && instance['GameStatus'].version) +
            '<p class="mcModsNote">Installing or removing a mod here can take a minute or two to show up in the list above. ' +
            'Changes only take effect after a restart:</p>' +
            mcRestartGameButtonHtml(modsRunning)
            : '') +
        '</div>' +
        '</td>';

      detailRow.querySelectorAll('.mcTab').forEach(function (tab) {
        tab.onclick = function (e) { e.stopPropagation(); mcSwitchTab(detailRow, tab.dataset.tab); };
      });
      mcSwitchTab(detailRow, instanceId === previouslySelectedInstanceId ? previouslyActiveTab : 'overview');

      var select = detailRow.querySelector('select');
      var resizeBtn = detailRow.querySelector('.primary');
      var backupBtn = detailRow.querySelector('.mcBackupNowBtn');
      var idleToggleBtn = detailRow.querySelector('.mcIdleShutdownToggleBtn');

      [row, detailRow].forEach(function (container) {
        var lifecycleBtn = container.querySelector('.mcLifecycleBtn');
        if (lifecycleBtn && !lifecycleBtn.disabled) lifecycleBtn.onclick = function (e) {
          e.stopPropagation();
          if (ec2State === 'stopped') { showAlert('Starting the Server'); startServer(instanceId); }
          else { showAlert('Stopping the Server'); stopServer(instanceId); }
        };
        container.querySelectorAll('.mcDownloadWorldBtn').forEach(function (btn) {
          btn.onclick = function (e) { e.stopPropagation(); downloadWorld(instanceId, btn.dataset.scope); };
        });
      });

      if (resizeBtn) resizeBtn.onclick = function (e) { e.stopPropagation(); showAlert('Please wait... Resizing your server'); resizeServer(select.value, instanceId); };
      if (backupBtn) backupBtn.onclick = function (e) { e.stopPropagation(); showAlert('Starting backup'); backupNow(instanceId); };
      if (idleToggleBtn) idleToggleBtn.onclick = function (e) {
        e.stopPropagation();
        if (idlePaused) { showAlert('Resuming auto-shutdown'); resumeIdleShutdown(instanceId); }
        else { showAlert('Pausing auto-shutdown'); pauseIdleShutdown(instanceId); }
      };
      var retryLink = detailRow.querySelector('.mcRetryWorldDownloadCheck');
      if (retryLink) retryLink.onclick = function (e) { e.stopPropagation(); retryWorldDownload(instanceId); };

      var modRefInput = detailRow.querySelector('.mcModRefInput');
      // Before the handlers below, which read these fields to decide what to show.
      if (instanceId === previouslySelectedInstanceId) {
        mcRestoreControlValues(detailRow, previousControls);
      }

      var revealPasswordBtn = detailRow.querySelector('.mcRevealPasswordBtn');
      if (revealPasswordBtn) {
        var passwordInput = detailRow.querySelector('.mcPasswordInput');
        var savePasswordBtn = detailRow.querySelector('.mcSavePasswordBtn');
        //Only the open row's field keeps its contents, so a server left and come back to has an
        //empty one, which cannot be on screen.
        mcPasswordShown[instanceId] = !!mcPasswordShown[instanceId] && !!passwordInput.value;
        mcShowPasswordState(instanceId, passwordInput, revealPasswordBtn);

        revealPasswordBtn.onclick = async function (e) {
          e.stopPropagation();
          //Closing the panel masks the field, so the state has to be read at click time rather than
          //when the row was drawn.
          if (mcPasswordShown[instanceId]) {
            mcPasswordShown[instanceId] = false;
            mcShowPasswordState(instanceId, passwordInput, revealPasswordBtn);
            return;
          }
          if (!passwordInput.value) {
            revealPasswordBtn.disabled = true;
            var password = await fetchGamePassword(instanceId);
            revealPasswordBtn.disabled = false;
            if (password === null) return;
            var field = mcPasswordField(instanceId);
            if (!field) return;
            field.input.value = password;
            mcPasswordShown[instanceId] = true;
            mcShowPasswordState(instanceId, field.input, field.revealBtn);
            return;
          }
          mcPasswordShown[instanceId] = true;
          mcShowPasswordState(instanceId, passwordInput, revealPasswordBtn);
        };

        savePasswordBtn.onclick = async function (e) {
          e.stopPropagation();
          var password = passwordInput.value.trim();
          if (!password) { showAlert('Type a new password first'); return; }
          savePasswordBtn.disabled = true;
          var saved = await saveGamePassword(instanceId, password);
          savePasswordBtn.disabled = false;
          if (saved === null) return;
          var field = mcPasswordField(instanceId);
          if (field) field.input.value = saved;
          showAlert('Join password saved - restart the game to apply it');
        };
      }

      var playerRoleBtn = detailRow.querySelector('.mcPlayerRoleBtn');
      if (playerRoleBtn && !playerRoleBtn.disabled) {
        playerRoleBtn.onclick = async function (e) {
          e.stopPropagation();
          var player = detailRow.querySelector('.mcPlayerNameInput').value.trim();
          var role = detailRow.querySelector('.mcPlayerRoleInput').value.trim();
          if (!player || !role) { showAlert('Name a player and a role first'); return; }
          playerRoleBtn.disabled = true;
          var granted = await grantPlayerRole(instanceId, player, role);
          var live = mcPlayerRoleFields(instanceId);
          if (!live) return;
          live.button.disabled = false;
          if (granted) live.name.value = '';
        };
      }

      var newWorldBtn = detailRow.querySelector('.mcNewWorldBtn');
      if (newWorldBtn) {
        mcLoadWorldDefaults(instanceId, instance['GameName']).then(function (presets) {
          var field = mcWorldField(instanceId);
          if (!field) return;
          mcFillWorldPresets(field, presets);
          //Only when there is nothing there: a rebuild carries an edited block across, and refilling
          //would throw away what someone is part way through.
          if (!field.config.value) {
            field.config.value = mcWorldPresetBody(instance['GameName'], field.preset.value);
          }
          field.config.readOnly = !field.customise.checked;
        });

        //The block always shows what the chosen preset would do. The box decides only whether those
        //settings are someone's to change.
        var showPreset = function () {
          var field = mcWorldField(instanceId);
          if (!field) return;
          field.config.readOnly = !field.customise.checked;
          field.config.value = mcWorldPresetBody(instance['GameName'], field.preset.value);
        };
        detailRow.querySelector('.mcWorldCustomiseBox').onchange = function () {
          var field = mcWorldField(instanceId);
          if (field) field.config.readOnly = !field.customise.checked;
        };
        detailRow.querySelector('.mcWorldPresetSelect').onchange = showPreset;

        newWorldBtn.onclick = async function (e) {
          e.stopPropagation();
          var field = mcWorldField(instanceId);
          if (!field) return;
          var name = field.name.value.trim();
          if (!name) { showAlert('Name the new world first'); return; }
          var body = field.config.value;
          if (!body) { showAlert('Pick the settings to generate the world from'); return; }
          if (!confirm('Generate a new world called "' + name + '" on this server?\n\n' +
              'It is created the next time the game starts. The world this server is on now stays ' +
              'on its disk, but the control panel cannot switch back to it.')) return;

          newWorldBtn.disabled = true;
          var saved = await saveNewWorld(instanceId, name, field.seed.value.trim(), body);
          newWorldBtn.disabled = false;
          if (saved === null) return;
          var live = mcWorldField(instanceId);
          if (live) { live.name.value = ''; live.seed.value = ''; }
          showAlert('New world saved - it is generated the next time the game starts');
        };
      }

      var saveServerNameBtn = detailRow.querySelector('.mcSaveServerNameBtn');
      if (saveServerNameBtn) {
        var serverNameInput = detailRow.querySelector('.mcServerNameInput');
        saveServerNameBtn.onclick = async function (e) {
          e.stopPropagation();
          var name = serverNameInput.value.trim();
          if (!name) { showAlert("Type a new server name - a server's name can't be removed"); return; }
          saveServerNameBtn.disabled = true;
          var saved = await saveServerName(instanceId, name);
          saveServerNameBtn.disabled = false;
          if (saved === null) return;
          //The request can outlast the rebuild that replaces this field, so it is looked up again.
          var live = mcServerNameField(instanceId);
          if (live) live.value = '';
          showAlert('Server name saved - restart the game to apply it');
        };
      }
      var installModBtn = detailRow.querySelector('.mcInstallModBtn');
      if (installModBtn && !installModBtn.disabled) installModBtn.onclick = function (e) {
        e.stopPropagation();
        var modRef = modRefInput.value.trim();
        if (!modRef) return;
        var versionInput = detailRow.querySelector('.mcModGameVersionInput');
        showAlert('Installing mod…');
        installMod(instanceId, modRef, versionInput ? versionInput.value.trim() : '');
      };
      detailRow.querySelectorAll('.mcModDeleteBtn').forEach(function (btn) {
        if (btn.disabled) return;
        btn.onclick = function (e) {
          e.stopPropagation();
          var modName = btn.dataset.modName;
          if (!window.confirm('Remove mod "' + modName + '" from this server?')) return;
          showAlert('Removing mod…');
          deleteMod(instanceId, modName);
        };
      });

      detailRow.querySelectorAll('.mcDeleteServerBtn').forEach(function (btn) {
        btn.onclick = function (e) {
          e.stopPropagation();
          deleteServer(instanceId, instance['ServerId'] || mcGameDisplayName(instance['GameName']));
        };
      });

      detailRow.querySelectorAll('.mcRestartGameBtn').forEach(function (btn) {
        if (btn.disabled) return;
        btn.onclick = function (e) {
          e.stopPropagation();
          showAlert('Restarting the game…');
          restartGame(instanceId);
        };
      });

      var updateGameBtn = detailRow.querySelector('.mcUpdateGameBtn');
      if (updateGameBtn && !updateGameBtn.disabled) updateGameBtn.onclick = function (e) {
        e.stopPropagation();
        if (!window.confirm('Update ' + mcGameDisplayName(instance['GameName']) + ' to the latest version? This briefly restarts the game.')) return;
        showAlert('Updating the game…');
        updateGame(instanceId);
      };

      detailRow.onclick = function (e) { e.stopPropagation(); };

      tbody.appendChild(row);
      tbody.appendChild(detailRow);

      if (previouslySelectedInstanceId && previouslySelectedInstanceId === row.dataset.instanceId) {
        row.classList.add('selected');
        detailRow.classList.remove('hidden');
      }

      // Must follow the appendChild calls above: .focus() is a silent no-op on a detached node, and
      // a scroll offset means nothing until the field has been laid out.
      if (instanceId === previouslySelectedInstanceId) {
        mcRestoreControlFocus(detailRow, previousControls);
      }

      // Deliberately not awaited: updates the dot without blocking the render
      if (dns !== '—') {
        dnsLookup(dns).then(function (resolvedIp) {
          var dot = row.querySelector('[data-dns-dot]');
          if (!dot) return;
          dot.classList.add(resolvedIp === instance['PublicIpAddress'] ? 'match' : 'mismatch');
        });
      }
    });
}

// The field is for the new name; the placeholder carries the one in force.
function mcServerNameClusterHtml(currentName) {
  return mcActionClusterHtml('Server Name',
    '<input class="mcServerNameInput" maxlength="64" autocomplete="off" placeholder="' +
      escapeHtml(currentName || 'Server name') + '">' +
    '<button class="btn mcSaveServerNameBtn">Save</button>');
}

// The presets a world can be generated from, by game. Fetched once: they are the same for every
// server of a game and do not change while the page is open.
var mcWorldDefaults = {};
var mcWorldDefaultsPromise = {};

// mcPlayerRoleFields finds a server's player-role controls as they are on the page now.
function mcPlayerRoleFields(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  var button = detailRow && detailRow.querySelector('.mcPlayerRoleBtn');
  if (!button) return null;
  return { button: button, name: detailRow.querySelector('.mcPlayerNameInput') };
}

//The role reaches the game through its console, so there has to be a game running to hear it.
function mcPlayerRoleClusterHtml(running) {
  var off = running ? '' : ' disabled';
  return mcActionClusterHtml('Player Role',
    '<input type="text" class="mcPlayerNameInput" maxlength="32" autocomplete="off" placeholder="Player"' + off + '>' +
    '<input type="text" class="mcPlayerRoleInput" maxlength="32" autocomplete="off" placeholder="Role"' + off + '>' +
    '<button type="button" class="btn mcPlayerRoleBtn"' + off + '>Grant</button>');
}

function mcWorldPanelHtml() {
  return '<p class="mcModsNote">A new world is generated the next time the game starts, and the ' +
      'world this server is on now stays on its disk. A preset changes only the settings it names.</p>' +
    '<div class="mcWorldForm">' +
      '<input type="text" class="mcWorldNameInput" maxlength="48" autocomplete="off" placeholder="World name">' +
      '<input type="text" class="mcWorldSeedInput" maxlength="64" autocomplete="off" placeholder="Seed (optional)">' +
      '<select class="mcWorldPresetSelect"></select>' +
      '<label class="mcWorldCustomise"><input type="checkbox" class="mcWorldCustomiseBox"> Customise</label>' +
      '<button type="button" class="btn mcNewWorldBtn">Create world</button>' +
    '</div>' +
    '<textarea class="mcWorldConfigInput" rows="16" spellcheck="false" readonly></textarea>';
}

// mcWorldField finds a server's world controls as they are on the page now. A request can outlast
// the rebuild that replaces the ones its click began with.
function mcWorldField(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  var name = detailRow && detailRow.querySelector('.mcWorldNameInput');
  if (!name) return null;
  return {
    name: name,
    seed: detailRow.querySelector('.mcWorldSeedInput'),
    preset: detailRow.querySelector('.mcWorldPresetSelect'),
    customise: detailRow.querySelector('.mcWorldCustomiseBox'),
    config: detailRow.querySelector('.mcWorldConfigInput'),
    button: detailRow.querySelector('.mcNewWorldBtn')
  };
}

async function mcLoadWorldDefaults(instanceId, gameName) {
  if (mcWorldDefaults.hasOwnProperty(gameName)) return mcWorldDefaults[gameName];
  if (!mcWorldDefaultsPromise[gameName]) {
    mcWorldDefaultsPromise[gameName] = (async function () {
      var jwt = await getJwt();
      var res = await fetch(API_URL + 'worlddefaults/' + query_string +
        '&instanceid=' + encodeURIComponent(instanceId), {
        method: 'get', headers: new Headers({ 'Authorization': jwt })
      });
      var data = await res.json();
      if (Array.isArray(data)) { showAlert(data[0]); return []; }
      mcWorldDefaults[gameName] = data.presets || [];
      return mcWorldDefaults[gameName];
    })();
  }
  try {
    return await mcWorldDefaultsPromise[gameName];
  } catch (e) {
    console.log(e);
    mcWorldDefaultsPromise[gameName] = null;
    return [];
  }
}

// mcWorldPresetBody is what a preset would set, for showing in the block.
function mcWorldPresetBody(gameName, name) {
  var presets = mcWorldDefaults[gameName] || [];
  for (var i = 0; i < presets.length; i++) {
    if (presets[i].name === name) return presets[i].body;
  }
  return '';
}

// mcFillWorldPresets puts the presets in the dropdown, once they have arrived.
function mcFillWorldPresets(field, presets) {
  if (!field || field.preset.options.length === presets.length) return;
  field.preset.innerHTML = presets.map(function (preset) {
    return '<option value="' + escapeHtml(preset.name) + '">' + escapeHtml(preset.name) + '</option>';
  }).join('');
}

async function grantPlayerRole(instanceId, player, role) {
  var jwt = await getJwt();
  try {
    var res = await fetch(API_URL + 'playerrole/' + query_string + '&instanceid=' + encodeURIComponent(instanceId), {
      method: 'post',
      headers: new Headers({ 'Authorization': jwt, 'Content-Type': 'application/json' }),
      body: JSON.stringify({ playerName: player, playerRole: role })
    });
    var data = await res.json();
    showAlert(Array.isArray(data) ? data[0] : data);
    return !Array.isArray(data);
  } catch (e) {
    console.log(e);
    showAlert('Could not set that player\'s role');
    return false;
  }
}

async function saveNewWorld(instanceId, name, seed, config) {
  var jwt = await getJwt();
  try {
    var res = await fetch(API_URL + 'newworld/' + query_string + '&instanceid=' + encodeURIComponent(instanceId), {
      method: 'post',
      headers: new Headers({ 'Authorization': jwt, 'Content-Type': 'application/json' }),
      body: JSON.stringify({ worldName: name, worldSeed: seed, worldConfig: config })
    });
    var data = await res.json();
    if (Array.isArray(data)) { showAlert(data[0]); return null; }
    return data.worldName || '';
  } catch (e) {
    console.log(e);
    showAlert('Could not save the new world');
    return null;
  }
}

function mcPasswordClusterHtml() {
  return mcActionClusterHtml('Join Password',
    '<input class="mcPasswordInput" type="password" autocomplete="off" placeholder="Join password">' +
    '<button class="btn mcRevealPasswordBtn">Show</button>' +
    '<button class="btn mcSavePasswordBtn">Save</button>');
}

function mcActionClusterHtml(label, buttonsHtml) {
  return '<div class="mcCluster"><div class="mcClusterLabel">' + label + '</div>' +
    '<div class="mcClusterButtons">' + buttonsHtml + '</div></div>';
}

function mcKvHtml(label, value) {
  return '<div><div class="mcKvLabel">' + label + '</div><div class="mcKvVal">' + value + '</div></div>';
}

function toggleServerRow(rowEl) {
  var detailRow = rowEl.nextElementSibling;
  var wasSelected = rowEl.classList.contains('selected');
  document.querySelectorAll('tr.mcServerRow').forEach(function (r) { r.classList.remove('selected'); });
  document.querySelectorAll('tr.mcServerDetail').forEach(function (r) { r.classList.add('hidden'); });
  if (!wasSelected) {
    rowEl.classList.add('selected');
    detailRow.classList.remove('hidden');
    // A closed panel keeps its row in the table, so without this a server opened again comes back on
    // the tab it was left on, with its join password still on screen.
    mcSwitchTab(detailRow, 'overview');
    mcMaskPassword(rowEl.dataset.instanceId, detailRow);
  }
}

// mcMaskPassword puts a server's join password field back to how it starts: empty and masked.
function mcMaskPassword(instanceId, detailRow) {
  var input = detailRow.querySelector('.mcPasswordInput');
  if (!input) return;
  mcPasswordShown[instanceId] = false;
  input.value = '';
  mcShowPasswordState(instanceId, input, detailRow.querySelector('.mcRevealPasswordBtn'));
}

// mcServerNameField finds a server's name field as it is on the page now.
function mcServerNameField(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  return detailRow && detailRow.querySelector('.mcServerNameInput');
}

// mcPasswordField finds a server's join password controls as they are on the page now. A request can
// outlast the rebuild that replaces the controls its click began with, and writing to a detached one
// leaves nothing on screen.
function mcPasswordField(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  var input = detailRow && detailRow.querySelector('.mcPasswordInput');
  if (!input) return null;
  return { input: input, revealBtn: detailRow.querySelector('.mcRevealPasswordBtn') };
}

// mcShowPasswordState makes the field and its button agree with mcPasswordShown.
function mcShowPasswordState(instanceId, input, revealBtn) {
  var shown = !!mcPasswordShown[instanceId];
  input.type = shown ? 'text' : 'password';
  if (revealBtn) revealBtn.textContent = shown ? 'Hide' : 'Show';
}

function showAlert(text) {
      var al = document.getElementsByClassName("alert");
    document.getElementsByClassName("alertmsg")[0].innerHTML = text;
    al[0].style.display = 'block';
}

function dropdownMenu() {
  var ddc = document.getElementById("dropdownClick");
  if (ddc.className === "top-nav") {
    ddc.className += " responsive";
  } else {
    ddc.className = "top-nav"
  }
}

async function stopServer(instanceId) {
  var stopUrl = API_URL + "stop/" + query_string + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "")
  //checkLogin();
  var jwt = await getJwt();

  var msg = await fetch(stopUrl, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

async function startServer(instanceId) {
  var startUrl = API_URL + "start/" + query_string + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "")
  //checkLogin();
  var jwt = await getJwt();

  var msg = await fetch(startUrl, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

async function resizeServer(size, instanceId) {
  var resizeUrl = API_URL + "resize/" + query_string + "&resize=" + encodeURIComponent(size) + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "")
  //checkLogin();
  var jwt = await getJwt();

  var msg = await fetch(resizeUrl, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<5; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

// An array answer is the [message, state] pair every other command uses: a refusal or a failure.
async function fetchGamePassword(instanceId) {
  var jwt = await getJwt();
  try {
    var res = await fetch(API_URL + 'gamepassword/' + query_string + '&instanceid=' + encodeURIComponent(instanceId), {
      method: 'get',
      headers: new Headers({ 'Authorization': jwt })
    });
    var data = await res.json();
    if (Array.isArray(data)) { showAlert(data[0]); return null; }
    return data.password || '';
  } catch (e) {
    console.log(e);
    showAlert('Could not read the join password');
    return null;
  }
}

async function saveServerName(instanceId, name) {
  var jwt = await getJwt();
  try {
    var res = await fetch(API_URL + 'setservername/' + query_string + '&instanceid=' + encodeURIComponent(instanceId), {
      method: 'post',
      headers: new Headers({ 'Authorization': jwt, 'Content-Type': 'application/json' }),
      body: JSON.stringify({ serverName: name })
    });
    var data = await res.json();
    if (Array.isArray(data)) { showAlert(data[0]); return null; }
    return data.serverName || '';
  } catch (e) {
    console.log(e);
    showAlert('Could not save the server name');
    return null;
  }
}

async function saveGamePassword(instanceId, password) {
  var jwt = await getJwt();
  try {
    var res = await fetch(API_URL + 'setgamepassword/' + query_string + '&instanceid=' + encodeURIComponent(instanceId), {
      method: 'post',
      headers: new Headers({ 'Authorization': jwt, 'Content-Type': 'application/json' }),
      body: JSON.stringify({ password: password })
    });
    var data = await res.json();
    if (Array.isArray(data)) { showAlert(data[0]); return null; }
    return data.password || '';
  } catch (e) {
    console.log(e);
    showAlert('Could not save the join password');
    return null;
  }
}

async function backupNow(instanceId) {
  var backupUrl = API_URL + "backupnow/" + query_string + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "")
  var jwt = await getJwt();

  var msg = await fetch(backupUrl, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

// modRef is whatever the admin pasted, resolved to an actual download server-side.
async function installMod(instanceId, modRef, gameVersion) {
  var url = API_URL + "installmod/" + query_string +
    (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "") + "&modref=" + encodeURIComponent(modRef) +
    "&modgameversion=" + encodeURIComponent(gameVersion || '');
  var jwt = await getJwt();

  var msg = await fetch(url, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

async function deleteMod(instanceId, modName) {
  var url = API_URL + "deletemod/" + query_string +
    (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "") + "&modname=" + encodeURIComponent(modName);
  var jwt = await getJwt();

  var msg = await fetch(url, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

async function restartGame(instanceId) {
  var url = API_URL + "restartgame/" + query_string + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "");
  var jwt = await getJwt();

  var msg = await fetch(url, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

// The update restarts the game as part of what it does, so no restartGame call is needed after it.
async function updateGame(instanceId) {
  var url = API_URL + "updategame/" + query_string + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "");
  var jwt = await getJwt();

  var msg = await fetch(url, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

async function pauseIdleShutdown(instanceId) {
  await setIdleShutdown("pauseidleshutdown", instanceId);
}

async function resumeIdleShutdown(instanceId) {
  await setIdleShutdown("resumeidleshutdown", instanceId);
}

async function setIdleShutdown(path, instanceId) {
  var url = API_URL + path + "/" + query_string + (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "")
  var jwt = await getJwt();

  var msg = await fetch(url, {
    method: 'get',
    headers: new Headers({
      'Authorization': jwt
    })
  });

  var msgdata = await msg.json();
  showAlert(msgdata[0]);

  for (y=0; y<6; y++){
    await sleep(1000);
    mcInfo(API_URL, jwt);
  }
}

// Starts a server-side zip, then polls until a presigned download URL comes back. Only works while the
// instance is running.
async function downloadWorld(instanceId, scope) {
  scope = scope || 'full';
  worldDownloads[instanceId] = { status: 'InProgress', attempt: 0, scope: scope };
  mcRefreshWorldDownloadStatusUi(instanceId);
  var jwt = await getJwt();

  var startUrl = API_URL + "downloadworldstart/" + query_string +
    "&instanceid=" + encodeURIComponent(instanceId) + "&scope=" + encodeURIComponent(scope);
  var startResp = await fetch(startUrl, { method: 'get', headers: new Headers({ 'Authorization': jwt }) });
  var startData = await startResp.json();

  if (startData.status === 'Failed') {
    worldDownloads[instanceId] = { status: 'Failed', error: startData.error || 'could not start', scope: scope };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }

  pollWorldDownloadStatus(instanceId, startData.commandId, startData.s3Key, 0, scope);
}

function retryWorldDownload(instanceId) {
  var dl = worldDownloads[instanceId];
  if (!dl || !dl.commandId) return;
  worldDownloads[instanceId] = { status: 'InProgress', attempt: 0, scope: dl.scope };
  mcRefreshWorldDownloadStatusUi(instanceId);
  pollWorldDownloadStatus(instanceId, dl.commandId, dl.s3Key, 0, dl.scope);
}

// Zipping and uploading a multi-GB world takes longer than API Gateway will hold a request open (29s),
// so progress comes from a separate status endpoint rather than one blocking call.
async function pollWorldDownloadStatus(instanceId, commandId, s3Key, attempt, scope) {
  var maxAttempts = 60; // 60 * 5s = 5 minutes
  var jwt = await getJwt();
  var statusUrl = API_URL + "downloadworldstatus/" + query_string +
    "&instanceid=" + encodeURIComponent(instanceId) +
    "&commandid=" + encodeURIComponent(commandId) +
    "&s3key=" + encodeURIComponent(s3Key);
  var resp = await fetch(statusUrl, { method: 'get', headers: new Headers({ 'Authorization': jwt }) });
  var data = await resp.json();

  if (data.status === 'Success') {
    worldDownloads[instanceId] = { status: 'Success', url: data.url, scope: scope };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }
  if (data.status === 'Failed') {
    worldDownloads[instanceId] = { status: 'Failed', error: data.error, scope: scope };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }
  if (attempt >= maxAttempts) {
    worldDownloads[instanceId] = { status: 'RetryPrompt', commandId: commandId, s3Key: s3Key, scope: scope };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }
  worldDownloads[instanceId] = { status: 'InProgress', attempt: attempt + 1, scope: scope };
  mcRefreshWorldDownloadStatusUi(instanceId);
  await sleep(5000);
  pollWorldDownloadStatus(instanceId, commandId, s3Key, attempt + 1, scope);
}

async function dnsLookup(dN) {
  var json = await fetch('https://cloudflare-dns.com/dns-query?name=' + dN, {    
    method: 'get',
    headers: new Headers({
    'accept': 'application/dns-json'
  })}).then(response => response.json());
  var DomainName2IP = ""
  try {
    DomainName2IP = json["Answer"][0]["data"];
  } catch(e) {
    console.log("DNS Lookup Failed for " + dN)
  }
  return(DomainName2IP);
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function updatelinks() {
  document.getElementById("home").setAttribute("href", mcCloudfrontUrl);
  var access = document.getElementById("accessLink");
  if (access) access.setAttribute("href", mcCloudfrontUrl + "/access.html");
}

//The Lambda rejects both access commands from a non-admin regardless; hiding the link just keeps the nav
//honest about what's reachable.
function updateAccessLink() {
  var access = document.getElementById("accessNav");
  if (access) access.style.display = mcIsAdmin ? '' : 'none';
}

//The button stays hidden until a reply says this caller may create a server, so the handlers are
//wired whether or not they ever can.
function mcWireNewServerForm() {
  var button = document.getElementById('mcNewServerBtn');
  if (!button) return;
  button.style.display = 'none';

  var f = mcServerOptionsForm();
  button.onclick = function () {
    toggleNewServerForm(f.form.style.display === 'none');
  };
  document.getElementById('mcNewServerCancel').onclick = function () {
    toggleNewServerForm(false);
  };
  f.form.onsubmit = function (event) {
    event.preventDefault();
    createServer();
  };
}

async function updateAuthButtons() {
  const loggedIn = await isLoggedIn();
  const signInBtn = document.getElementById('signInBtn');
  const signOutBtn = document.getElementById('signOutBtn');
  if (signInBtn) signInBtn.style.display = loggedIn ? 'none' : 'block';
  if (signOutBtn) signOutBtn.style.display = loggedIn ? 'block' : 'none';
}


//////////////////// Cognito //////////////////////////



async function init() {
  const { Auth } = aws_amplify_auth;
  const { Amplify } = aws_amplify_core;

  updatelinks();

  Amplify.configure(aws_auth_config)

  await authIfNeeded();
  await updateAuthButtons();
  await loadIsAdmin();
  updateAccessLink();
  mcWireNewServerForm();

  refreshData();
  setInterval(refreshData, 5000);

  async function loadIsAdmin() {
    try {
      const session = await Auth.currentSession();
      const groups = session.getIdToken().payload['cognito:groups'] || [];
      mcIsAdmin = groups.indexOf('admins') !== -1;
    } catch (e) {
      console.log(e);
      mcIsAdmin = false;
    }
  }

  async function authIfNeeded() {
    try {     
      await Auth.currentAuthenticatedUser();
      console.log("True")
      return true
    }   
    catch(e) {
      console.log("False")
      Auth.federatedSignIn({
        provider: 'COGNITO',
        domain: mcCognitoDomainName
        });     
      return false   
    }
  } 


  async function getJwt2() {
    var jwt = Auth.currentSession()
        .then(res=>{
        
        let IdToken = res.getIdToken()
        let resJwt = IdToken.getJwtToken()

        return resJwt
        })
        .catch(e => {console.log(e)})
    return jwt
  }

  async function refreshData() {
    var jwt2 = await getJwt2()
    return mcInfo(API_URL, jwt2);
  }


}

async function checkLogin() {
  if (!await isLoggedIn()) {
    DoSignIn()
  }
}

function DoSignIn() {
  const { Auth } = aws_amplify_auth;
  Auth.federatedSignIn({
    provider: 'COGNITO',
    domain: mcCognitoDomainName
});
}

async function isLoggedIn() {
  try {     
    const { Auth } = aws_amplify_auth;
    await Auth.currentAuthenticatedUser();
    console.log("True")
    return true
  }   
  catch(e) {
    console.log("False")     
    return false   
  } 
}

async function getJwt() {
  const { Amplify } = aws_amplify_core;
  const { Auth } = aws_amplify_auth;
  var jwt = Auth.currentSession()
      .then(res=>{

      let IdToken = res.getIdToken()
      let resJwt = IdToken.getJwtToken()

      return resJwt
      })
      .catch(e => {console.log(e)})
  return jwt
}

function logout() {
    const { Amplify } = aws_amplify_core;
    const { Auth } = aws_amplify_auth;

    Amplify.configure(aws_auth_config)
    Auth.signOut({ global: true });
}

// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

//Whether the signed-in Cognito user is in the "admins" group - fetched once in init() from the ID
//token's own cognito:groups claim. This only drives which buttons render; the Lambda re-checks
//independently and is the real security boundary (see gaming_server_start_stop-v1_0.py).
var mcIsAdmin = false;

// Defining async function taking in JWT and URL
async function mcInfo(url, idToken) {

    //mcInfo
    var mcInfodata = API_URL + "getinfo/" + query_string;
    
    // Storing response
    const response = await fetch(mcInfodata, {
      method: 'get',
      headers: new Headers({
        'Authorization': idToken
      })
    });
    
    //Storing data in form of JSON
    var data = await response.json();
    renderTable(data);
}

// Maps raw instance State to a badge CSS class
//GameName (cfn/server-stack.yaml's GameName parameter) is a lowercase-hyphen slug used to name AWS
//resources - display a human-readable name instead. Unknown/future games fall back to a capitalized
//version of the slug rather than showing nothing.
var MC_GAME_DISPLAY_NAMES = {
  'valheim': 'Valheim',
  'vintagestory': 'Vintage Story'
};
function mcGameDisplayName(gameName) {
  if (!gameName) return '—';
  if (MC_GAME_DISPLAY_NAMES[gameName]) return MC_GAME_DISPLAY_NAMES[gameName];
  return gameName.charAt(0).toUpperCase() + gameName.slice(1);
}

// What an admin pastes into "Install Mod" differs per game's own mod site - see
// docs/frontend-ui-ux-requirements.md's "Install/delete mods" section for the back-end side of this
// (installmod/deletemod endpoints don't exist yet).
var MC_MOD_SOURCE_HINT = {
  'vintagestory': { placeholder: 'e.g. carrycapacity', label: 'Mod ID or slug from mods.vintagestory.at' },
  'valheim': { placeholder: 'e.g. Smoothbrain-EquipmentAndQuickSlots', label: 'Full package name from thunderstore.io (Namespace-Name)' }
};
function mcModSourceHint(gameName) {
  return MC_MOD_SOURCE_HINT[gameName] || { placeholder: 'mod identifier', label: 'Mod identifier' };
}

// The EC2 instance can be running before/without the game process itself being up (still installing,
// crash-looping, etc) - this is deliberately a separate badge from the EC2 State one. See docs/server-status.md.
function mcGameStatus(instance) {
  if (instance['State'] !== 'running') {
    return { text: '—', badgeClass: null };
  }
  var serviceStatus = instance['GameStatus'] && instance['GameStatus'].serviceStatus;
  if (serviceStatus === 'active') return { text: 'Running', badgeClass: 'running' };
  if (serviceStatus === 'failed') return { text: 'Crashed', badgeClass: 'error' };
  if (serviceStatus === 'inactive') return { text: 'Stopped', badgeClass: 'stopped' };
  return { text: 'Starting…', badgeClass: 'pending' };
}

// IdleShutdownStatus: 'disabled' (paused, e.g. for maintenance), 'enabled' (15-min idle check running,
// hasn't seen an empty check yet), or 'triggered-once' (one empty check already recorded - the *next*
// empty check will actually stop the instance).
function mcIdleShutdownStatus(instance) {
  var status = instance['IdleShutdownStatus'];
  if (status === 'disabled') return { text: 'Paused', badgeClass: 'stopped' };
  if (status === 'triggered-once') return { text: 'Active (Idle)', badgeClass: 'pending' };
  if (status === 'enabled') return { text: 'Active', badgeClass: 'running' };
  return { text: '—', badgeClass: null };
}

// Single at-a-glance status combining EC2 State, GameStatus.serviceStatus and IdleShutdownStatus - replaces
// showing State/Game Status/Auto-Shutdown as three separate columns. Only surfaces "Idle" once idle-shutdown
// has actually recorded an empty check (IdleShutdownStatus 'triggered-once' - see mcIdleShutdownStatus above);
// idle-shutdown merely being turned on isn't itself notable enough for the main State column, so that stays
// visible only in the Overview tab's Auto-Shutdown field.
function mcCombinedState(instance) {
  var ec2State = instance['State'];
  if (ec2State !== 'running') {
    if (ec2State === 'stopped') return { text: 'Stopped', badgeClass: 'stopped' };
    var label = ec2State ? ec2State.charAt(0).toUpperCase() + ec2State.slice(1).replace(/-/g, ' ') : 'Unknown';
    return { text: label + ' (EC2)', badgeClass: 'pending' };
  }
  var serviceStatus = instance['GameStatus'] && instance['GameStatus'].serviceStatus;
  if (serviceStatus === 'failed') return { text: 'Error', badgeClass: 'error' };
  if (serviceStatus === 'inactive') return { text: 'Stopped (Game)', badgeClass: 'stopped' };
  if (serviceStatus !== 'active') return { text: 'Starting (Game)', badgeClass: 'pending' };
  if (instance['IdleShutdownStatus'] === 'triggered-once') return { text: 'Running (Idle)', badgeClass: 'idle' };
  return { text: 'Running', badgeClass: 'running' };
}

// The resize Lambda maps this value through a fixed set of environment variables (micro/small/medium/large
// -> t3a.*, see cfn/control-panel.yaml's StartStopLambda) rather than accepting an EC2 instance type string
// directly, so the <option> value stays the slug the backend expects - only the label changes to the real
// type it resolves to. xlarge/2xlarge are left out because the Lambda has no mapping for them yet.
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

// One state-aware Start/Stop button rather than always showing both - disabled with a transitional label
// while EC2 itself is mid-transition, since neither action applies until that settles. `permissionDenied`
// is for the table's quick-action button only (see mcActionClusterHtml callers in the Actions tab, which
// omit the button entirely instead) - it keeps the button visible but inert, with a tooltip explaining why,
// rather than the table's column layout shifting per-row based on who's looking at it.
function mcLifecycleButtonHtml(ec2State, permissionDenied) {
  var label, cls;
  if (ec2State === 'stopped') { label = 'Start'; cls = 'start'; }
  else if (ec2State === 'running') { label = 'Stop'; cls = 'stop'; }
  else { label = ec2State === 'pending' ? 'Starting…' : 'Stopping…'; cls = 'disabled'; }
  var disabled = permissionDenied || cls === 'disabled';
  var title = permissionDenied ? ' title="You don\'t have permission to control this server"' : '';
  return '<button class="btn ' + cls + ' mcLifecycleBtn"' + (disabled ? ' disabled' : '') + title + '>' + label + '</button>';
}

// Restarts just the game process/container, not the EC2 instance - e.g. to pick up a mod just
// installed/removed or a hand-edited config, without the ~minutes-long cost of a full Stop/Start.
// Admin-only (restartGame falls under the Lambda's generic admin-only permission bucket, same as Resize/
// Backup Now/mods - not the UserLifecycleAllowed tag Start/Stop use), so gated by mcIsAdmin at the call
// site, not shown here. `enabled` is false while the instance isn't running, same treatment as the other
// lifecycle/mod controls.
function mcRestartGameButtonHtml(enabled) {
  var title = enabled ? 'Restart just the game process, not the EC2 instance' : 'Start the server to restart the game';
  return '<button type="button" class="btn mcRestartGameBtn"' + (enabled ? '' : ' disabled') + ' title="' + title + '">Restart Game</button>';
}

// `scope` is 'full' (world save + mods + player data - everything WorldDownloadPaths lists) or 'core'
// (world save only). Only one download can run at a time per instance either way - the SSM command zips
// to a fixed /tmp path on the instance, so a second one while the first is still running would collide -
// hence disabling every download button (both scopes, table and Actions tab alike) whenever any download
// for this instance is in progress, not just the one that was clicked.
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

// Formats "time since a timestamp" - used for both LastBackupTime (AWS Backup's own schedule, independent
// of the instance's State) and lastSaveTime (the game's own last autosave - a different concept: one is
// infrastructure-level EBS backup, the other is what the game itself last wrote to its world file).
function mcRelativeTimeText(timestamp) {
  if (!timestamp) return '—';
  var diffMinutes = Math.floor((Date.now() - new Date(timestamp).getTime()) / (1000 * 60));
  if (diffMinutes < 1) return '<1m ago';
  if (diffMinutes < 60) return diffMinutes + 'm ago';
  var diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) return diffHours + 'h ago';
  return Math.floor(diffHours / 24) + 'd ago';
}

// Per-instance world-download state, kept outside the DOM: the table body gets fully torn down and
// rebuilt on every periodic refresh (see renderTable's `tbody.innerHTML = ''`), which would otherwise wipe
// out any in-progress download status a couple seconds after it appeared. Read by renderTable on every
// rebuild (same idea as the existing previouslySelectedInstanceId handling) and updated live by the poll
// loop in between rebuilds.
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

// Pushes the current worldDownloads state into the live DOM, if that row still exists right now (it
// usually does - this just avoids waiting for the next periodic refresh to reflect a status change).
function mcRefreshWorldDownloadStatusUi(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  var inProgress = worldDownloads[instanceId] && worldDownloads[instanceId].status === 'InProgress';

  // The table row's quick button and the Actions tab's duplicates both need the same in-progress state.
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

// mods: array of {name, version} from GameStatus - see docs/server-status.md. `canDelete` shows a remove
// icon per mod (admin-only - see canManageMods in renderTable); `enabled` disables it while the instance
// isn't running, same treatment as the lifecycle/download buttons (visible but inert, not hidden).
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

// Placed below the mod list/form - `gameName` picks the right hint text for what to paste (see
// mcModSourceHint above); `enabled` is false while the EC2 instance isn't running (mod install/delete
// goes through SSM Run Command on the instance, the same as Backup Now/Download World).
function mcInstallModFormHtml(gameName, enabled) {
  var hint = mcModSourceHint(gameName);
  return '<div class="mcInstallModForm">' +
    '<input type="text" class="mcModRefInput" placeholder="' + escapeHtml(hint.placeholder) + '"' + (enabled ? '' : ' disabled') + '>' +
    '<button type="button" class="btn mcInstallModBtn"' + (enabled ? '' : ' disabled') + '>Install</button>' +
    '</div>' +
    '<p class="mcModSourceHint">' + escapeHtml(hint.label) + (enabled ? '' : ' — start the server to install or remove mods') + '</p>';
}

// Renders one row per instance, preserving which row (if any) is currently expanded
async function renderTable(data) {
    var instances = (data && data[1] && data[1]["Instances"]) || [];
    var tbody = document.getElementById('mcServerTableBody');
    var previouslySelectedRow = tbody.querySelector('tr.mcServerRow.selected');
    var previouslySelectedInstanceId = previouslySelectedRow ? previouslySelectedRow.dataset.instanceId : null;
    // Scoped to the selected row's own detail panel - querying the whole tbody would find whichever
    // row happens to come first in the list (still showing its default Overview tab), not the tab the
    // user actually has open on the expanded row, and periodic refreshes would keep snapping back to it.
    var previouslyActiveDetail = previouslySelectedRow && previouslySelectedRow.nextElementSibling;
    var previouslyActiveTabEl = previouslyActiveDetail && previouslyActiveDetail.querySelector('.mcTab.active');
    var previouslyActiveTab = previouslyActiveTabEl ? previouslyActiveTabEl.dataset.tab : 'overview';
    // Same rebuild-wipes-live-state problem as the selection/tab above, but for text actually being typed:
    // the periodic refresh tears down and rebuilds every row's markup from scratch, which would otherwise
    // silently erase whatever the admin is mid-typing into the Install Mod field a few seconds later.
    var previouslyModRefEl = previouslyActiveDetail && previouslyActiveDetail.querySelector('.mcModRefInput');
    var previouslyModRefValue = previouslyModRefEl ? previouslyModRefEl.value : '';
    var previouslyModRefFocused = previouslyModRefEl === document.activeElement;

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
      var canLifecycle = mcIsAdmin || instance['UserLifecycleAllowed'];
      var canDownload = mcIsAdmin || instance['UserDownloadsAllowed'];
      // Install/delete rewrites the server's actual content, not just start/stop or a read-only download -
      // admin-only, no per-user override (see docs/frontend-ui-ux-requirements.md). Separate from whether
      // the instance is actually running right now, which just disables the controls (same treatment as
      // mcLifecycleButtonHtml/mcDownloadWorldButtonHtml - visible but inert, not hidden).
      var canManageMods = mcIsAdmin;
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
        '<td class="mcMuted">—</td>' +
        '<td><span class="mcBadge ' + combinedState.badgeClass + '">' + escapeHtml(combinedState.text) + '</span></td>' +
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

      var actionClusters =
        (canLifecycle ? mcActionClusterHtml('Power', mcLifecycleButtonHtml(ec2State)) : '') +
        (mcIsAdmin ?
          mcActionClusterHtml('Capacity',
            '<select class="mcResizeSelect">' + mcResizeOptionsHtml(instance['InstanceType']) + '</select>' +
            '<button class="btn primary">Resize</button>') : '') +
        ((mcIsAdmin || canDownload) ?
          mcActionClusterHtml('Data',
            (mcIsAdmin ? '<button class="btn mcBackupNowBtn">Backup Now</button>' : '') +
            (canDownload ?
              mcDownloadWorldButtonHtml(instanceId, 'full', 'Download (All Data)') +
              mcDownloadWorldButtonHtml(instanceId, 'core', 'Download (Just World)') +
              '<span class="mcWorldDownloadStatus">' + mcWorldDownloadStatusHtml(instanceId) + '</span>' : '') +
            // Restarts the game process, not EC2 power state - Data rather than Power since it's grouped
            // with the other "acts on the server's live content/state" actions, not instance lifecycle.
            (mcIsAdmin ? mcRestartGameButtonHtml(modsRunning) : '')) : '') +
        (mcIsAdmin ?
          mcActionClusterHtml('Automation',
            '<button class="btn mcIdleShutdownToggleBtn">' + (idlePaused ? 'Resume Auto-Shutdown' : 'Pause Auto-Shutdown') + '</button>' +
            '<span class="mcMuted">Auto-shutdown: ' + escapeHtml(idleStatus.text) + '</span>') : '');

      var overviewHtml =
        mcKvHtml('Game', mcGameDisplayName(instance['GameName'])) +
        mcKvHtml('Server Name', '—') +
        mcKvHtml('EC2 State', escapeHtml(ec2State || '—')) +
        mcKvHtml('Game State', escapeHtml(gameStatus.text)) +
        mcKvHtml('Auto-Shutdown', escapeHtml(idleStatus.text)) +
        mcKvHtml('Players', playersText) +
        mcKvHtml('Last Save', lastSaveText) +
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
        '</div>' +
        '<div class="mcTabPanel mcKvGrid" data-tab-panel="overview">' + overviewHtml + '</div>' +
        '<div class="mcTabPanel mcActionClusters" data-tab-panel="actions">' + actionClusters + '</div>' +
        '<div class="mcTabPanel mcModsPanel" data-tab-panel="mods">' +
          renderModsList(status && status.mods, canManageMods, modsRunning) +
          (canManageMods ?
            mcInstallModFormHtml(instance['GameName'], modsRunning) +
            '<p class="mcModsNote">Installing or removing a mod here can take a minute or two to show up in the list above. ' +
            'Changes only take effect after a restart:</p>' +
            mcRestartGameButtonHtml(modsRunning)
            : '') +
        '</div>' +
        '</td>';

      detailRow.querySelectorAll('.mcTab').forEach(function (tab) {
        tab.onclick = function (e) { e.stopPropagation(); mcSwitchTab(detailRow, tab.dataset.tab); };
      });
      // Only the row that was actually open keeps its tab - every other (hidden) row starts fresh on
      // Overview rather than inheriting whatever tab happened to be open elsewhere.
      mcSwitchTab(detailRow, instanceId === previouslySelectedInstanceId ? previouslyActiveTab : 'overview');

      var select = detailRow.querySelector('select');
      var resizeBtn = detailRow.querySelector('.primary');
      var backupBtn = detailRow.querySelector('.mcBackupNowBtn');
      var idleToggleBtn = detailRow.querySelector('.mcIdleShutdownToggleBtn');

      // Both the table row's quick lifecycle/download buttons and their Actions-tab duplicates get the
      // same handler - clicking either does the same thing.
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
      if (modRefInput && instanceId === previouslySelectedInstanceId && previouslyModRefValue) {
        modRefInput.value = previouslyModRefValue;
        if (previouslyModRefFocused) {
          modRefInput.focus();
          modRefInput.setSelectionRange(modRefInput.value.length, modRefInput.value.length);
        }
      }
      var installModBtn = detailRow.querySelector('.mcInstallModBtn');
      if (installModBtn && !installModBtn.disabled) installModBtn.onclick = function (e) {
        e.stopPropagation();
        var modRef = modRefInput.value.trim();
        if (!modRef) return;
        showAlert('Installing mod…');
        installMod(instanceId, modRef);
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

      // Appears both in the Power cluster and again in the Mods tab (see mcRestartGameButtonHtml) -
      // clicking either does the same thing.
      detailRow.querySelectorAll('.mcRestartGameBtn').forEach(function (btn) {
        if (btn.disabled) return;
        btn.onclick = function (e) {
          e.stopPropagation();
          showAlert('Restarting the game…');
          restartGame(instanceId);
        };
      });

      detailRow.onclick = function (e) { e.stopPropagation(); };

      tbody.appendChild(row);
      tbody.appendChild(detailRow);

      if (previouslySelectedInstanceId && previouslySelectedInstanceId === row.dataset.instanceId) {
        row.classList.add('selected');
        detailRow.classList.remove('hidden');
      }

      // Async DNS-vs-actual-IP check; updates the dot without blocking the initial render
      if (dns !== '—') {
        dnsLookup(dns).then(function (resolvedIp) {
          var dot = row.querySelector('[data-dns-dot]');
          if (!dot) return;
          dot.classList.add(resolvedIp === instance['PublicIpAddress'] ? 'match' : 'mismatch');
        });
      }
    });
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
  }
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
    // Change top-nav to top-nav.responsive on Click
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

// NOTE: installmod/deletemod don't exist on the back end yet - see docs/frontend-ui-ux-requirements.md's
// "Install/delete mods" section. modRef is whatever the admin pasted (a ModDB id/slug for Vintage Story, a
// "Namespace-Name" package name for Valheim - see mcModSourceHint) and is resolved to a download server-side.
async function installMod(instanceId, modRef) {
  var url = API_URL + "installmod/" + query_string +
    (instanceId ? "&instanceid=" + encodeURIComponent(instanceId) : "") + "&modref=" + encodeURIComponent(modRef);
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

// Restarts just the game process/container - the EC2 instance itself stays running throughout, so this
// is a few seconds to kick off rather than the minutes a full Stop/Start takes. See restartGame in
// Lambda/gaming_server_start_stop-v1_0.py.
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

// Zips specific subfolders of the currently-live data directory via SSM Run Command (only works while the
// server is running) and uploads the result to S3 - see docs/server-status.md and the Control Panel design
// notes. Kicked off by downloadWorld, then polled by pollWorldDownloadStatus until a presigned download URL
// comes back.
// State lives in worldDownloads (not a captured DOM element) since the periodic refresh rebuilds the
// whole table every few seconds - see the comment on worldDownloads itself.
// `scope` is 'full' (everything WorldDownloadPaths lists - mods, player data, world save) or 'core'
// (world save only) - see mcDownloadWorldButtonHtml above. NOTE: the downloadworldstart Lambda doesn't
// read this parameter yet - see docs/frontend-ui-ux-requirements.md's "Core vs. full world download"
// section - so until that's wired up, 'core' produces the same (full) archive as 'full' does.
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

// Zipping+uploading a multi-GB world takes an unbounded amount of time, and API Gateway can't hold a
// request open past 29s anyway - so this polls a separate status endpoint rather than blocking on one call.
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

        //You can print them to see the full objects
        //console.log(`myIdToken: ${JSON.stringify(IdToken)}`)
        //console.log(`myJwt: ${resJwt}`)
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

      //You can print them to see the full objects
      //console.log(`myIdToken: ${JSON.stringify(IdToken)}`)
      //console.log(`myJwt: ${resJwt}`)
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

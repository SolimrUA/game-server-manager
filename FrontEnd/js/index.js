// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

//Only drives which buttons render - the Lambda re-checks the caller's groups itself and is the real
//security boundary.
var mcIsAdmin = false;

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

// 'triggered-once' means one empty check is already recorded, so the next one stops the instance.
function mcIdleShutdownStatus(instance) {
  var status = instance['IdleShutdownStatus'];
  if (status === 'disabled') return { text: 'Paused', badgeClass: 'stopped' };
  if (status === 'triggered-once') return { text: 'Active (Idle)', badgeClass: 'pending' };
  if (status === 'enabled') return { text: 'Active', badgeClass: 'running' };
  return { text: '—', badgeClass: null };
}

// Auto-shutdown merely being enabled isn't worth surfacing here; it stays in the Overview tab.
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

// The resize Lambda takes a slug, not an instance type: it resolves one through a fixed
// micro/small/medium/large -> t3a.* map of environment variables (cfn/control-panel.yaml's
// StartStopLambda), so a size with no mapping there can't be offered here.
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

function mcInstallModFormHtml(gameName, enabled) {
  var hint = mcModSourceHint(gameName);
  return '<div class="mcInstallModForm">' +
    '<input type="text" class="mcModRefInput" placeholder="' + escapeHtml(hint.placeholder) + '"' + (enabled ? '' : ' disabled') + '>' +
    '<button type="button" class="btn mcInstallModBtn"' + (enabled ? '' : ' disabled') + '>Install</button>' +
    '</div>' +
    '<p class="mcModSourceHint">' + escapeHtml(hint.label) + (enabled ? '' : ' — start the server to install or remove mods') + '</p>';
}

async function renderTable(data) {
    var instances = (data && data[1] && data[1]["Instances"]) || [];
    var tbody = document.getElementById('mcServerTableBody');
    var previouslySelectedRow = tbody.querySelector('tr.mcServerRow.selected');
    var previouslySelectedInstanceId = previouslySelectedRow ? previouslySelectedRow.dataset.instanceId : null;
    // Scoped to the selected row's own panel: querying the whole tbody would find whichever row comes
    // first (still on its default Overview tab), and every refresh would snap the open row back to it.
    var previouslyActiveDetail = previouslySelectedRow && previouslySelectedRow.nextElementSibling;
    var previouslyActiveTabEl = previouslyActiveDetail && previouslyActiveDetail.querySelector('.mcTab.active');
    var previouslyActiveTab = previouslyActiveTabEl ? previouslyActiveTabEl.dataset.tab : 'overview';
    // Same rebuild problem, applied to text being typed: without this, the refresh erases whatever is
    // half-entered in the Install Mod field.
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
      // Installing or deleting a mod rewrites the server's content: admin-only, no per-user override.
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
            // Grouped under Data, not Power: these act on the server's content, not its power state.
            (mcIsAdmin ? mcRestartGameButtonHtml(modsRunning) + mcUpdateGameButtonHtml(modsRunning) : '')) : '') +
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
      if (modRefInput && instanceId === previouslySelectedInstanceId && previouslyModRefValue) {
        modRefInput.value = previouslyModRefValue;
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

      // Must follow the appendChild calls above: .focus() is a silent no-op on a detached node.
      if (modRefInput && previouslyModRefFocused && instanceId === previouslySelectedInstanceId) {
        modRefInput.focus();
        modRefInput.setSelectionRange(modRefInput.value.length, modRefInput.value.length);
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

// modRef is whatever the admin pasted, resolved to an actual download server-side.
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

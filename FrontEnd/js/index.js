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

function mcDownloadWorldButtonHtml(instanceId, permissionDenied) {
  var inProgress = worldDownloads[instanceId] && worldDownloads[instanceId].status === 'InProgress';
  var disabled = inProgress || permissionDenied;
  var title = permissionDenied ? ' title="You don\'t have permission to download this server\'s world"' : '';
  return '<button class="btn mcDownloadWorldBtn"' + (disabled ? ' disabled' : '') + title + '>' +
    (inProgress ? 'Downloading…' : 'Download World') + '</button>';
}

var MC_COPY_ICON_SVG = '<svg width="12" height="12" viewBox="0 0 14 14" fill="none">' +
  '<rect x="4" y="4" width="8" height="9" rx="1.5" stroke="currentColor" stroke-width="1.3"/>' +
  '<path d="M3 9.5V2.5C3 1.94772 3.44772 1.5 4 1.5H9" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>' +
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
  if (dl.status === 'Success') return '<a href="' + dl.url + '" target="_blank">Download</a>';
  if (dl.status === 'Failed') return 'Failed: ' + escapeHtml(dl.error || 'unknown error');
  if (dl.status === 'RetryPrompt') return '<a href="javascript:void(0)" class="mcRetryWorldDownloadCheck">Still processing - click to check again</a>';
  return 'Preparing archive…' + (dl.attempt ? ' (' + dl.attempt + ')' : '');
}

// Pushes the current worldDownloads state into the live DOM, if that row still exists right now (it
// usually does - this just avoids waiting for the next periodic refresh to reflect a status change).
function mcRefreshWorldDownloadStatusUi(instanceId) {
  var row = document.querySelector('tr.mcServerRow[data-instance-id="' + instanceId + '"]');
  var detailRow = row && row.nextElementSibling;
  var inProgress = worldDownloads[instanceId] && worldDownloads[instanceId].status === 'InProgress';

  // The table row's quick button and the Actions tab's duplicate both need the same in-progress state.
  [row, detailRow].forEach(function (scope) {
    if (!scope) return;
    scope.querySelectorAll('.mcDownloadWorldBtn').forEach(function (btn) {
      btn.disabled = inProgress;
      btn.textContent = inProgress ? 'Downloading…' : 'Download World';
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

// mods: array of {name, version} from GameStatus - see docs/server-status.md
function renderModsList(mods) {
  if (!mods || mods.length === 0) {
    return '<p class="mcModsEmpty">No mod info available yet.</p>';
  }
  return '<ul class="mcModsList">' + mods.map(function (mod) {
    var version = mod.version ? ' <span class="mcModVersion">v' + escapeHtml(mod.version) + '</span>' : '';
    return '<li>' + escapeHtml(mod.name) + version + '</li>';
  }).join('') + '</ul>';
}

// Renders one row per instance, preserving which row (if any) is currently expanded
async function renderTable(data) {
    var instances = (data && data[1] && data[1]["Instances"]) || [];
    var tbody = document.getElementById('mcServerTableBody');
    var previouslySelectedInstanceId = tbody.querySelector('tr.mcServerRow.selected') ? tbody.querySelector('tr.mcServerRow.selected').dataset.instanceId : null;
    var previouslyActiveTab = tbody.querySelector('.mcTab.active') ? tbody.querySelector('.mcTab.active').dataset.tab : 'overview';

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
        '<td>' + mcDownloadWorldButtonHtml(instanceId, !canDownload) + '</td>';

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
            (canDownload ? mcDownloadWorldButtonHtml(instanceId) +
              '<span class="mcWorldDownloadStatus">' + mcWorldDownloadStatusHtml(instanceId) + '</span>' : '')) : '') +
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
        '<div class="mcTabPanel mcModsPanel" data-tab-panel="mods">' + renderModsList(status && status.mods) + '</div>' +
        '</td>';

      detailRow.querySelectorAll('.mcTab').forEach(function (tab) {
        tab.onclick = function (e) { e.stopPropagation(); mcSwitchTab(detailRow, tab.dataset.tab); };
      });
      mcSwitchTab(detailRow, previouslyActiveTab);

      var select = detailRow.querySelector('select');
      var resizeBtn = detailRow.querySelector('.primary');
      var backupBtn = detailRow.querySelector('.mcBackupNowBtn');
      var idleToggleBtn = detailRow.querySelector('.mcIdleShutdownToggleBtn');

      // Both the table row's quick lifecycle/download buttons and their Actions-tab duplicates get the
      // same handler - clicking either does the same thing.
      [row, detailRow].forEach(function (scope) {
        var lifecycleBtn = scope.querySelector('.mcLifecycleBtn');
        if (lifecycleBtn && !lifecycleBtn.disabled) lifecycleBtn.onclick = function (e) {
          e.stopPropagation();
          if (ec2State === 'stopped') { showAlert('Starting the Server'); startServer(instanceId); }
          else { showAlert('Stopping the Server'); stopServer(instanceId); }
        };
        scope.querySelectorAll('.mcDownloadWorldBtn').forEach(function (btn) {
          btn.onclick = function (e) { e.stopPropagation(); downloadWorld(instanceId); };
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
async function downloadWorld(instanceId) {
  worldDownloads[instanceId] = { status: 'InProgress', attempt: 0 };
  mcRefreshWorldDownloadStatusUi(instanceId);
  var jwt = await getJwt();

  var startUrl = API_URL + "downloadworldstart/" + query_string + "&instanceid=" + encodeURIComponent(instanceId);
  var startResp = await fetch(startUrl, { method: 'get', headers: new Headers({ 'Authorization': jwt }) });
  var startData = await startResp.json();

  if (startData.status === 'Failed') {
    worldDownloads[instanceId] = { status: 'Failed', error: startData.error || 'could not start' };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }

  pollWorldDownloadStatus(instanceId, startData.commandId, startData.s3Key, 0);
}

function retryWorldDownload(instanceId) {
  var dl = worldDownloads[instanceId];
  if (!dl || !dl.commandId) return;
  worldDownloads[instanceId] = { status: 'InProgress', attempt: 0 };
  mcRefreshWorldDownloadStatusUi(instanceId);
  pollWorldDownloadStatus(instanceId, dl.commandId, dl.s3Key, 0);
}

// Zipping+uploading a multi-GB world takes an unbounded amount of time, and API Gateway can't hold a
// request open past 29s anyway - so this polls a separate status endpoint rather than blocking on one call.
async function pollWorldDownloadStatus(instanceId, commandId, s3Key, attempt) {
  var maxAttempts = 60; // 60 * 5s = 5 minutes
  var jwt = await getJwt();
  var statusUrl = API_URL + "downloadworldstatus/" + query_string +
    "&instanceid=" + encodeURIComponent(instanceId) +
    "&commandid=" + encodeURIComponent(commandId) +
    "&s3key=" + encodeURIComponent(s3Key);
  var resp = await fetch(statusUrl, { method: 'get', headers: new Headers({ 'Authorization': jwt }) });
  var data = await resp.json();

  if (data.status === 'Success') {
    worldDownloads[instanceId] = { status: 'Success', url: data.url };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }
  if (data.status === 'Failed') {
    worldDownloads[instanceId] = { status: 'Failed', error: data.error };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }
  if (attempt >= maxAttempts) {
    worldDownloads[instanceId] = { status: 'RetryPrompt', commandId: commandId, s3Key: s3Key };
    mcRefreshWorldDownloadStatusUi(instanceId);
    return;
  }
  worldDownloads[instanceId] = { status: 'InProgress', attempt: attempt + 1 };
  mcRefreshWorldDownloadStatusUi(instanceId);
  await sleep(5000);
  pollWorldDownloadStatus(instanceId, commandId, s3Key, attempt + 1);
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

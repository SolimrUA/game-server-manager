// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT-0

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
function mcStateBadgeClass(state) {
  if (state === 'running') return 'running';
  if (state === 'stopped') return 'stopped';
  return 'pending';
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

// LastBackupTime comes from AWS Backup, independent of the instance's own State - the daily backup runs
// on its own schedule against the EBS data volume regardless of whether the game is running.
function mcLastBackupText(lastBackupTime) {
  if (!lastBackupTime) return '—';
  var diffHours = Math.floor((Date.now() - new Date(lastBackupTime).getTime()) / (1000 * 60 * 60));
  if (diffHours < 1) return '<1h ago';
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

    if (instances.length === 0) {
      tbody.innerHTML = '<tr class="mcLoadingRow"><td colspan="9">No gaming server instances found</td></tr>';
      return;
    }

    tbody.innerHTML = '';
    instances.forEach(function (instance) {
      var row = document.createElement('tr');
      row.className = 'mcServerRow';
      row.dataset.instanceId = instance['InstanceId'];
      row.onclick = function () { toggleServerRow(row); };

      var badgeClass = mcStateBadgeClass(instance['State']);
      var dns = instance['DomainName'] && instance['DomainName'] !== 'No domain tag found' ? instance['DomainName'] : '—';
      var status = instance['GameStatus'];
      var gameStatus = mcGameStatus(instance);
      var playersText = status && status.players && status.players.current != null
        ? status.players.current + (status.players.max != null ? ' / ' + status.players.max : '')
        : '—';
      var versionText = status && status.version ? escapeHtml(status.version) : '—';

      row.innerHTML =
        '<td><span class="mcChevron">▸</span></td>' +
        '<td><span class="mcDnsDot" data-dns-dot></span>' + dns + '</td>' +
        '<td>' + (instance['PublicIpAddress'] || '—') + '</td>' +
        '<td><span class="mcBadge ' + badgeClass + '">' + instance['State'] + '</span></td>' +
        '<td>' + (gameStatus.badgeClass ? '<span class="mcBadge ' + gameStatus.badgeClass + '">' + gameStatus.text + '</span>' : gameStatus.text) + '</td>' +
        '<td>' + instance['InstanceType'] + '</td>' +
        '<td>' + playersText + '</td>' +
        '<td>' + versionText + '</td>' +
        '<td>' + mcLastBackupText(instance['LastBackupTime']) + '</td>';

      var detailRow = document.createElement('tr');
      detailRow.className = 'mcServerDetail hidden';
      detailRow.innerHTML =
        '<td colspan="9"><div class="mcDetailInner">' +
        '<button class="btn stop">Stop</button>' +
        '<button class="btn start">Start</button>' +
        '<select class="mcResizeSelect">' +
        '<option value="micro">Micro</option>' +
        '<option value="small">Small</option>' +
        '<option value="medium">Medium</option>' +
        '<option value="large">Large</option>' +
        '</select>' +
        '<button class="btn primary">Resize</button>' +
        '<button class="btn mcBackupNowBtn">Backup Now</button>' +
        '<button class="btn mcDownloadWorldBtn">Download World</button>' +
        '<span class="mcWorldDownloadStatus">' + mcWorldDownloadStatusHtml(instance['InstanceId']) + '</span>' +
        '</div>' +
        '<div class="mcModsSection"><h4>Mods</h4>' + renderModsList(status && status.mods) + '</div>' +
        '</td>';

      var instanceId = instance['InstanceId'];
      var select = detailRow.querySelector('select');
      detailRow.querySelector('.stop').onclick = function (e) { e.stopPropagation(); showAlert('Stopping the Server'); stopServer(instanceId); };
      detailRow.querySelector('.start').onclick = function (e) { e.stopPropagation(); showAlert('Starting the Server'); startServer(instanceId); };
      detailRow.querySelector('.primary').onclick = function (e) { e.stopPropagation(); showAlert('Please wait... Resizing your server'); resizeServer(select.value, instanceId); };
      detailRow.querySelector('.mcBackupNowBtn').onclick = function (e) { e.stopPropagation(); showAlert('Starting backup'); backupNow(instanceId); };
      detailRow.querySelector('.mcDownloadWorldBtn').onclick = function (e) { e.stopPropagation(); downloadWorld(instanceId); };
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

  refreshData();
  setInterval(refreshData, 5000);

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

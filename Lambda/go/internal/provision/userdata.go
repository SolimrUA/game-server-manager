package provision

import (
	"fmt"

	"personal-game-server-manager/lambda/internal/remote"
)

// userDataScript installs the game on first boot and reports how it went.
//
// The files it writes go to the root disk rather than /tmp, which this AMI mounts as tmpfs and
// clears on every reboot - the status agent re-reads them on every restart.
const userDataScript = `#!/bin/bash
set -x
REPORT_URL=%[1]s
LOG_URL=%[2]s
report() {
  code=$?
  state=ok
  [ "$code" = "0" ] || state=failed
  printf '{"status":"%%s","exitCode":%%d,"finishedAt":"%%s"}' "$state" "$code" "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)" \
    > /tmp/install-result.json
  wget -q --method=PUT --body-file=/tmp/install-result.json \
    --header='Content-Type: application/json' -O /dev/null "$REPORT_URL" || true
  #Uploaded so a failed install can be read without reaching the instance at all. The journal is
  #included because a unit started here reports its own failure there, not on this script's output.
  tail -c 120000 /var/log/cloud-init-output.log > /tmp/install.log 2>/dev/null || true
  echo '===== journal =====' >> /tmp/install.log
  journalctl -b --no-pager 2>/dev/null | tail -c 120000 >> /tmp/install.log || true
  wget -q --method=PUT --body-file=/tmp/install.log \
    --header='Content-Type: text/plain' -O /dev/null "$LOG_URL" || true
}
trap report EXIT
set -e
export DEBIAN_FRONTEND=noninteractive
apt update || (sleep 5 && apt update) || (sleep 15 && apt update)
apt upgrade -y || echo "apt upgrade failed - continuing, not required for the game server itself"
mkdir -p /etc/game-server-manager
echo %[3]s > /etc/game-server-manager/paramName.txt
echo %[4]s > /etc/game-server-manager/statusBucket.txt
echo %[3]s > /etc/game-server-manager/statusKey.txt
echo %[5]s > /etc/game-server-manager/companionSha256.txt
echo %[6]s > /etc/game-server-manager/gameVersion.txt
mkdir -p /usr/games/install
cd /usr/games/install
wget -O gameserverinstall.sh %[7]s
echo "%[8]s  gameserverinstall.sh" | sha256sum -c -
chmod +x gameserverinstall.sh
./gameserverinstall.sh %[7]s
`

// userData fills in the install script. Every value the caller had any hand in is quoted as a
// single shell word, so it can only ever be read as an argument.
func userData(req createRequest, template Template, urls installURLs, statusBucket string) string {
	return fmt.Sprintf(userDataScript,
		remote.Quote(urls.report),
		remote.Quote(urls.log),
		remote.Quote(req.ServerID),
		remote.Quote(statusBucket),
		remote.Quote(template.CompanionSha256),
		remote.Quote(req.GameVersion),
		remote.Quote(template.InstallScriptURL),
		template.InstallSha256,
	)
}

// installURLs are the presigned URLs the installer reports its outcome and its log to.
type installURLs struct {
	report string
	log    string
}

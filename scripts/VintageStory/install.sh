#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

#Without this the outer UserData's cfn-signal reports success anyway, since it only sees this script's
#own exit code.
set -euo pipefail

#UserData passes this script's own source URL, so sibling files can be fetched from wherever this copy
#was served from.
INSTALLSCRIPTURL="$1"
BASEURL="${INSTALLSCRIPTURL%/*}"
#The scripts root holds what every game shares; this game's own files sit under it.
SCRIPTSURL="${BASEURL%/*}"

DIGESTS=/etc/game-server-manager/digests.txt

#Checked against the one digest UserData pinned: a list that doesn't match is a list this deploy
#didn't publish, so nothing it names can be trusted either.
sudo wget -O "$DIGESTS" "${BASEURL}/digests.txt"
echo "$(cat /etc/game-server-manager/digests.sha256)  $DIGESTS" | sha256sum -c -

#Duplicated in each game's install.sh rather than shared, because this is what bootstraps trust:
#a shared copy would itself need fetching and verifying, with nothing to verify it against.
#
#The list is the whole of what may reach this server, so a missing entry means the deploy and
#this script disagree about what to install.
verify() {
  local want
  want=$(awk -v name="$1" '$2 == name { print $1 }' "$DIGESTS")
  if [ -z "$want" ]; then
    echo "no digest recorded for $1" >&2
    exit 1
  fi
  echo "$want  $2" | sha256sum -c -
}

fetch_script() {
  sudo mkdir -p "$(dirname "$2")"
  sudo wget -O "$2" "${SCRIPTSURL}/$1"
  verify "$1" "$2"
}

#Without this apt opens dialogs that hang forever waiting for a terminal there isn't one of. Set
#directly rather than through sudo, which resets the environment; this already runs as root.
export DEBIAN_FRONTEND=noninteractive

#Non-fatal: a transient mirror desync (a .deb 404ing before the mirror catches up with the index)
#shouldn't abort the install under `set -e`, and OS upgrades aren't required for the game to run.
apt update || (sleep 5 && apt update) || (sleep 15 && apt update)
apt upgrade -y || echo "apt upgrade failed - continuing, not required for the game server itself"
sudo apt install -y wget curl tar unzip zip jq apt-transport-https ca-certificates gnupg lsb-release python3 screen procps \
  || (sleep 5 && sudo apt install -y wget curl tar unzip zip jq apt-transport-https ca-certificates gnupg lsb-release python3 screen procps) \
  || (sleep 15 && sudo apt install -y wget curl tar unzip zip jq apt-transport-https ca-certificates gnupg lsb-release python3 screen procps)

#Canonical's AMI ships the SSM Agent started already; this covers the rare variant where it isn't.
sudo snap start amazon-ssm-agent 2>/dev/null || true

sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip -o awscliv2.zip
sudo ./aws/install

#From Microsoft's installer rather than apt, whose dotnet-runtime packages lag behind the releases the
#game needs. The channel has to match what VSVERSION requires.
curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh
chmod +x /tmp/dotnet-install.sh
sudo /tmp/dotnet-install.sh --runtime dotnet --channel 10.0 --install-dir /usr/share/dotnet

#server.sh looks for `dotnet` on PATH, which a bare --install-dir install doesn't provide.
sudo ln -sf /usr/share/dotnet/dotnet /usr/bin/dotnet

#/etc/game-server-manager, not /tmp, which this AMI clears on every reboot - vintagestory-prepare-data.sh
#re-reads these values and can run after one.
STACKNAME=$(</etc/game-server-manager/paramName.txt)
PARAMNAME=game-password-$STACKNAME

if VSPW=$(aws ssm get-parameter --name "$PARAMNAME" --with-decryption --query Parameter.Value --output text 2>/dev/null); then
  echo "Reusing existing join password from $PARAMNAME"
else
  VSPW=$(echo $RANDOM | md5sum | head -c 20)
  aws ssm put-parameter --name "$PARAMNAME" --value "$VSPW" --type "SecureString" --overwrite
fi

#Required, with no fallback to a hardcoded default: a default would silently revert an updated server to
#an old version on the next CreateInstance cycle. Failing here surfaces as a failed stack operation via
#the caller's cfn-signal trap.
VSVERSION=$(cat /etc/game-server-manager/gameVersion.txt 2>/dev/null)
if [ -z "$VSVERSION" ]; then
  echo "GameVersion is required for Vintage Story - set it in the server stack's CloudFormation parameters (e.g. 1.22.7). See https://account.vintagestory.at/downloads for available versions." >&2
  exit 1
fi
VSPORT=42420
VSNAME=" "

#ubuntu joins the group so the install can be inspected over SSH without sudo for every command;
#server.sh special-cases a caller sharing it. The data directory is a mount point, created below.
id -u vintagestory &>/dev/null || sudo useradd vintagestory -m
sudo usermod -aG vintagestory ubuntu
sudo mkdir -p /home/vintagestory/server

#Ephemeral, unlike the data volume: redownloaded on every new instance, since it's only the game binary.
cd /tmp
sudo wget -O vs_server.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VSVERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server.tar.gz
sudo chown -R vintagestory:vintagestory /home/vintagestory
sudo chmod +x /home/vintagestory/server/VintagestoryServer /home/vintagestory/server/server.sh

#server.sh defaults to a shared /var/vintagestory/data; point it at this instance's data path.
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='/home/vintagestory/data'|" /home/vintagestory/server/server.sh

sudo bash -c 'cat > /usr/local/bin/vintagestory-prepare-data.sh' <<'PREPARE_SCRIPT'
#!/bin/bash
set -euo pipefail
VSPORT="$1"
VSNAME="$2"
MOUNT_POINT=/home/vintagestory/data
LABEL=gamedata

#Found at every boot. The one EBS device that isn't the root disk is it.
#
#Addressed by its by-id path because NVMe names like /dev/nvme1n1 aren't predictable on Nitro.
ROOT_DEVICE=$(findmnt -n -o SOURCE / | sed 's/p\?[0-9]*$//')
DEVICE_PATH=""
for _ in $(seq 1 30); do
  for link in /dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_*; do
    [ -e "$link" ] || continue
    case "$link" in *-part*) continue ;; esac
    if [ "$(readlink -f "$link")" = "$ROOT_DEVICE" ]; then continue; fi
    DEVICE_PATH="$link"
    break
  done
  [ -n "$DEVICE_PATH" ] && break
  sleep 2
done
if [ -z "$DEVICE_PATH" ]; then
  echo "No data volume found attached to this instance" >&2
  exit 1
fi

RESOLVED=$(readlink -f "$DEVICE_PATH")
if ! blkid -o value -s TYPE "$RESOLVED" >/dev/null 2>&1; then
  echo "No filesystem on $RESOLVED - first use of this volume, formatting"
  mkfs.ext4 -L "$LABEL" "$RESOLVED"
else
  echo "$RESOLVED already has data - preserving it"
fi

mkdir -p "$MOUNT_POINT"
grep -q "^LABEL=${LABEL} " /etc/fstab || echo "LABEL=${LABEL} ${MOUNT_POINT} ext4 defaults,nofail 0 2" >> /etc/fstab
systemctl daemon-reload
#On a fresh volume this is the only thing that mounts it. On a reused one, systemd's fstab-generated
#mount unit may have got there first during boot, and `mount` would fail with "already mounted".
mountpoint -q "$MOUNT_POINT" || mount "$MOUNT_POINT"
chown -R vintagestory:vintagestory /home/vintagestory

if [ ! -f "$MOUNT_POINT/serverconfig.json" ]; then
  echo "No serverconfig.json on this volume yet - bootstrapping a fresh one"
  #Running the server once and letting it time out is what writes a default serverconfig.json to patch.
  sudo -u vintagestory timeout 30 /home/vintagestory/server/VintagestoryServer --dataPath "$MOUNT_POINT" || true

  #No password here: apply_config.sh sets it before the game starts.
  sudo -u vintagestory jq \
    --argjson port "$VSPORT" \
    --arg name "$VSNAME" \
    '.Port=$port | .ServerName=$name | .Upnp=false | .WhitelistMode=1 | .VerifyPlayerAuth=false' \
    "$MOUNT_POINT/serverconfig.json" | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
  sudo -u vintagestory mv /tmp/serverconfig.json.tmp "$MOUNT_POINT/serverconfig.json"
fi

#`systemctl enable` only arranges a start on a future boot; multi-user.target was reached long before
#this device-triggered script ran, so without an explicit start a fresh install sits "enabled" but
#"inactive (dead)" until something reboots it. Start them here, where the data they need is ready.
#
#--no-block is required: vintagestory.service is ordered After=vintagestory-data.service, and this script
#is that service's own ExecStart. A blocking start waits on an ordering dependency that can't clear until
#this script returns, leaving the calling service deadlocked in "activating".
systemctl start --no-block vintagestory.service
systemctl start --no-block vintagestory-status-agent.service
PREPARE_SCRIPT
sudo chmod +x /usr/local/bin/vintagestory-prepare-data.sh

sudo bash -c "cat > /etc/systemd/system/vintagestory-data.service" <<EOF
[Unit]
Description=Prepare and mount the persistent Vintage Story data volume

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/vintagestory-prepare-data.sh '${VSPORT}' '${VSNAME}'
#Longer than the script's own wait for the disk, so systemd doesn't give up before it does.
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory-data.service

#Starts the server once its data is mounted, and on every later boot. Goes through server.sh rather than
#the raw binary because server.sh runs the game inside a detached `screen` session, which is also what
#lets status_agent.py send it console commands later. server.sh drops privileges to the vintagestory
#user itself, so this unit runs as root.
#
#RequiresMountsFor, not a hardcoded ordering on vintagestory-data.service: this is what lets systemd defer
#starting the game until the mount is genuinely live, however long that takes (the volume attaches well
#after this setup script finishes, via CloudFormation, completely decoupled from this boot).
#
#Type=oneshot + RemainAfterExit rather than Type=forking: the game lives inside a screen session, not as
#a traceable child of this unit, so forking mode concludes the service never started and stops it again.
#status_agent.py's pgrep-based check is what actually knows whether the game is up.
sudo bash -c 'cat > /etc/systemd/system/vintagestory.service' <<EOF
[Unit]
Description=Vintage Story Dedicated Server
After=network.target vintagestory-data.service vintagestory-config.service
#A failed reconcile must not stop the game starting.
Wants=vintagestory-config.service
RequiresMountsFor=/home/vintagestory/data

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/home/vintagestory/server/server.sh start
ExecStop=/home/vintagestory/server/server.sh stop
TimeoutStartSec=60
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory

fetch_script VintageStory/status_agent.py /home/vintagestory/status_agent.py
sudo chown vintagestory:vintagestory /home/vintagestory/status_agent.py
sudo chmod +x /home/vintagestory/status_agent.py

sudo bash -c 'cat > /etc/systemd/system/vintagestory-status-agent.service' <<EOF
[Unit]
Description=Vintage Story status agent (player count/mods/version to S3)
After=vintagestory.service
Requires=vintagestory.service
RequiresMountsFor=/home/vintagestory/data

[Service]
Type=simple
User=vintagestory
WorkingDirectory=/home/vintagestory
ExecStart=/usr/bin/python3 /home/vintagestory/status_agent.py
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory-status-agent

fetch_script VintageStory/apply_config.sh /home/vintagestory/apply_config.sh
sudo chmod +x /home/vintagestory/apply_config.sh

#The controller and its operations: the control panel can ask for one of these by name and
#nothing else.
fetch_script controller.sh /home/vintagestory/controller/controller.sh
fetch_script lib/archive.sh /home/vintagestory/controller/lib/archive.sh
for script in lib/game.sh lib/mods.sh lib/world.sh lib/update-game.sh \
  ops/install-mod.sh ops/delete-mod.sh ops/restart.sh ops/update.sh ops/set-player-role.sh \
  ops/archive-world.sh; do
  fetch_script "VintageStory/$script" "/home/vintagestory/controller/$script"
done
sudo chmod +x /home/vintagestory/controller/controller.sh /home/vintagestory/controller/ops/*.sh

sudo bash -c 'cat > /etc/systemd/system/vintagestory-config.service' <<EOF
[Unit]
Description=Reconcile Vintage Story config with the control panel (join password, name)
#The password is read from Parameter Store, so the network has to be up.
After=network-online.target vintagestory-data.service
Wants=network-online.target
RequiresMountsFor=/home/vintagestory/data

[Service]
Type=oneshot
ExecStart=/home/vintagestory/apply_config.sh
TimeoutStartSec=60

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory-config.service

#Started as well as enabled: enabling only arranges a start on the next boot, and the world has to be
#mounted now. Last, because mounting ends by starting the units written above. Blocking, so a failure
#to mount fails the install.
sudo systemctl start vintagestory-data.service

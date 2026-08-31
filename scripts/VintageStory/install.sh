#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

#fail loudly on any error - without this, a failing command here (e.g. an apt package that doesn't
#exist) just gets skipped over, and the outer UserData wrapper's cfn-signal reports success regardless,
#since it only sees this script's own exit code.
set -euo pipefail

#the server stack's UserData invokes this as `./install.sh "$GameServer"` so we know our own source URL and
#can fetch sibling files (status_agent.py) from the same location, whether that's raw.githubusercontent.com
#or a published S3 copy.
INSTALLSCRIPTURL="$1"
BASEURL="${INSTALLSCRIPTURL%/*}"

#stops apt from popping an interactive dialog (e.g. a pending-kernel-upgrade notice) that would otherwise
#hang forever waiting for a terminal that isn't there. Not using sudo here since sudo resets the
#environment by default and would drop this - this whole script already runs as root anyway.
export DEBIAN_FRONTEND=noninteractive

apt update && apt upgrade -y
sudo apt install -y wget curl tar unzip zip jq apt-transport-https ca-certificates gnupg lsb-release python3 screen procps

#Canonical's AMI ships the SSM Agent pre-installed and auto-started - this is just cheap insurance for the
#rare AMI variant where it's present but not running. Needed for the Control Panel's "Download Backup"
#feature (AWS Systems Manager Run Command). No-op if it's already running.
sudo snap start amazon-ssm-agent 2>/dev/null || true

#install AWS CLI
sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip -o awscliv2.zip
sudo ./aws/install

#install the .NET runtime required by the Vintage Story dedicated server, via Microsoft's official installer
#rather than the apt feed - Ubuntu's dotnet-runtime apt packages lag behind current .NET releases, so the
#exact version this needs may not be available there. Must match whatever VSVERSION below actually requires
#(check with `./VintagestoryServer` if a newer VSVERSION starts refusing to launch).
curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh
chmod +x /tmp/dotnet-install.sh
sudo /tmp/dotnet-install.sh --runtime dotnet --channel 10.0 --install-dir /usr/share/dotnet

#the game's own server.sh checks for a `dotnet` command on PATH (`dotnet --list-runtimes`) rather than
#checking the runtime files directly, which a bare --install-dir install doesn't provide on its own
sudo ln -sf /usr/share/dotnet/dotnet /usr/bin/dotnet

#get stackname created by user data script and update SSM parameter name with this to make it unique.
#Read from /etc/game-server-manager, not /tmp: /tmp is cleared on every reboot on this AMI (tmpfs), and
#this same value gets re-read later by vintagestory-prepare-data.sh, which can run after a reboot too.
STACKNAME=$(</etc/game-server-manager/paramName.txt)
PARAMNAME=game-password-$STACKNAME

#reuse the join password across reinstalls of this same stack instead of minting a new one every time;
#only generate and store one the first time this server is ever set up
if VSPW=$(aws ssm get-parameter --name "$PARAMNAME" --with-decryption --query Parameter.Value --output text 2>/dev/null); then
  echo "Reusing existing join password from $PARAMNAME"
else
  VSPW=$(echo $RANDOM | md5sum | head -c 20)
  aws ssm put-parameter --name "$PARAMNAME" --value "$VSPW" --type "SecureString" --overwrite
fi

#pinned server version - check https://account.vintagestory.at/downloads for newer stable releases
VSVERSION=1.22.6
VSPORT=42420
VSNAME=" "

#create a dedicated, unprivileged user to run the server under. Add the default ubuntu login user to its
#group so you can actually browse/inspect the install over SSH without sudo for every command - server.sh
#itself already special-cases a caller that shares this group (see GROUPNAME in as_user()). The data
#directory itself isn't created here - it's a mount point, created by the data-prepare script below once
#the persistent volume actually attaches.
id -u vintagestory &>/dev/null || sudo useradd vintagestory -m
sudo usermod -aG vintagestory ubuntu
sudo mkdir -p /home/vintagestory/server
echo $VSVERSION | sudo -u vintagestory tee /home/vintagestory/version.txt > /dev/null

#download and unpack the dedicated server - this also gives us server.sh, the game's own launcher script.
#This is ephemeral (redownloaded fresh on every new instance, unlike the persistent data volume), which is
#fine - it's the game binary, not anything a player created.
cd /tmp
sudo wget -O vs_server.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VSVERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server.tar.gz
sudo chown -R vintagestory:vintagestory /home/vintagestory
sudo chmod +x /home/vintagestory/server/VintagestoryServer /home/vintagestory/server/server.sh

#server.sh defaults to a shared /var/vintagestory/data path (meant for one server per box); point it at
#the same per-instance data path everything else here already uses
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='/home/vintagestory/data'|" /home/vintagestory/server/server.sh

#The persistent data volume (world save, mods, serverconfig.json) is a separate EC2 resource from this
#instance, attached by CloudFormation only after this whole setup script finishes and signals success -
#that's deliberate, so a broken setup script fails fast without ever touching game data, and so the
#instance never blocks its own creation waiting on storage. Once CloudFormation actually attaches the
#volume, systemd's own built-in udev integration notices the new block device and starts the service
#below automatically - no custom udev rule needed, just a unit that names the device by its stable by-id
#path (NVMe device names like /dev/nvme1n1 aren't predictable on these Nitro instances, but this path is).
DATA_VOLUME_ID=$(</etc/game-server-manager/dataVolumeId.txt)
VOLUME_ID_NO_DASH="${DATA_VOLUME_ID//-/}"
DEVICE_PATH="/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_${VOLUME_ID_NO_DASH}"
DEVICE_UNIT=$(systemd-escape --path --suffix=device "$DEVICE_PATH")

sudo bash -c 'cat > /usr/local/bin/vintagestory-prepare-data.sh' <<'PREPARE_SCRIPT'
#!/bin/bash
set -euo pipefail
DEVICE="$1"
VSPORT="$2"
VSNAME="$3"
MOUNT_POINT=/home/vintagestory/data
LABEL=gamedata

RESOLVED=$(readlink -f "$DEVICE")
if ! blkid -o value -s TYPE "$RESOLVED" >/dev/null 2>&1; then
  echo "No filesystem on $RESOLVED - first use of this volume, formatting"
  mkfs.ext4 -L "$LABEL" "$RESOLVED"
else
  echo "$RESOLVED already has data - preserving it"
fi

mkdir -p "$MOUNT_POINT"
grep -q "^LABEL=${LABEL} " /etc/fstab || echo "LABEL=${LABEL} ${MOUNT_POINT} ext4 defaults,nofail 0 2" >> /etc/fstab
systemctl daemon-reload
#on a fresh volume this is the only thing that mounts it. On a reused one, systemd's own fstab-generated
#mount unit may already have raced ahead and mounted it during normal boot (the fstab entry above was
#written by a previous run of this same script) - `mount` would then fail with "already mounted", so only
#call it if the path isn't live yet.
mountpoint -q "$MOUNT_POINT" || mount "$MOUNT_POINT"
chown -R vintagestory:vintagestory /home/vintagestory

if [ ! -f "$MOUNT_POINT/serverconfig.json" ]; then
  echo "No serverconfig.json on this volume yet - bootstrapping a fresh one"
  #running the server once (and letting it time out) makes it write the default serverconfig.json,
  #which we then patch with our own port/name/password/visibility settings before the real start.
  #Only happens for a genuinely fresh volume - a reused one keeps whatever config it already has,
  #including any manual edits, untouched.
  sudo -u vintagestory timeout 30 /home/vintagestory/server/VintagestoryServer --dataPath "$MOUNT_POINT" || true

  VSPW=$(aws ssm get-parameter --name "game-password-$(cat /etc/game-server-manager/paramName.txt)" --with-decryption --query Parameter.Value --output text)
  sudo -u vintagestory jq \
    --argjson port "$VSPORT" \
    --arg name "$VSNAME" \
    --arg pw "$VSPW" \
    '.Port=$port | .ServerName=$name | .Password=$pw | .Upnp=false | .WhitelistMode=1 | .VerifyPlayerAuth=false' \
    "$MOUNT_POINT/serverconfig.json" | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
  sudo -u vintagestory mv /tmp/serverconfig.json.tmp "$MOUNT_POINT/serverconfig.json"
fi

#`systemctl enable` alone only arranges for these to start on a FUTURE boot - it doesn't retroactively
#start them now just because multi-user.target was already reached earlier in this same boot (which it
#was, well before this device-triggered script got a chance to run and enable them). Every install used to
#get away without this because the instance auto-shut-down ~2 minutes after install and got manually
#restarted from the control panel - a genuine second boot, by which point these units already existed and
#WantedBy=multi-user.target correctly picked them up. Removing that auto-shutdown (idle-shutdown redesign)
#silently exposed this - confirmed live on Valheim's equivalent services: a fresh install left them
#"enabled" but "inactive (dead)" indefinitely. Starting them explicitly here, once the data they depend on
#is actually ready, is what makes a fresh install actually come up working without needing a reboot or
#manual nudge.
#
#--no-block is required, not optional: vintagestory.service has After=vintagestory-data.service, and this
#script IS vintagestory-data.service's own ExecStart - a plain (blocking) `systemctl start` here waits for
#vintagestory.service's ordering dependency on vintagestory-data.service to clear, which can't happen until
#THIS script returns. Confirmed live on Valheim's equivalent unit: without --no-block, the calling service
#hangs in "activating" forever, deadlocked against itself.
systemctl start --no-block vintagestory.service
systemctl start --no-block vintagestory-status-agent.service
PREPARE_SCRIPT
sudo chmod +x /usr/local/bin/vintagestory-prepare-data.sh

sudo bash -c "cat > /etc/systemd/system/vintagestory-data.service" <<EOF
[Unit]
Description=Prepare and mount the persistent Vintage Story data volume
BindsTo=${DEVICE_UNIT}
After=${DEVICE_UNIT}

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/vintagestory-prepare-data.sh '${DEVICE_PATH}' '${VSPORT}' '${VSNAME}'

[Install]
WantedBy=${DEVICE_UNIT}
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory-data.service

#systemd unit so the server starts once its data is actually mounted (and again on any future boot, or
#restart). Runs through server.sh (the game's own launcher) rather than the raw binary, since server.sh
#runs the game inside a detached `screen` session - that's also what makes it possible to send the server
#console commands later (e.g. status_agent.py querying player counts) via `screen -X stuff`, which the raw
#binary has no equivalent for. server.sh does its own privilege drop to the vintagestory user internally
#(via su), so this runs as root.
#
#RequiresMountsFor, not a hardcoded ordering on vintagestory-data.service: this is what lets systemd defer
#starting the game until the mount is genuinely live, however long that takes (the volume attaches well
#after this setup script finishes, via CloudFormation, completely decoupled from this boot).
#
#Type=oneshot + RemainAfterExit, not Type=forking: the actual game process ends up living inside a
#screen session, not as a traceable child of this unit, so systemd's forking-mode heuristics can't find
#it - it concludes the service didn't start and immediately stops what it just started. oneshot sidesteps
#that entirely: run the start command once, consider the unit "active" regardless, run the stop command
#on shutdown. status_agent.py's own pgrep-based liveness check is what actually knows if the game is up.
sudo bash -c 'cat > /etc/systemd/system/vintagestory.service' <<EOF
[Unit]
Description=Vintage Story Dedicated Server
After=network.target vintagestory-data.service
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

#status agent - see docs/server-status.md and status_agent.py for details. Fetched from alongside this
#install script so it tracks whichever copy (fork or published S3 assets) served install.sh itself.
sudo wget -O /home/vintagestory/status_agent.py "${BASEURL}/status_agent.py"
COMPANION_SHA256=$(</etc/game-server-manager/companionSha256.txt)
if [ -n "$COMPANION_SHA256" ]; then
  echo "${COMPANION_SHA256}  /home/vintagestory/status_agent.py" | sha256sum -c -
fi
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

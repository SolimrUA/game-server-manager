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
sudo apt install -y wget curl tar unzip jq apt-transport-https ca-certificates gnupg lsb-release python3 screen procps

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

#get stackname created by user data script and update SSM parameter name with this to make it unique
STACKNAME=$(</tmp/paramName.txt)
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
#itself already special-cases a caller that shares this group (see GROUPNAME in as_user()).
id -u vintagestory &>/dev/null || sudo useradd vintagestory -m
sudo usermod -aG vintagestory ubuntu
sudo mkdir -p /home/vintagestory/server /home/vintagestory/data
echo $VSVERSION | sudo -u vintagestory tee /home/vintagestory/version.txt > /dev/null

#download and unpack the dedicated server - this also gives us server.sh, the game's own launcher script
cd /tmp
sudo wget -O vs_server.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VSVERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server.tar.gz
sudo chown -R vintagestory:vintagestory /home/vintagestory
sudo chmod +x /home/vintagestory/server/VintagestoryServer /home/vintagestory/server/server.sh

#server.sh defaults to a shared /var/vintagestory/data path (meant for one server per box); point it at
#the same per-instance data path everything else here already uses
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='/home/vintagestory/data'|" /home/vintagestory/server/server.sh

#running the server once (and letting it time out) makes it write the default serverconfig.json,
#which we then patch with our own port/name/password/visibility settings before the real start
sudo -u vintagestory timeout 30 /home/vintagestory/server/VintagestoryServer --dataPath /home/vintagestory/data || true

CONFIGFILE=/home/vintagestory/data/serverconfig.json
sudo -u vintagestory jq \
  --argjson port $VSPORT \
  --arg name "$VSNAME" \
  --arg pw "$VSPW" \
  '.Port=$port | .ServerName=$name | .Password=$pw | .Upnp=false | WhitelistMode=1 | VerifyPlayerAuth=false' \
  $CONFIGFILE | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
sudo -u vintagestory mv /tmp/serverconfig.json.tmp $CONFIGFILE

#systemd unit so the server starts on boot. Runs through server.sh (the game's own launcher) rather than
#the raw binary, since server.sh runs the game inside a detached `screen` session - that's also what makes
#it possible to send the server console commands later (e.g. status_agent.py querying player counts) via
#`screen -X stuff`, which the raw binary has no equivalent for. server.sh does its own privilege drop to
#the vintagestory user internally (via su), so this runs as root.
#
#Type=oneshot + RemainAfterExit, not Type=forking: the actual game process ends up living inside a
#screen session, not as a traceable child of this unit, so systemd's forking-mode heuristics can't find
#it - it concludes the service didn't start and immediately stops what it just started. oneshot sidesteps
#that entirely: run the start command once, consider the unit "active" regardless, run the stop command
#on shutdown. status_agent.py's own pgrep-based liveness check is what actually knows if the game is up.
sudo bash -c 'cat > /etc/systemd/system/vintagestory.service' <<EOF
[Unit]
Description=Vintage Story Dedicated Server
After=network.target

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
sudo systemctl start vintagestory

#status agent - see docs/server-status.md and status_agent.py for details. Fetched from alongside this
#install script so it tracks whichever copy (fork or published S3 assets) served install.sh itself.
sudo wget -O /home/vintagestory/status_agent.py "${BASEURL}/status_agent.py"
sudo chown vintagestory:vintagestory /home/vintagestory/status_agent.py
sudo chmod +x /home/vintagestory/status_agent.py

sudo bash -c 'cat > /etc/systemd/system/vintagestory-status-agent.service' <<EOF
[Unit]
Description=Vintage Story status agent (player count/mods/version to S3)
After=vintagestory.service
Requires=vintagestory.service

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
sudo systemctl start vintagestory-status-agent

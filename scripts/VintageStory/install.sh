#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

#fail loudly on any error - the outer UserData wrapper's cfn-signal only reports install failure
#correctly if this script actually exits nonzero, which it won't do by default (no shebang meant this
#previously ran without -e at all, so e.g. a failed apt install for the wrong .NET version silently
#continued into "success").
set -euo pipefail

#the server stack's UserData invokes this as `./install.sh "$GameServer"` so we know our own source URL and
#can fetch sibling files (status_agent.py) from the same location, whether that's raw.githubusercontent.com
#or a published S3 copy.
INSTALLSCRIPTURL="$1"
BASEURL="${INSTALLSCRIPTURL%/*}"

sudo apt update && sudo apt upgrade -y
sudo apt install -y wget curl tar unzip jq apt-transport-https ca-certificates gnupg lsb-release python3

#install AWS CLI
sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip -o awscliv2.zip
sudo ./aws/install

#install the .NET runtime required by the Vintage Story dedicated server, via Microsoft's official installer
#rather than the apt feed - Ubuntu 20.04 has no dotnet-runtime-10.0 apt package (Microsoft drops apt support
#for older distros faster than .NET major versions ship), so relying on apt here silently installed nothing
#and the game server refused to start. Must match whatever VSVERSION below actually requires (check with
#`./VintagestoryServer` if a newer VSVERSION starts refusing to launch).
curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh
chmod +x /tmp/dotnet-install.sh
sudo /tmp/dotnet-install.sh --runtime dotnet --channel 10.0 --install-dir /usr/share/dotnet

#create random string for join password
VSPW=$(echo $RANDOM | md5sum | head -c 20)

#get stackname created by user data script and update SSM parameter name with this to make it unique
STACKNAME=$(</tmp/paramName.txt)
PARAMNAME=game-password-$STACKNAME

#put random string into parameter store as encrypted string value
aws ssm put-parameter --name $PARAMNAME --value $VSPW --type "SecureString" --overwrite

#pinned server version - check https://account.vintagestory.at/downloads for newer stable releases
VSVERSION=1.22.6
VSPORT=42420
VSNAME=" "

#create a dedicated, unprivileged user to run the server under
id -u vintagestory &>/dev/null || sudo useradd vintagestory -m
sudo mkdir -p /home/vintagestory/server /home/vintagestory/data
echo $VSVERSION | sudo -u vintagestory tee /home/vintagestory/version.txt > /dev/null

#download and unpack the dedicated server
cd /tmp
sudo wget -O vs_server.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VSVERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server.tar.gz
sudo chown -R vintagestory:vintagestory /home/vintagestory
sudo chmod +x /home/vintagestory/server/VintagestoryServer

#running the server once (and letting it time out) makes it write the default serverconfig.json,
#which we then patch with our own port/name/password/visibility settings before the real start
sudo -u vintagestory timeout 30 /home/vintagestory/server/VintagestoryServer --dataPath /home/vintagestory/data || true

CONFIGFILE=/home/vintagestory/data/serverconfig.json
sudo -u vintagestory jq \
  --argjson port $VSPORT \
  --arg name "$VSNAME" \
  --arg pw "$VSPW" \
  '.Port=$port | .ServerName=$name | .Password=$pw | .Upnp=false' \
  $CONFIGFILE | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
sudo -u vintagestory mv /tmp/serverconfig.json.tmp $CONFIGFILE

#systemd service so the server starts on boot and gets restarted if it crashes
sudo bash -c 'cat > /etc/systemd/system/vintagestory.service' <<EOF
[Unit]
Description=Vintage Story Dedicated Server
After=network.target

[Service]
Type=simple
User=vintagestory
WorkingDirectory=/home/vintagestory/server
ExecStart=/home/vintagestory/server/VintagestoryServer --dataPath /home/vintagestory/data
Restart=on-failure
RestartSec=10

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

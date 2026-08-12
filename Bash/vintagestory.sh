# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

sudo apt update && sudo apt upgrade -y
sudo apt install -y wget curl tar unzip jq apt-transport-https ca-certificates gnupg lsb-release

#install AWS CLI
sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip -o awscliv2.zip
sudo ./aws/install

#install the .NET 8.0 runtime required by the Vintage Story dedicated server
wget https://packages.microsoft.com/config/ubuntu/$(lsb_release -rs)/packages-microsoft-prod.deb -O /tmp/packages-microsoft-prod.deb
sudo dpkg -i /tmp/packages-microsoft-prod.deb
sudo apt update
sudo apt install -y dotnet-runtime-8.0

#create random string for join password
VSPW=$(echo $RANDOM | md5sum | head -c 20)

#get stackname created by user data script and update SSM parameter name with this to make it unique
STACKNAME=$(</tmp/mcParamName.txt)
PARAMNAME=mcValheimPW-$STACKNAME

#put random string into parameter store as encrypted string value
aws ssm put-parameter --name $PARAMNAME --value $VSPW --type "SecureString" --overwrite

#pinned server version - check https://account.vintagestory.at/downloads for newer stable releases
VSVERSION=1.22.6
VSPORT=42420
VSNAME="MyAWSGamingServer"

#create a dedicated, unprivileged user to run the server under
id -u vintagestory &>/dev/null || sudo useradd vintagestory -m
sudo mkdir -p /home/vintagestory/server /home/vintagestory/data

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
  '.Port=$port | .ServerName=$name | .Password=$pw | .Upnp=false | .AdvertiseServer=true' \
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

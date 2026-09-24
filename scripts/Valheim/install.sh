#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#UserData passes this script's own source URL, so sibling files can be fetched from wherever this copy
#was served from.
INSTALLSCRIPTURL="$1"
BASEURL="${INSTALLSCRIPTURL%/*}"

#Without this apt opens dialogs that hang forever waiting for a terminal there isn't one of. Set
#directly rather than through sudo, which resets the environment; this already runs as root.
export DEBIAN_FRONTEND=noninteractive

apt update && apt upgrade -y
sudo apt install unzip zip apt-transport-https ca-certificates curl gnupg lsb-release -y

#Canonical's AMI ships the SSM Agent started already; this covers the rare variant where it isn't.
sudo snap start amazon-ssm-agent 2>/dev/null || true

#Docker's own apt repo, so docker-ce resolves instead of Ubuntu's older bundled docker.io
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt update

sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip awscliv2.zip
sudo ./aws/install

sudo apt install docker-ce docker-ce-cli containerd.io docker-compose-plugin -y
sudo usermod -aG docker $USER

sudo mkdir -p /usr/games/serverconfig

#The data volume is attached only after this script signals success, so a broken install fails fast
#without touching game data. systemd's udev integration starts the service below when the device
#appears, named by its by-id path since NVMe names like /dev/nvme1n1 aren't predictable on Nitro.
DATA_VOLUME_ID=$(</etc/game-server-manager/dataVolumeId.txt)
VOLUME_ID_NO_DASH="${DATA_VOLUME_ID//-/}"
DEVICE_PATH="/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_${VOLUME_ID_NO_DASH}"
DEVICE_UNIT=$(systemd-escape --path --suffix=device "$DEVICE_PATH")

sudo bash -c 'cat > /usr/local/bin/valheim-prepare-data.sh' <<'PREPARE_SCRIPT'
#!/bin/bash
set -euo pipefail
DEVICE="$1"
MOUNT_POINT=/usr/games/serverconfig/valheim
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
#On a fresh volume this is the only thing that mounts it. On a reused one, systemd's fstab-generated
#mount unit may have got there first during boot, and `mount` would fail with "already mounted".
mountpoint -q "$MOUNT_POINT" || mount "$MOUNT_POINT"

if [ ! -f "$MOUNT_POINT/docker-compose.yml" ]; then
  echo "No docker-compose.yml on this volume yet - bootstrapping a fresh one"
  STACKNAME=$(</etc/game-server-manager/paramName.txt)
  PARAMNAME=game-password-$STACKNAME

  if VHPW=$(aws ssm get-parameter --name "$PARAMNAME" --with-decryption --query Parameter.Value --output text 2>/dev/null); then
    echo "Reusing existing join password from $PARAMNAME"
  else
    VHPW=$(echo $RANDOM | md5sum | head -c 20)
    aws ssm put-parameter --name "$PARAMNAME" --value "$VHPW" --type "SecureString" --overwrite
  fi

  #The one file this script creates but never overwrites: mods (TYPE=BepInEx, MODS=...) and any manual
  #tuning live here and have no other home, so a redeploy onto the same volume must not clobber it.
  #
  #Written directly rather than through a nested `bash -c`, which would put $MOUNT_POINT in a subshell
  #that doesn't inherit it and silently redirect to the wrong path.
  cat > "$MOUNT_POINT/docker-compose.yml" <<COMPOSE_EOF
version: "3"
services:
  valheim:
    image: mbround18/valheim:latest
    #The game saves the world on SIGINT, not on Docker's default SIGTERM, and 10s is too short for that
    #save plus AUTO_BACKUP_ON_SHUTDOWN's archive. Without both, `docker compose down` kills it unsaved.
    stop_signal: SIGINT
    stop_grace_period: 60s
    ports:
      - 2456:2456/udp
      - 2457:2457/udp
      - 2458:2458/udp
    environment:
      - PORT=2456
      - NAME=MyAWSGamingServer
      - WORLD=Dedicated
      - PASSWORD=$VHPW
      - TZ=Europe/London
      - PUBLIC=1
      - AUTO_UPDATE=1
      - AUTO_UPDATE_SCHEDULE=0 1 * * *
      - AUTO_BACKUP=1
      - AUTO_BACKUP_SCHEDULE=*/15 * * * *
      - AUTO_BACKUP_REMOVE_OLD=1
      - AUTO_BACKUP_DAYS_TO_LIVE=3
      - AUTO_BACKUP_ON_UPDATE=1
      - AUTO_BACKUP_ON_SHUTDOWN=1
    volumes:
      - ./saves:/home/steam/.config/unity3d/IronGate/Valheim
      - ./server:/home/steam/valheim
      - ./backups:/home/steam/backups
COMPOSE_EOF
fi

#`systemctl enable` only arranges a start on a future boot; multi-user.target was reached long before
#this device-triggered script ran, so without an explicit start a fresh install sits "enabled" but
#"inactive (dead)" until something reboots it. Start them here, where the data they need is ready.
#
#--no-block is required: docker-compose-valheim.service is ordered After=valheim-data.service, and this
#script is that service's own ExecStart. A blocking start waits on an ordering dependency that can't
#clear until this script returns, leaving the calling service deadlocked in "activating".
systemctl start --no-block docker-compose-valheim.service
systemctl start --no-block valheim-status-agent.service
PREPARE_SCRIPT
sudo chmod +x /usr/local/bin/valheim-prepare-data.sh

sudo bash -c "cat > /etc/systemd/system/valheim-data.service" <<EOF
[Unit]
Description=Prepare and mount the persistent Valheim data volume
BindsTo=${DEVICE_UNIT}
After=${DEVICE_UNIT}

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/valheim-prepare-data.sh '${DEVICE_PATH}'

[Install]
WantedBy=${DEVICE_UNIT}
EOF

sudo systemctl daemon-reload
sudo systemctl enable valheim-data.service

#RequiresMountsFor is what defers the container until the mount is live, however long the volume takes
#to attach.
sudo bash -c 'cat > /etc/systemd/system/docker-compose-valheim.service' <<EOF
[Unit]
Description=Valheim Dedicated Server (docker compose)
After=network.target docker.service valheim-data.service
Requires=docker.service
RequiresMountsFor=/usr/games/serverconfig/valheim

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/usr/games/serverconfig/valheim
ExecStart=/usr/bin/docker compose -f /usr/games/serverconfig/valheim/docker-compose.yml up -d
ExecStop=/usr/bin/docker compose -f /usr/games/serverconfig/valheim/docker-compose.yml down
TimeoutStartSec=120
#Must exceed docker-compose.yml's stop_grace_period (60s) by a real margin, or systemd kills this
#ExecStop - and the container it's waiting on - before that grace period is even up.
TimeoutStopSec=120

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable docker-compose-valheim.service

#Fetched from alongside this script, so it always matches the copy that served install.sh.
sudo wget -O /usr/games/serverconfig/status_agent.py "${BASEURL}/status_agent.py"
COMPANION_SHA256=$(</etc/game-server-manager/companionSha256.txt)
if [ -n "$COMPANION_SHA256" ]; then
  echo "${COMPANION_SHA256}  /usr/games/serverconfig/status_agent.py" | sha256sum -c -
fi
sudo chmod +x /usr/games/serverconfig/status_agent.py

#Root, because it needs `docker compose exec` into the game container.
sudo bash -c 'cat > /etc/systemd/system/valheim-status-agent.service' <<EOF
[Unit]
Description=Valheim status agent (player count/mods/version to S3)
After=docker-compose-valheim.service
Requires=docker-compose-valheim.service
RequiresMountsFor=/usr/games/serverconfig/valheim

[Service]
Type=simple
WorkingDirectory=/usr/games/serverconfig
ExecStart=/usr/bin/python3 /usr/games/serverconfig/status_agent.py
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable valheim-status-agent

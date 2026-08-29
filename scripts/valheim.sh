#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#stops apt from popping an interactive dialog (e.g. a pending-kernel-upgrade notice) that would otherwise
#hang forever waiting for a terminal that isn't there. Not using sudo here since sudo resets the
#environment by default and would drop this - this whole script already runs as root anyway.
export DEBIAN_FRONTEND=noninteractive

apt update && apt upgrade -y
sudo apt install unzip zip apt-transport-https ca-certificates curl gnupg lsb-release -y

#Canonical's AMI ships the SSM Agent pre-installed and auto-started - this is just cheap insurance for the
#rare AMI variant where it's present but not running. Needed for the Control Panel's "Download Backup"
#feature (AWS Systems Manager Run Command). No-op if it's already running.
sudo snap start amazon-ssm-agent 2>/dev/null || true

#Docker's own apt repo, so docker-ce actually resolves instead of falling back to Ubuntu's bundled docker.io
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt update

#install AWS CLI
sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip awscliv2.zip
sudo ./aws/install

#get stackname created by user data script and update SSM parameter name with this to make it unique
STACKNAME=$(</etc/game-server-manager/paramName.txt)
PARAMNAME=game-password-$STACKNAME

#reuse the join password across reinstalls of this same stack instead of minting a new one every time;
#only generate and store one the first time this server is ever set up
if VHPW=$(aws ssm get-parameter --name "$PARAMNAME" --with-decryption --query Parameter.Value --output text 2>/dev/null); then
  echo "Reusing existing join password from $PARAMNAME"
else
  VHPW=$(echo $RANDOM | md5sum | head -c 20)
  aws ssm put-parameter --name "$PARAMNAME" --value "$VHPW" --type "SecureString" --overwrite
fi

#install docker and valheim app on docker
sudo apt install docker-ce docker-ce-cli containerd.io docker-compose-plugin -y
sudo usermod -aG docker $USER

#docker-compose.yml itself lives on the ephemeral root disk (it's just config, safe to regenerate on every
#new instance) and points at ./valheim/{saves,server,backups} for its bind mounts - those subdirectories
#live on the persistent data volume mounted below, so world saves/backups survive instance replacement
#even though this file doesn't.
sudo mkdir -p /usr/games/serverconfig
cd /usr/games/serverconfig
sudo bash -c 'echo "version: \"3\"
services:
  valheim:
    image: mbround18/valheim:latest
    ports:
      - 2456:2456/udp
      - 2457:2457/udp
      - 2458:2458/udp
    environment:
      - PORT=2456
      - NAME="MyAWSGamingServer"
      - WORLD="Dedicated"
      - PASSWORD='"$VHPW"'
      - TZ=Europe/London
      - PUBLIC=1
      - AUTO_UPDATE=1
      - AUTO_UPDATE_SCHEDULE="0 1 * * *"
      - AUTO_BACKUP=1
      - AUTO_BACKUP_SCHEDULE="*/15 * * * *"
      - AUTO_BACKUP_REMOVE_OLD=1
      - AUTO_BACKUP_DAYS_TO_LIVE=3
      - AUTO_BACKUP_ON_UPDATE=1
      - AUTO_BACKUP_ON_SHUTDOWN=1
    volumes:
      - ./valheim/saves:/home/steam/.config/unity3d/IronGate/Valheim
      - ./valheim/server:/home/steam/valheim
      - ./valheim/backups:/home/steam/backups" >> docker-compose.yml'

#The persistent data volume (world saves/backups) is a separate EC2 resource from this instance, attached
#by CloudFormation only after this whole setup script finishes and signals success - that's deliberate, so
#a broken setup script fails fast without ever touching game data, and so the instance never blocks its own
#creation waiting on storage. Once CloudFormation actually attaches the volume, systemd's own built-in udev
#integration notices the new block device and starts the service below automatically - no custom udev rule
#needed, just a unit that names the device by its stable by-id path (NVMe device names like /dev/nvme1n1
#aren't predictable on these Nitro instances, but this path is).
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
#on a fresh volume this is the only thing that mounts it. On a reused one, systemd's own fstab-generated
#mount unit may already have raced ahead and mounted it during normal boot (the fstab entry above was
#written by a previous run of this same script) - `mount` would then fail with "already mounted", so only
#call it if the path isn't live yet.
mountpoint -q "$MOUNT_POINT" || mount "$MOUNT_POINT"
#docker itself creates the saves/server/backups subdirectories under this mount on first `compose up`,
#whether the volume is fresh or already has them from a previous instance - nothing else to bootstrap here.
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

#systemd unit so the server starts once its data is actually mounted (and again on any future boot, or
#restart), replacing the previous @reboot cron entry - RequiresMountsFor is what lets systemd defer
#starting the container until the mount is genuinely live, however long that takes (the volume attaches
#well after this setup script finishes, via CloudFormation, completely decoupled from this boot).
sudo bash -c 'cat > /etc/systemd/system/docker-compose-valheim.service' <<EOF
[Unit]
Description=Valheim Dedicated Server (docker compose)
After=network.target docker.service valheim-data.service
Requires=docker.service
RequiresMountsFor=/usr/games/serverconfig/valheim

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/usr/games/serverconfig
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable docker-compose-valheim.service

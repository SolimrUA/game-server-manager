#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
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

#install docker and valheim app on docker
sudo apt install docker-ce docker-ce-cli containerd.io docker-compose-plugin -y
sudo usermod -aG docker $USER

sudo mkdir -p /usr/games/serverconfig

#The persistent data volume (world saves/backups, and - see below - docker-compose.yml itself) is a
#separate EC2 resource from this instance, attached
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

if [ ! -f "$MOUNT_POINT/docker-compose.yml" ]; then
  echo "No docker-compose.yml on this volume yet - bootstrapping a fresh one"
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

  #docker-compose.yml lives ON the persistent volume (unlike everything else this install script writes,
  #which is safe to regenerate on every new instance) - this is deliberately the ONE file this script only
  #ever creates, never overwrites, on a reused volume: it's where mods (TYPE=BepInEx, MODS=...) and any
  #other manual tuning actually live, and those edits have no other home - there's no CloudFormation
  #parameter or code path meant to hold "which mods are installed" for a given server. A future redeploy
  #(new EC2 instance, same data volume) must never clobber this.
  #
  #Written directly (no nested `bash -c`/sudo, unlike other blocks in this script) since
  #valheim-prepare-data.sh already runs entirely as root - an earlier version wrapped this in `bash -c
  #'... > "$MOUNT_POINT/..."'`, which put the `$MOUNT_POINT` reference inside a nested subshell that
  #doesn't inherit un-exported variables from this script, so it silently wrote to the wrong path (`>`'s
  #own working directory, not $MOUNT_POINT) instead of erroring - confirmed live.
  cat > "$MOUNT_POINT/docker-compose.yml" <<COMPOSE_EOF
version: "3"
services:
  valheim:
    image: mbround18/valheim:latest
    #Docker's default stop signal (SIGTERM) isn't what the game process listens for - it only performs a
    #graceful world save on SIGINT - and even with the right signal, the default 10s grace period isn't
    #enough time for a save plus AUTO_BACKUP_ON_SHUTDOWN's own backup archive to complete. Without both of
    #these, `docker compose down` (what the control panel's Stop button triggers, via
    #docker-compose-valheim.service's ExecStop) kills the world without saving it - confirmed live.
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

#`systemctl enable` alone only arranges for these to start on a FUTURE boot - it doesn't retroactively
#start them now just because multi-user.target was already reached earlier in this same boot (which it
#was, well before this device-triggered script got a chance to run and enable them). Every install used to
#get away without this because the instance auto-shut-down ~2 minutes after install and got manually
#restarted from the control panel - a genuine second boot, by which point these units already existed and
#WantedBy=multi-user.target correctly picked them up. Removing that auto-shutdown (idle-shutdown redesign)
#silently exposed this - confirmed live: a fresh install left both services "enabled" but "inactive (dead)"
#indefinitely. Starting them explicitly here, once the data they depend on is actually ready, is what makes
#a fresh install actually come up working without needing a reboot or manual nudge.
#
#--no-block is required, not optional: docker-compose-valheim.service has After=valheim-data.service, and
#this script IS valheim-data.service's own ExecStart - a plain (blocking) `systemctl start` here waits for
#docker-compose-valheim.service's ordering dependency on valheim-data.service to clear, which can't happen
#until THIS script returns. Confirmed live: without --no-block, valheim-data.service hangs in "activating"
#forever, deadlocked against itself.
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
WorkingDirectory=/usr/games/serverconfig/valheim
ExecStart=/usr/bin/docker compose -f /usr/games/serverconfig/valheim/docker-compose.yml up -d
ExecStop=/usr/bin/docker compose -f /usr/games/serverconfig/valheim/docker-compose.yml down
TimeoutStartSec=120
#must exceed docker-compose.yml's own stop_grace_period (60s) by a real margin, or systemd kills this
#ExecStop command (and, since docker compose down is what's doing the waiting, the container underneath
#it) before that grace period is even up - defeating the whole point of setting it.
TimeoutStopSec=120

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable docker-compose-valheim.service

#status agent - see docs/server-status.md and status_agent.py for details. Fetched from alongside this
#install script so it tracks whichever copy (fork or published S3 assets) served install.sh itself.
sudo wget -O /usr/games/serverconfig/status_agent.py "${BASEURL}/status_agent.py"
COMPANION_SHA256=$(</etc/game-server-manager/companionSha256.txt)
if [ -n "$COMPANION_SHA256" ]; then
  echo "${COMPANION_SHA256}  /usr/games/serverconfig/status_agent.py" | sha256sum -c -
fi
sudo chmod +x /usr/games/serverconfig/status_agent.py

#runs as root (not a dedicated low-priv user, unlike Vintage Story's status agent) since it needs
#`docker compose exec` access into the game container - same trust level as everything else in this
#script, which already runs as root throughout.
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

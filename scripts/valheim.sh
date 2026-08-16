#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#stops apt from popping an interactive dialog (e.g. a pending-kernel-upgrade notice) that would otherwise
#hang forever waiting for a terminal that isn't there. Not using sudo here since sudo resets the
#environment by default and would drop this - this whole script already runs as root anyway.
export DEBIAN_FRONTEND=noninteractive

apt update && apt upgrade -y
sudo apt install unzip apt-transport-https ca-certificates curl gnupg lsb-release -y

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
STACKNAME=$(</tmp/paramName.txt)
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
sudo mkdir /usr/games/serverconfig
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
echo "@reboot root (cd /usr/games/serverconfig/ && docker compose up -d)" > /etc/cron.d/awsgameserver
sudo docker compose up -d

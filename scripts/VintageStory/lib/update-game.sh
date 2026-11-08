#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -e
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/game.sh"

#Reimplements the update sequence, because server.sh's own `update` subcommand is a disabled stub
#that prints manual instructions and exits 0.

#The API lists pre-release tags like "1.4.4-dev.2" alongside stable ones.
VERSION=$(curl -fsS https://mods.vintagestory.at/api/gameversions | jq -r '[.gameversions[].name | select(contains("-") | not)] | last')
if [ -z "$VERSION" ] || [ "$VERSION" = "null" ]; then
  echo "Couldn't determine the latest available version" >&2
  exit 1
fi
echo "Updating Vintage Story server to $VERSION"

sudo -u "$GAME_USER" "$SERVER_SCRIPT" stop

#Extracting over an existing install mixes old and new asset files, and the game then refuses to start
#("Your Server installation still contains old files from a previous game version"). Safe to wipe:
#this directory only ever holds the game binary, and the world sits under DATA_PATH instead.
sudo rm -rf "$SERVER_PATH"/*

cd /tmp
sudo wget -O vs_server_update.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VERSION}.tar.gz"
sudo tar -C "$SERVER_PATH" -xzf vs_server_update.tar.gz
#The tarball is root-owned (sudo wget), so a plain rm -f leaves it behind with "Operation not permitted".
sudo rm -f vs_server_update.tar.gz

#The game binary only, never all of /home/vintagestory: an update must not touch the world save.
sudo chown -R "$GAME_USER:$GAME_USER" "$SERVER_PATH"
sudo chmod +x "$SERVER_PATH/VintagestoryServer" "$SERVER_SCRIPT"

#The extracted server.sh ships the stock DATAPATH - repatch it, or the server silently comes back up
#pointed at the wrong data directory.
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='$DATA_PATH'|" "$SERVER_SCRIPT"

sudo -u "$GAME_USER" "$SERVER_SCRIPT" start
echo "Update complete: $VERSION"

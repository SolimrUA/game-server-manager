#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#Sets the join password and name the control panel recorded into serverconfig.json. The game reads
#its config only as it starts, so this runs ahead of the game: at boot, and again on every restart.

CONFIG=/home/vintagestory/data/serverconfig.json
SERVER_ID=$(</etc/game-server-manager/paramName.txt)
PARAM="game-password-$SERVER_ID"
NAME_PARAM="game-servername-$SERVER_ID"
WORLD_PARAM="game-world-$SERVER_ID"

if [ ! -f "$CONFIG" ]; then
  echo "no config at $CONFIG - nothing to reconcile"
  exit 0
fi

if ! parameter=$(aws ssm get-parameter --name "$PARAM" --with-decryption --output json 2>/dev/null); then
  echo "could not read $PARAM - leaving the password as it is"
  exit 0
fi
#JSON, so a password containing a tab survives.
password=$(printf '%s' "$parameter" | jq -r '.Parameter.Value')
if [ -z "$password" ]; then
  echo "$PARAM holds nothing - leaving the password as it is"
  exit 0
fi

#Written as the game's own user: it cannot read a config it doesn't own.
sudo -u vintagestory jq --arg pw "$password" '.Password=$pw' "$CONFIG" \
  | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
sudo -u vintagestory mv /tmp/serverconfig.json.tmp "$CONFIG"

echo "join password reconciled from $PARAM"

#No world parameter means nobody has asked for a new world, which leaves the one the server is on.
#Re-applying the same world names the same save file, so an ordinary restart generates nothing.
if world=$(aws ssm get-parameter --name "$WORLD_PARAM" --output json 2>/dev/null \
  | jq -r '.Parameter.Value') && [ -n "$world" ] && [ "$world" != "null" ]; then
  #The save file keeps company with whichever one the game is using, so this follows the layout the
  #game chose rather than assuming one.
  current=$(jq -r '.WorldConfig.SaveFileLocation // ""' "$CONFIG")
  saves_dir=$(dirname -- "$current")
  if [ "$saves_dir" = "." ]; then
    saves_dir="Saves"
  fi
  save="$saves_dir/$(printf '%s' "$world" | jq -r '.slug').vcdbs"

  sudo -u vintagestory jq --argjson world "$world" --arg save "$save" \
    '. * $world.config | .WorldConfig.SaveFileLocation = $save' "$CONFIG" \
    | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
  sudo -u vintagestory mv /tmp/serverconfig.json.tmp "$CONFIG"
  echo "world reconciled from $WORLD_PARAM into $save"
fi

#No name parameter means nobody has named this server, which leaves the name the game has.
if name=$(aws ssm get-parameter --name "$NAME_PARAM" --output json 2>/dev/null \
  | jq -r '.Parameter.Value') && [ -n "$name" ]; then
  sudo -u vintagestory jq --arg name "$name" '.ServerName=$name' "$CONFIG" \
    | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
  sudo -u vintagestory mv /tmp/serverconfig.json.tmp "$CONFIG"
  echo "name reconciled from $NAME_PARAM"
fi

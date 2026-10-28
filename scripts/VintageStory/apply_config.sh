#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#Sets the join password the control panel recorded into serverconfig.json. The game reads its config
#only as it starts, so this runs ahead of the game: at boot, and again on every restart.

CONFIG=/home/vintagestory/data/serverconfig.json
PARAM="game-password-$(</etc/game-server-manager/paramName.txt)"

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

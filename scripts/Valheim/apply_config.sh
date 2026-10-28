#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#Sets the join password the control panel recorded into the compose file. The container reads
#PASSWORD only as it starts, so this runs ahead of the game: at boot, and again on every restart.

COMPOSE=/usr/games/serverconfig/valheim/docker-compose.yml
PARAM="game-password-$(</etc/game-server-manager/paramName.txt)"

if [ ! -f "$COMPOSE" ]; then
  echo "no compose file at $COMPOSE - nothing to reconcile"
  exit 0
fi

if ! parameter=$(aws ssm get-parameter --name "$PARAM" --with-decryption --output json 2>/dev/null); then
  echo "could not read $PARAM - leaving the password as it is"
  exit 0
fi
#JSON, so a password containing a tab survives.
password=$(printf '%s' "$parameter" | yq -p json -oy -r '.Parameter.Value' -)
if [ -z "$password" ]; then
  echo "$PARAM holds nothing - leaving the password as it is"
  exit 0
fi

PASSWORD="$password" yq -i \
  '(.services.valheim.environment[] | select(. == "PASSWORD=*")) = "PASSWORD=" + strenv(PASSWORD)' \
  "$COMPOSE"

#yq exits 0 when nothing matched, so the result is read back rather than assumed.
applied=$(yq '.services.valheim.environment[] | select(. == "PASSWORD=*")' "$COMPOSE" | sed 's/^PASSWORD=//')
if [ "$applied" != "$password" ]; then
  echo "no PASSWORD entry in $COMPOSE to set" >&2
  exit 1
fi

echo "join password reconciled from $PARAM"

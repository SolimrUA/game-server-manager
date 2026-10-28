#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#Sets the join password and name the control panel recorded into the compose file. The container
#reads them only as it starts, so this runs ahead of the game: at boot, and again on every restart.

COMPOSE=/usr/games/serverconfig/valheim/docker-compose.yml
SERVER_ID=$(</etc/game-server-manager/paramName.txt)
PARAM="game-password-$SERVER_ID"
NAME_PARAM="game-servername-$SERVER_ID"

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

#No name parameter means nobody has named this server, which leaves the image's own default.
if name=$(aws ssm get-parameter --name "$NAME_PARAM" --output json 2>/dev/null \
  | yq -p json -oy -r '.Parameter.Value' -) && [ -n "$name" ]; then
  #A compose file without a NAME entry gets one.
  NAME="$name" yq -i \
    '(.services.valheim.environment[] | select(. == "NAME=*")) = "NAME=" + strenv(NAME)' \
    "$COMPOSE"
  yq '.services.valheim.environment[] | select(. == "NAME=*")' "$COMPOSE" | grep -q . \
    || NAME="$name" yq -i '.services.valheim.environment += ["NAME=" + strenv(NAME)]' "$COMPOSE"
  echo "name reconciled from $NAME_PARAM"
fi

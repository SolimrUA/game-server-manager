#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"

if [ $# -ne 1 ]; then
  echo "usage: delete-mod <mod display name>" >&2
  exit 64
fi

rm -f "$REMAINING_MODS"
#Checked before the edit, so a server that can't reconcile isn't left edited.
require_container

python3 "$COMPOSE_MODS" delete "$COMPOSE_FILE" "$1" "$REMAINING_MODS"

#The image reconciles MODS only at startup, and only when it is non-empty, so a removal is
#reconciled here instead - with the new list passed in, because a running container keeps the MODS
#it started with.
docker compose -f "$COMPOSE_FILE" exec -T -e MODS="$(cat "$REMAINING_MODS")" \
  valheim odin mod:install --from-var

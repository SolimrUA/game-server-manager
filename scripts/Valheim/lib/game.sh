# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

DATA_PATH=/usr/games/serverconfig/valheim
APPLY_CONFIG=/usr/games/serverconfig/apply_config.sh
#Also holds the mod list, as the container's MODS variable.
COMPOSE_FILE="$DATA_PATH/docker-compose.yml"
COMPOSE_MODS=/usr/games/serverconfig/controller/lib/compose_mods.py
#Where an edit leaves the mods that remain, for the reconcile to read.
REMAINING_MODS=/tmp/valheim-mods-remaining.txt

require_container() {
  if ! docker compose -f "$COMPOSE_FILE" ps -q valheim | grep -q .; then
    #What the control panel looks for to say the server has to be started first.
    echo NOT_RUNNING
    return 1
  fi
}

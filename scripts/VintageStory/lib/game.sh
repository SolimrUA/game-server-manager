# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

GAME_USER=vintagestory
DATA_PATH=/home/vintagestory/data
SERVER_PATH=/home/vintagestory/server
SERVER_SCRIPT="$SERVER_PATH/server.sh"
APPLY_CONFIG=/home/vintagestory/apply_config.sh
MODS_PATH="$DATA_PATH/Mods"

#console injects a line into the running server's console. It exits 0 whether or not the game acted
#on the line, so its output is the only account of what happened.
console() {
  sudo -u "$GAME_USER" "$SERVER_SCRIPT" command "$1"
}

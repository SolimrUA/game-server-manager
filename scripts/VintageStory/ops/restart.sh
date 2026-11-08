#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"

#Backgrounded because a restart outlasts what the control panel waits for, and logged, so a restart
#that stops the server and fails to start it again leaves a reason behind.

if [ $# -ne 0 ]; then
  echo "usage: restart" >&2
  exit 64
fi

#A failed reconcile must not stop the restart.
setsid bash -c "$APPLY_CONFIG || true; exec sudo -u $GAME_USER $SERVER_SCRIPT restart" \
  </dev/null >/tmp/game-restart.log 2>&1 &

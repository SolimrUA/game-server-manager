#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"

#The mod itself is downloaded by the image the next time the container starts.

if [ $# -ne 1 ]; then
  echo "usage: install-mod <mod reference>" >&2
  exit 64
fi

python3 "$COMPOSE_MODS" insert "$COMPOSE_FILE" "$1"

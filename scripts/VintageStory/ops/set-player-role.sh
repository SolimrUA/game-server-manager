#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"

if [ $# -ne 2 ]; then
  echo "usage: set-player-role <player name> <role code>" >&2
  exit 64
fi

console "/player $1 role $2"

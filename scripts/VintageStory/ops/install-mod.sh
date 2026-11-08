#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"

#The version has to be explicit: without it the client looks "current" up itself, through a request
#that fails.

if [ $# -ne 2 ]; then
  echo "usage: install-mod <mod reference> <game version>" >&2
  exit 64
fi

console "moddb install $1 $2"

#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"
source "$HERE/lib/mods.sh"

if [ $# -ne 1 ]; then
  echo "usage: delete-mod <mod display name>" >&2
  exit 64
fi

require_mod_tools

#Not finding one is an answer, not a failure, so set -e must not take the exit here.
modid=$(find_modid "$1" || true)
if [ -z "$modid" ]; then
  #What the control panel looks for to say which mod it couldn't find.
  echo NO_MATCH
  exit 1
fi

console "moddb remove $modid"

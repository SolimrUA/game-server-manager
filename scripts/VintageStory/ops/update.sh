#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)

#Backgrounded because downloading and extracting a release outlasts what the control panel waits
#for, and logged so that a failure partway through is diagnosable.

if [ $# -ne 0 ]; then
  echo "usage: update" >&2
  exit 64
fi

setsid bash "$HERE/lib/update-game.sh" </dev/null >/tmp/vs-update.log 2>&1 &

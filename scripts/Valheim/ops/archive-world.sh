#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"
source "$HERE/lib/archive.sh"

#Valheim archives the same way whatever the scope: the control panel has already turned it into
#this folder list.

if [ $# -ne 3 ]; then
  echo "usage: archive-world <include paths> <bucket> <key>" >&2
  exit 64
fi

rm -f "$ARCHIVE"
zip_folders "$1"
upload_and_clean "$2" "$3"

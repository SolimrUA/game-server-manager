#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail
HERE=$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd)
source "$HERE/lib/game.sh"
source "$HERE/lib/world.sh"
source "$HERE/lib/archive.sh"

if [ $# -ne 4 ]; then
  echo "usage: archive-world <scope> <include paths> <bucket> <key>" >&2
  exit 64
fi

scope=$1
paths=$2
bucket=$3
key=$4

rm -f "$ARCHIVE"

if [ "$scope" = core ]; then
  #The game's own genbackup, not the live save: it writes one file, without the Saves folder's
  #other worlds and screenshots, and without missing writes still sitting in the save's -wal file.
  backup=$(genbackup_world)
  cp "$backup" /tmp/world.vcdbs
  rm -f "$backup"
  zip -j "$ARCHIVE" /tmp/world.vcdbs
  rm -f /tmp/world.vcdbs
else
  zip_folders "$paths"
  #serverconfig.json holds the join password - zip a redacted copy, never the real file.
  jq "del(.Password)" "$DATA_PATH/serverconfig.json" > /tmp/serverconfig-redacted.json
  zip -j "$ARCHIVE" /tmp/serverconfig-redacted.json
  rm -f /tmp/serverconfig-redacted.json
fi

upload_and_clean "$bucket" "$key"

# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

BACKUPS_PATH="$DATA_PATH/Backups"

#The console command returns before the game has written the file, so this waits for it to appear
#and for its size to stop changing.
genbackup_world() {
  local name file size resize
  name="manualbackup-$(date -u +%Y-%m-%d-%H%M)"
  file="$BACKUPS_PATH/$name"

  #Out of the way of this function's stdout, which is the filename.
  console "genbackup $name" >&2

  for _ in $(seq 1 100); do
    if [ -f "$file" ]; then
      size=$(stat -c%s "$file")
      sleep 3
      resize=$(stat -c%s "$file")
      if [ "$size" = "$resize" ]; then
        break
      fi
    fi
    sleep 3
  done

  if [ ! -f "$file" ]; then
    echo "genbackup never produced a file" >&2
    return 1
  fi
  printf '%s' "$file"
}

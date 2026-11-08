# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

ARCHIVE=/tmp/worldsave.zip

zip_folders() {
  local path
  #Split on commas alone, and don't let a name be read as a glob.
  local IFS=,
  set -f
  cd "$DATA_PATH"
  for path in $1; do
    path=${path#"${path%%[![:space:]]*}"}
    path=${path%"${path##*[![:space:]]}"}
    if [ -n "$path" ] && [ -e "$path" ]; then
      zip -r "$ARCHIVE" "$path"
    fi
  done
  set +f
}

upload_and_clean() {
  aws s3 cp "$ARCHIVE" "s3://$1/$2"
  rm -f "$ARCHIVE"
}

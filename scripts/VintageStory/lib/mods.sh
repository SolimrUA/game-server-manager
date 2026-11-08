# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

#Reading the installed mods, which the game stores as either a zip or a folder holding modinfo.json.

#Without jq and unzip no mod can be read at all, which would look like none being installed.
require_mod_tools() {
  local tool
  for tool in jq unzip; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "$tool is not installed, so the installed mods cannot be read" >&2
      return 1
    fi
  done
}

read_modinfo() {
  local entry=$1
  case ${entry,,} in
    *.zip) unzip -p "$entry" modinfo.json 2>/dev/null ;;
    *) [ -f "$entry/modinfo.json" ] && cat "$entry/modinfo.json" ;;
  esac
}

#find_modid resolves the display name the control panel shows to the modid the game uses, trying
#every casing modinfo.json is written with.
find_modid() {
  local target=$1 entry info name
  [ -d "$MODS_PATH" ] || return 0
  for entry in "$MODS_PATH"/*; do
    [ -e "$entry" ] || continue
    info=$(read_modinfo "$entry") || continue
    [ -n "$info" ] || continue
    name=$(printf '%s' "$info" | jq -r '.name // .Name // empty' 2>/dev/null) || continue
    if [ "$name" = "$target" ]; then
      printf '%s' "$info" | jq -r '.modid // .ModID // .Modid // empty'
      return 0
    fi
  done
}

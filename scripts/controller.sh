#!/bin/bash
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0
set -euo pipefail

#The single entry point the control panel invokes over SSM. It sends an operation name and
#arguments, never shell of its own, so nothing without a file in ops/ can be asked for.

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

operations() {
  (cd "$HERE/ops" 2>/dev/null && ls -1 ./*.sh 2>/dev/null | sed 's|^\./||; s|\.sh$||' | tr '\n' ' ')
}

usage() {
  echo "usage: controller.sh <operation> [arguments]" >&2
  echo "operations: $(operations)" >&2
}

if [ $# -lt 1 ]; then
  usage
  exit 64
fi

operation=$1
shift

#Held to a bare name so that it can only ever select a file in ops/ - never a path out of it.
case $operation in
  '' | *[!a-z-]*)
    echo "not an operation name: $operation" >&2
    usage
    exit 64
    ;;
esac

script="$HERE/ops/$operation.sh"
if [ ! -f "$script" ]; then
  echo "no such operation: $operation" >&2
  usage
  exit 64
fi

exec bash "$script" "$@"

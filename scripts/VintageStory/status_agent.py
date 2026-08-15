#!/usr/bin/env python3
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

# Reports player count/max, version and mods to S3 every 5s, only pushing when something changed.
# See docs/server-status.md for the schema and why this exists. Log line patterns below are Vintage Story's
# best-known join/leave format - if player counts don't track correctly against your server-main.log, this is
# the regex to adjust.
import hashlib
import json
import os
import re
import subprocess
import time
import zipfile

DATA_PATH = "/home/vintagestory/data"
MODS_PATH = os.path.join(DATA_PATH, "Mods")
CONFIG_PATH = os.path.join(DATA_PATH, "serverconfig.json")
LOG_PATH = os.path.join(DATA_PATH, "Logs", "server-main.log")
VERSION_FILE = "/home/vintagestory/version.txt"
STATE_FILE = "/home/vintagestory/.status_last_hash"
STATUS_TMP_FILE = "/tmp/status_push.json"
with open("/tmp/statusBucket.txt") as f:
    STATUS_BUCKET = f.read().strip()
with open("/tmp/statusKey.txt") as f:
    STATUS_KEY = f.read().strip()

JOIN_RE = re.compile(r"Player (.+?) joined", re.IGNORECASE)
LEAVE_RE = re.compile(r"Player (.+?) left", re.IGNORECASE)


def read_max_players():
    try:
        with open(CONFIG_PATH) as f:
            config = json.load(f)
        return config.get("MaxClients", config.get("MaxClientsInSingleplayer"))
    except Exception:
        return None


def read_version():
    try:
        with open(VERSION_FILE) as f:
            return f.read().strip()
    except Exception:
        return None


def read_mod_info(read_fn):
    try:
        info = json.loads(read_fn())
        return info.get("name") or info.get("Name"), info.get("version") or info.get("Version")
    except Exception:
        return None, None


def read_mods():
    mods = []
    if not os.path.isdir(MODS_PATH):
        return mods
    for entry in sorted(os.listdir(MODS_PATH)):
        path = os.path.join(MODS_PATH, entry)
        name, version = None, None
        if entry.lower().endswith(".zip"):
            try:
                with zipfile.ZipFile(path) as zf:
                    name, version = read_mod_info(lambda: zf.read("modinfo.json").decode("utf-8"))
            except Exception:
                pass
        elif os.path.isdir(path):
            modinfo_path = os.path.join(path, "modinfo.json")
            if os.path.exists(modinfo_path):
                name, version = read_mod_info(lambda: open(modinfo_path).read())
        else:
            continue
        mods.append({"name": name or os.path.splitext(entry)[0], "version": version})
    return mods


def update_online_players(online, offset):
    try:
        with open(LOG_PATH) as f:
            f.seek(offset)
            for line in f:
                joined = JOIN_RE.search(line)
                if joined:
                    online.add(joined.group(1).strip())
                    continue
                left = LEAVE_RE.search(line)
                if left:
                    online.discard(left.group(1).strip())
            return f.tell()
    except FileNotFoundError:
        return offset


def push_status(status):
    with open(STATUS_TMP_FILE, "w") as f:
        json.dump(status, f)
    destination = "s3://{}/status/{}.json".format(STATUS_BUCKET, STATUS_KEY)
    subprocess.run(["/usr/local/bin/aws", "s3", "cp", STATUS_TMP_FILE, destination], check=True)


def main():
    log_offset = os.path.getsize(LOG_PATH) if os.path.exists(LOG_PATH) else 0
    online_players = set()
    version = read_version()
    last_hash = None
    if os.path.exists(STATE_FILE):
        with open(STATE_FILE) as f:
            last_hash = f.read().strip()

    while True:
        log_offset = update_online_players(online_players, log_offset)
        status = {
            "gameName": "vintagestory",
            "version": version,
            "players": {"current": len(online_players), "max": read_max_players()},
            "mods": read_mods(),
            "updatedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        comparable = {k: v for k, v in status.items() if k != "updatedAt"}
        current_hash = hashlib.sha256(json.dumps(comparable, sort_keys=True).encode()).hexdigest()
        if current_hash != last_hash:
            try:
                push_status(status)
                last_hash = current_hash
                with open(STATE_FILE, "w") as f:
                    f.write(current_hash)
            except Exception as e:
                print("Failed to push status: {}".format(e))
        time.sleep(5)


if __name__ == "__main__":
    main()

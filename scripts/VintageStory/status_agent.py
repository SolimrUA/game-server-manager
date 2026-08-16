#!/usr/bin/env python3
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

# Reports player count/max, version and mods to S3 every 5s, only pushing when something changed.
# See docs/server-status.md for the schema and why this exists.
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
VERSION_FILE = "/home/vintagestory/version.txt"
STATE_FILE = "/home/vintagestory/.status_last_hash"
STATUS_TMP_FILE = "/tmp/status_push.json"
SERVER_SH = "/home/vintagestory/server/server.sh"
#server.sh identifies its own process this way (see PGREPTEST in server.sh) - matching it here means our
#liveness check agrees with the game's own tooling rather than guessing independently
PGREP_PATTERN = "dotnet VintagestoryServer.dll --dataPath {}".format(DATA_PATH)
with open("/tmp/statusBucket.txt") as f:
    STATUS_BUCKET = f.read().strip()
with open("/tmp/statusKey.txt") as f:
    STATUS_KEY = f.read().strip()

#matches /stats output, e.g. "Players online: 1 / 16 (Solimr [69ms])" - verified against a live server
PLAYERS_ONLINE_RE = re.compile(r"Players online:\s*(\d+)\s*/\s*(\d+)", re.IGNORECASE)


def read_service_status():
    #systemd only sees server.sh's own process, not the game process it detaches into a screen session -
    #so `systemctl is-active` would just reflect whether server.sh's brief startup run succeeded, not
    #whether the actual game is still up. Checking for the game process directly is what server.sh itself
    #does before accepting any command, so it's the accurate signal.
    try:
        result = subprocess.run(["pgrep", "-f", PGREP_PATTERN], capture_output=True)
        return "active" if result.returncode == 0 else "inactive"
    except Exception:
        return "unknown"


def send_console_command(command):
    #the game has no network API - server.sh's `command` action is the supported way in, injecting text
    #into the detached screen session's stdin and reading back what the server printed in response
    try:
        result = subprocess.run([SERVER_SH, "command", command], capture_output=True, text=True, timeout=15)
        return result.stdout
    except Exception:
        return ""


def read_current_players():
    match = PLAYERS_ONLINE_RE.search(send_console_command("stats"))
    return int(match.group(1)) if match else None


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


def push_status(status):
    with open(STATUS_TMP_FILE, "w") as f:
        json.dump(status, f)
    destination = "s3://{}/status/{}.json".format(STATUS_BUCKET, STATUS_KEY)
    subprocess.run(["/usr/local/bin/aws", "s3", "cp", STATUS_TMP_FILE, destination], check=True)


def main():
    version = read_version()
    last_hash = None
    if os.path.exists(STATE_FILE):
        with open(STATE_FILE) as f:
            last_hash = f.read().strip()

    while True:
        service_status = read_service_status()
        status = {
            "gameName": "vintagestory",
            "version": version,
            "serviceStatus": service_status,
            "players": {
                "current": read_current_players() if service_status == "active" else None,
                "max": read_max_players(),
            },
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

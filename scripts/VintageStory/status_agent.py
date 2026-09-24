#!/usr/bin/env python3
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

# Reports player count/max, version and mods to S3 every 5s, pushing only when something changed.
# docs/server-status.md documents the JSON schema.
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
STATE_FILE = "/home/vintagestory/.status_last_hash"
STATUS_TMP_FILE = "/tmp/status_push.json"
SERVER_SH = "/home/vintagestory/server/server.sh"
#Matches server.sh's own PGREPTEST, so this liveness check agrees with the game's tooling.
PGREP_PATTERN = "dotnet VintagestoryServer.dll --dataPath {}".format(DATA_PATH)
#/etc/game-server-manager, not /tmp, which this AMI clears on every reboot - this code re-runs on every
#restart of this long-lived service, including after one.
with open("/etc/game-server-manager/statusBucket.txt") as f:
    STATUS_BUCKET = f.read().strip()
with open("/etc/game-server-manager/statusKey.txt") as f:
    STATUS_KEY = f.read().strip()

#Matches /stats output, e.g. "Players online: 1 / 16 (Solimr [69ms])"
PLAYERS_ONLINE_RE = re.compile(r"Players online:\s*(\d+)\s*/\s*(\d+)", re.IGNORECASE)
#Matches /stats' own "24.9.2026 14:44:41 [Notification] Version: 1.22.7" line. Read live rather than
#cached: an update restarts the game process, not this service, so a cached value would go stale.
VERSION_RE = re.compile(r"Version:\s*(\S+)", re.IGNORECASE)


def read_service_status():
    #systemd only sees server.sh, not the game process it detaches into a screen session, so
    #`systemctl is-active` reports whether server.sh's startup run succeeded, not whether the game is up.
    #Answers only "is the OS process there"; read_stats() below answers "is it responding yet".
    try:
        result = subprocess.run(["pgrep", "-f", PGREP_PATTERN], capture_output=True)
        return "active" if result.returncode == 0 else "inactive"
    except Exception:
        return "unknown"


def send_console_command(command):
    #The game has no network API - this injects text into the detached screen session's stdin and reads
    #back what the server printed.
    try:
        result = subprocess.run([SERVER_SH, "command", command], capture_output=True, text=True, timeout=15)
        return result.stdout
    except Exception:
        return ""


def read_stats():
    #While the OS process is up but the game is still loading, /stats prints only "process found ...
    #executing command" with no stats block, so version_match stays None - main() reads that as
    #"activating" rather than "active".
    output = send_console_command("stats")
    players_match = PLAYERS_ONLINE_RE.search(output)
    version_match = VERSION_RE.search(output)
    return {
        "current_players": int(players_match.group(1)) if players_match else None,
        "version": version_match.group(1) if version_match else None,
    }


def read_max_players():
    try:
        with open(CONFIG_PATH) as f:
            config = json.load(f)
        return config.get("MaxClients", config.get("MaxClientsInSingleplayer"))
    except Exception:
        return None


def read_server_name():
    #A fresh install leaves ServerName a single space, so treat blank as unset.
    try:
        with open(CONFIG_PATH) as f:
            config = json.load(f)
        return (config.get("ServerName") or "").strip() or None
    except Exception:
        return None


def read_last_save_time():
    #When the game itself last wrote the active world, as opposed to AWS Backup's EBS snapshot.
    try:
        with open(CONFIG_PATH) as f:
            config = json.load(f)
        save_file = config.get("WorldConfig", {}).get("SaveFileLocation")
        if not save_file:
            return None
        #Under SQLite's WAL mode writes land in -wal first and are checkpointed back later, so the main
        #file's mtime lags real activity. -shm is the weakest signal, since reads touch it too.
        candidates = [save_file, save_file + "-wal", save_file + "-shm"]
        mtimes = [os.path.getmtime(p) for p in candidates if os.path.exists(p)]
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(max(mtimes))) if mtimes else None
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
    last_hash = None
    if os.path.exists(STATE_FILE):
        with open(STATE_FILE) as f:
            last_hash = f.read().strip()

    while True:
        process_status = read_service_status()
        if process_status == "active":
            stats = read_stats()
            #Process is up but the console isn't answering yet (still loading the world).
            service_status = "active" if stats["version"] is not None else "activating"
        else:
            stats = {"current_players": None, "version": None}
            service_status = process_status

        status = {
            "gameName": "vintagestory",
            "serverName": read_server_name(),
            "version": stats["version"],
            "serviceStatus": service_status,
            "players": {
                "current": stats["current_players"],
                "max": read_max_players(),
            },
            "mods": read_mods(),
            "lastSaveTime": read_last_save_time(),
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

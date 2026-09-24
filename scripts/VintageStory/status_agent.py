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
STATE_FILE = "/home/vintagestory/.status_last_hash"
STATUS_TMP_FILE = "/tmp/status_push.json"
SERVER_SH = "/home/vintagestory/server/server.sh"
#server.sh identifies its own process this way (see PGREPTEST in server.sh) - matching it here means our
#liveness check agrees with the game's own tooling rather than guessing independently
PGREP_PATTERN = "dotnet VintagestoryServer.dll --dataPath {}".format(DATA_PATH)
#read from /etc/game-server-manager, not /tmp: /tmp is cleared on every reboot on this AMI (tmpfs), but
#this module-level code runs every time this long-lived service (re)starts, including after a reboot -
#and this instance always goes through at least one, since it auto-shuts-down ~2 minutes after install.
with open("/etc/game-server-manager/statusBucket.txt") as f:
    STATUS_BUCKET = f.read().strip()
with open("/etc/game-server-manager/statusKey.txt") as f:
    STATUS_KEY = f.read().strip()

#matches /stats output, e.g. "Players online: 1 / 16 (Solimr [69ms])" - verified against a live server
PLAYERS_ONLINE_RE = re.compile(r"Players online:\s*(\d+)\s*/\s*(\d+)", re.IGNORECASE)
#matches /stats' own "24.9.2026 14:44:41 [Notification] Version: 1.22.7" line - verified against a live
#server. Reading the version straight from the running game's own console output, instead of a static file
#written once at install time, means it's always accurate to what's actually running rather than whatever
#was true the last time this long-lived agent process itself (re)started - a real bug found live: an
#in-place update (Lambda's updateGame) restarts the game process directly, not this systemd service, so a
#file-based version went stale across every update until the agent itself happened to restart too.
VERSION_RE = re.compile(r"Version:\s*(\S+)", re.IGNORECASE)


def read_service_status():
    #systemd only sees server.sh's own process, not the game process it detaches into a screen session -
    #so `systemctl is-active` would just reflect whether server.sh's brief startup run succeeded, not
    #whether the actual game is still up. Checking for the game process directly is what server.sh itself
    #does before accepting any command, so it's the accurate signal. Deliberately just "is the OS process
    #there" - see read_stats() below for the finer-grained "is the game actually responding yet" check
    #main() layers on top of this.
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


def read_stats():
    #one /stats call covers both player count and live version - previously two separate concerns (this
    #used to be player-count-only, with version coming from a separate file). If the OS process is up but
    #the game hasn't finished loading yet, /stats prints just "process found ... executing command" / "is
    #up and running" with none of the actual stats block (confirmed live) - version_match stays None in
    #that case, which main() uses as the signal to report "activating" rather than "active".
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
    #the display name players see in the server browser - whatever install.sh patched into
    #serverconfig.json's ServerName field at setup time (see vintagestory-prepare-data.sh). Currently
    #that's just a blank space (VSNAME=" ") on a fresh install, so strip and treat blank as unset rather
    #than reporting a name that's literally just whitespace.
    try:
        with open(CONFIG_PATH) as f:
            config = json.load(f)
        return (config.get("ServerName") or "").strip() or None
    except Exception:
        return None


def read_last_save_time():
    #distinct from AWS Backup's LastBackupTime (a separate, infrastructure-level EBS snapshot) - this is
    #when the game itself last wrote the active world, per serverconfig.json's own WorldConfig.SaveFileLocation
    try:
        with open(CONFIG_PATH) as f:
            config = json.load(f)
        save_file = config.get("WorldConfig", {}).get("SaveFileLocation")
        if not save_file:
            return None
        #SQLite WAL mode: writes land in the -wal file first and only get checkpointed back into the main
        #file periodically, so the main file's own mtime can lag real write activity by an unpredictable
        #amount - check -wal too. -shm's mtime can also be touched by mere read activity, not just writes,
        #so it's the weakest signal of the three; included anyway since nothing else opens this file today.
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
            #process is up but the console isn't answering meaningfully yet (still loading the world) -
            #"activating" is one of the states docs/server-status.md already documents for exactly this
            #"EC2/process running, game not actually ready" case, previously unused by this agent
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

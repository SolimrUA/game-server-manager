#!/usr/bin/env python3
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

# Reports player count/max, version and mods to S3 every 5s, only pushing when something changed.
# See docs/server-status.md for the schema and why this exists.
import glob
import hashlib
import json
import os
import re
import subprocess
import time

#docker-compose.yml lives ON the persistent data volume (see valheim-prepare-data.sh in install.sh),
#bootstrapped once so mod config (TYPE=BepInEx, MODS=...) survives instance replacement.
DATA_DIR = "/usr/games/serverconfig/valheim"
COMPOSE_FILE = os.path.join(DATA_DIR, "docker-compose.yml")
SAVES_GLOB = os.path.join(DATA_DIR, "saves/worlds_local/*.db")
STATE_FILE = "/usr/games/serverconfig/.status_last_hash"
STATUS_TMP_FILE = "/tmp/status_push.json"
#the docker-compose service name (not the container's runtime name, which docker compose suffixes with an
#index like "valheim-1") - `docker compose exec` resolves this against whatever docker-compose.yml defines.
COMPOSE_SERVICE = "valheim"
#read from /etc/game-server-manager, not /tmp: /tmp is cleared on every reboot on this AMI (tmpfs), but
#this module-level code runs every time this long-lived service (re)starts, including after a reboot -
#and this instance always goes through at least one, since it auto-shuts-down ~2 minutes after install.
with open("/etc/game-server-manager/statusBucket.txt") as f:
    STATUS_BUCKET = f.read().strip()
with open("/etc/game-server-manager/statusKey.txt") as f:
    STATUS_KEY = f.read().strip()


def read_odin_status():
    #the one source everything else below is derived from - Odin (the docker image's own launcher/manager)
    #exposes a structured status command from inside the container; returns None on any failure (container
    #not running, docker compose down, unexpected output) so callers can fall back cleanly rather than crash.
    try:
        #--local is required: Odin's default query targets the server's PUBLIC address, which a process
        #inside the container generally can't reach (hairpin NAT - the host's own public IP isn't routable
        #from behind its own NAT/ENI) - confirmed live, this made `online` false and every other field
        #empty/zeroed even while the game was actually up and joinable. --local queries the game over the
        #container's loopback interface instead, which works regardless of network topology.
        result = subprocess.run(
            ["docker", "compose", "-f", COMPOSE_FILE, "exec", "-T", COMPOSE_SERVICE, "odin", "status", "--local", "--json"],
            capture_output=True, text=True, timeout=15,
        )
        if result.returncode != 0:
            print("odin status exited {}: stdout={!r} stderr={!r}".format(result.returncode, result.stdout, result.stderr))
            return None
        #Odin has been observed writing its own log lines (e.g. a "Failed to request server information"
        #ERROR when the game itself isn't up yet) to the same stream as the JSON, ahead of it - rather than
        #confined to stderr the way `capture_output` would cleanly separate. Parse from the first '{' rather
        #than assuming stdout is pure JSON, so a stray log line doesn't fail the whole read.
        stdout = result.stdout
        brace = stdout.find("{")
        if brace == -1:
            print("odin status returned no JSON: stdout={!r} stderr={!r}".format(stdout, result.stderr))
            return None
        return json.loads(stdout[brace:])
    except Exception as e:
        print("read_odin_status failed: {}".format(e))
        return None


#Odin's own version field is a raw internal string like "g=0.221.12,n=36,m=" (game version, network
#version, modified-flag) - confirmed live - rather than a plain version number. Pull out just the game
#version for display; fall back to the raw string if it doesn't match, so an Odin format change degrades
#gracefully instead of hiding the value entirely.
ODIN_VERSION_RE = re.compile(r"g=([\d.]+)")


def parse_odin_version(raw):
    if not raw:
        return raw
    match = ODIN_VERSION_RE.search(raw)
    return match.group(1) if match else raw


def read_service_status(odin_status):
    if odin_status is None:
        return "inactive"
    return "active" if odin_status.get("online") else "inactive"


def read_mods(odin_status):
    #bepinex.mods' exact item shape isn't confirmed against a live server (Odin's own docs don't spell it
    #out) - handle both a list of {name, version} dicts and a list of Thunderstore-style
    #"Author-Package-Version" strings, same "never crash the loop over one bad entry" spirit as Vintage
    #Story's read_mod_info.
    mods = []
    raw = ((odin_status or {}).get("bepinex") or {}).get("mods") or []
    for entry in raw:
        if isinstance(entry, dict):
            name = entry.get("name") or entry.get("Name")
            version = entry.get("version") or entry.get("Version")
            if name:
                mods.append({"name": name, "version": version})
                continue
        if isinstance(entry, str):
            parts = entry.rsplit("-", 2)
            if len(parts) == 3:
                mods.append({"name": parts[0] + "-" + parts[1], "version": parts[2]})
            else:
                mods.append({"name": entry, "version": None})
    return mods


def read_last_save_time():
    #distinct from AWS Backup's LastBackupTime (a separate, infrastructure-level EBS snapshot) - this is
    #when the game itself last wrote the active world. No fixed world name is configured, so glob rather
    #than hardcode one; .fwl is the small metadata file paired with each .db, included as a second signal.
    try:
        candidates = glob.glob(SAVES_GLOB) + glob.glob(SAVES_GLOB[:-3] + ".fwl")
        mtimes = [os.path.getmtime(p) for p in candidates]
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(max(mtimes))) if mtimes else None
    except Exception:
        return None


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
        odin_status = read_odin_status()
        service_status = read_service_status(odin_status)
        active = service_status == "active"
        status = {
            "gameName": "valheim",
            "serverName": (odin_status or {}).get("name") if odin_status else None,
            "version": parse_odin_version((odin_status or {}).get("version")) if odin_status else None,
            "serviceStatus": service_status,
            "players": {
                "current": odin_status.get("players") if active else None,
                "max": odin_status.get("max_players") if active else None,
            },
            "mods": read_mods(odin_status),
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

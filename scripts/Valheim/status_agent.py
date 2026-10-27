#!/usr/bin/env python3
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

# Reports player count/max, version, mods and free disk space to S3 every 5s, pushing only when
# something changed.
# docs/server-status.md documents the JSON schema.
import glob
import hashlib
import json
import os
import re
import subprocess
import time

#On the persistent data volume, so mod config survives instance replacement.
DATA_DIR = "/usr/games/serverconfig/valheim"
COMPOSE_FILE = os.path.join(DATA_DIR, "docker-compose.yml")
SAVES_GLOB = os.path.join(DATA_DIR, "saves/worlds_local/*.db")
STATE_FILE = "/usr/games/serverconfig/.status_last_hash"
STATUS_TMP_FILE = "/tmp/status_push.json"
#The compose service name, not the container's runtime name (suffixed with an index like "valheim-1").
COMPOSE_SERVICE = "valheim"
#/etc/game-server-manager, not /tmp, which this AMI clears on every reboot - this code re-runs on every
#restart of this long-lived service, including after one.
with open("/etc/game-server-manager/statusBucket.txt") as f:
    STATUS_BUCKET = f.read().strip()
with open("/etc/game-server-manager/statusKey.txt") as f:
    STATUS_KEY = f.read().strip()


def read_odin_status():
    #Odin, the image's own launcher, exposes a structured status command from inside the container.
    #None on any failure, so callers degrade rather than crash.
    try:
        #--local is required: Odin otherwise queries the server's public address, which a process inside
        #the container can't reach, and every field comes back empty while the game is up and joinable.
        result = subprocess.run(
            ["docker", "compose", "-f", COMPOSE_FILE, "exec", "-T", COMPOSE_SERVICE, "odin", "status", "--local", "--json"],
            capture_output=True, text=True, timeout=15,
        )
        if result.returncode != 0:
            print("odin status exited {}: stdout={!r} stderr={!r}".format(result.returncode, result.stdout, result.stderr))
            return None
        #Odin writes some of its own log lines into the same stream as the JSON, ahead of it.
        stdout = result.stdout
        brace = stdout.find("{")
        if brace == -1:
            print("odin status returned no JSON: stdout={!r} stderr={!r}".format(stdout, result.stderr))
            return None
        return json.loads(stdout[brace:])
    except Exception as e:
        print("read_odin_status failed: {}".format(e))
        return None


#Odin reports version as a raw internal string like "g=0.221.12,n=36,m=" (game version, network version,
#modified flag), falling back to the raw string if that format changes.
ODIN_VERSION_RE = re.compile(r"g=([\d.]+)")


def parse_odin_version(raw):
    if not raw:
        return raw
    match = ODIN_VERSION_RE.search(raw)
    return match.group(1) if match else raw


def read_container_state():
    #Asked of Docker, not Odin: Odin only answers from inside a running container, so it cannot tell
    #a game still loading from one whose container never started. Returns the container's state,
    #"absent" when compose has no container for the service, or None when Docker itself didn't answer.
    try:
        found = subprocess.run(
            ["docker", "compose", "-f", COMPOSE_FILE, "ps", "-aq", COMPOSE_SERVICE],
            capture_output=True, text=True, timeout=15)
        if found.returncode != 0:
            print("docker compose ps exited {}: stderr={!r}".format(found.returncode, found.stderr))
            return None
        containers = found.stdout.split()
        if not containers:
            return "absent"
        state = subprocess.run(
            ["docker", "inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", containers[0]],
            capture_output=True, text=True, timeout=15)
        if state.returncode != 0:
            print("docker inspect exited {}: stderr={!r}".format(state.returncode, state.stderr))
            return None
        return state.stdout.strip()
    except Exception as e:
        print("read_container_state failed: {}".format(e))
        return None


def read_service_status(container_state, odin_status):
    #Odin reporting online is the only thing that means the game is up. Everything else is read from
    #the container, which is what distinguishes a game still loading from one that died.
    if container_state is None:
        return "unknown"
    status, _, exit_code = container_state.partition(" ")
    if status == "running":
        return "active" if (odin_status or {}).get("online") else "activating"
    if status == "restarting":
        #The container is being brought back after dying, so the game is crash-looping.
        return "failed"
    if status in ("exited", "dead"):
        return "inactive" if exit_code.strip() == "0" else "failed"
    #"absent" (compose down, or an `up` that never succeeded), "created" and "paused".
    return "inactive"


def read_mods(odin_status):
    #Odin's docs don't pin down bepinex.mods' item shape, so handle both {name, version} dicts and
    #Thunderstore-style "Author-Package-Version" strings.
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
    #When the game itself last wrote the active world, as opposed to AWS Backup's EBS snapshot. No fixed
    #world name is configured, so glob for it.
    try:
        candidates = glob.glob(SAVES_GLOB) + glob.glob(SAVES_GLOB[:-3] + ".fwl")
        mtimes = [os.path.getmtime(p) for p in candidates]
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(max(mtimes))) if mtimes else None
    except Exception:
        return None


GIB = 1024 ** 3


def read_disk():
    #Root as well as data: logs and docker images fill root, the world and its archives fill data,
    #and either one filling breaks the server.
    #Floored to a tenth of a GiB, because this feeds the change hash that decides whether to push -
    #byte-exact free space would make every poll a push. Floored, not rounded, so a reading never
    #claims more room than the server has.
    disks = {}
    for name, path in (("data", DATA_DIR), ("root", "/")):
        try:
            stat = os.statvfs(path)
        except OSError:
            continue
        disks[name] = {
            "totalGib": stat.f_blocks * stat.f_frsize * 10 // GIB / 10,
            #f_bavail, not f_bfree: the reserve only root may use is not room the game can write into.
            "freeGib": stat.f_bavail * stat.f_frsize * 10 // GIB / 10,
        }
    return disks


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
        container_state = read_container_state()
        #Only asked when there is a container to ask - exec into a stopped one just fails, which
        #would fill the journal with a failure every five seconds while the server is down.
        odin_status = read_odin_status() if (container_state or "").startswith("running") else None
        service_status = read_service_status(container_state, odin_status)
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
            "disk": read_disk(),
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

# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

sudo apt update && sudo apt upgrade -y
sudo apt install -y wget curl tar unzip jq apt-transport-https ca-certificates gnupg lsb-release python3

#install AWS CLI
sudo curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
sudo unzip -o awscliv2.zip
sudo ./aws/install

#install the .NET 8.0 runtime required by the Vintage Story dedicated server
wget https://packages.microsoft.com/config/ubuntu/$(lsb_release -rs)/packages-microsoft-prod.deb -O /tmp/packages-microsoft-prod.deb
sudo dpkg -i /tmp/packages-microsoft-prod.deb
sudo apt update
sudo apt install -y dotnet-runtime-8.0

#create random string for join password
VSPW=$(echo $RANDOM | md5sum | head -c 20)

#get stackname created by user data script and update SSM parameter name with this to make it unique
STACKNAME=$(</tmp/paramName.txt)
PARAMNAME=game-password-$STACKNAME

#put random string into parameter store as encrypted string value
aws ssm put-parameter --name $PARAMNAME --value $VSPW --type "SecureString" --overwrite

#pinned server version - check https://account.vintagestory.at/downloads for newer stable releases
VSVERSION=1.22.6
VSPORT=42420
VSNAME="MyAWSGamingServer"

#create a dedicated, unprivileged user to run the server under
id -u vintagestory &>/dev/null || sudo useradd vintagestory -m
sudo mkdir -p /home/vintagestory/server /home/vintagestory/data
echo $VSVERSION | sudo -u vintagestory tee /home/vintagestory/version.txt > /dev/null

#download and unpack the dedicated server
cd /tmp
sudo wget -O vs_server.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VSVERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server.tar.gz
sudo chown -R vintagestory:vintagestory /home/vintagestory
sudo chmod +x /home/vintagestory/server/VintagestoryServer

#running the server once (and letting it time out) makes it write the default serverconfig.json,
#which we then patch with our own port/name/password/visibility settings before the real start
sudo -u vintagestory timeout 30 /home/vintagestory/server/VintagestoryServer --dataPath /home/vintagestory/data || true

CONFIGFILE=/home/vintagestory/data/serverconfig.json
sudo -u vintagestory jq \
  --argjson port $VSPORT \
  --arg name "$VSNAME" \
  --arg pw "$VSPW" \
  '.Port=$port | .ServerName=$name | .Password=$pw | .Upnp=false | .AdvertiseServer=true' \
  $CONFIGFILE | sudo -u vintagestory tee /tmp/serverconfig.json.tmp > /dev/null
sudo -u vintagestory mv /tmp/serverconfig.json.tmp $CONFIGFILE

#systemd service so the server starts on boot and gets restarted if it crashes
sudo bash -c 'cat > /etc/systemd/system/vintagestory.service' <<EOF
[Unit]
Description=Vintage Story Dedicated Server
After=network.target

[Service]
Type=simple
User=vintagestory
WorkingDirectory=/home/vintagestory/server
ExecStart=/home/vintagestory/server/VintagestoryServer --dataPath /home/vintagestory/data
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory
sudo systemctl start vintagestory

#status agent - reports player count/max, version and mods to S3 every 5s, only pushing when something changed.
#See docs/server-status.md for the schema and why this exists. Log line patterns below are Vintage Story's best-known
#join/leave format as of VSVERSION above - if player counts don't track correctly against your server-main.log, this
#is the regex to adjust.
sudo bash -c 'cat > /home/vintagestory/status_agent.py' <<'PYEOF'
#!/usr/bin/env python3
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
PYEOF

sudo chown vintagestory:vintagestory /home/vintagestory/status_agent.py
sudo chmod +x /home/vintagestory/status_agent.py

sudo bash -c 'cat > /etc/systemd/system/vintagestory-status-agent.service' <<EOF
[Unit]
Description=Vintage Story status agent (player count/mods/version to S3)
After=vintagestory.service
Requires=vintagestory.service

[Service]
Type=simple
User=vintagestory
WorkingDirectory=/home/vintagestory
ExecStart=/usr/bin/python3 /home/vintagestory/status_agent.py
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vintagestory-status-agent
sudo systemctl start vintagestory-status-agent

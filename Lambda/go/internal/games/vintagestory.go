package games

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"personal-game-server-manager/lambda/internal/remote"
	"personal-game-server-manager/lambda/internal/usererr"
)

type vintageStory struct{}

// serverScript is the control script the install puts on every Vintage Story server. Its `command`
// subcommand injects a line into the running server's console.
const serverScript = "/home/vintagestory/server/server.sh"

// modsSubdir mirrors status_agent.py's MODS_PATH.
const modsSubdir = "Mods"

func (vintageStory) console(command string) string {
	return "sudo -u vintagestory " + serverScript + " command " + remote.Quote(command)
}

// InstallMod hands the install to the game's own moddb client, which needs the server's game
// version as an explicit argument: left off, the server looks "current" up itself through a request
// that fails inside that client.
func (g vintageStory) InstallMod(ctx context.Context, req ModRequest) ([]string, error) {
	if req.Version == "" {
		return nil, usererr.New("Couldn't determine the server's current game version - " +
			"try again once the status agent has reported it")
	}
	if err := modRegistry(ctx, req.Ref, req.Version); err != nil {
		return nil, err
	}
	return []string{g.console("moddb install " + req.Ref + " " + req.Version)}, nil
}

// DeleteMod resolves the display name the caller has to the modid the game's moddb client needs -
// e.g. "Carry Capacity" against "carrycapacity".
func (g vintageStory) DeleteMod(req ModRequest) ([]string, error) {
	find := fmt.Sprintf(findModIDScript, pythonString(req.DataPath+"/"+modsSubdir), pythonString(req.Name))
	return []string{
		// The newline is load bearing: bash needs the closing PYEOF alone on its line, or it finds no
		// terminator and reads to end-of-script.
		"MODID=$(" + find + "\n)",
		// $(...) swallows the find script's "NO_MATCH" into MODID instead of stdout - re-echo it so the
		// caller's check for it still sees it.
		`if [ $? -ne 0 ]; then echo "NO_MATCH"; exit 1; fi`,
		`sudo -u vintagestory ` + serverScript + ` command "moddb remove $MODID"`,
	}, nil
}

func (vintageStory) DeleteFailure(req ModRequest, stdout string) error {
	if strings.Contains(stdout, "NO_MATCH") {
		return usererr.Newf("Couldn't find a mod matching %q to delete", req.Name)
	}
	return nil
}

func (g vintageStory) Restart(string) ([]string, error) {
	// Backgrounded because a restart takes longer than the control Lambda may wait for it. Logged to
	// a file rather than discarded, so a restart that fails to bring the game back says why.
	return []string{
		`setsid sh -c "sudo -u vintagestory ` + serverScript + ` restart" </dev/null >/tmp/game-restart.log 2>&1 &`,
	}, nil
}

// Update writes the update out as a script and backgrounds it, since downloading and extracting a
// release takes minutes. Logged to a file rather than discarded, so a failure partway through is
// diagnosable.
func (g vintageStory) Update(dataPath string) ([]string, error) {
	script := strings.ReplaceAll(updateScript, "__DATAPATH__", dataPath)
	return []string{
		// The quoted delimiter is required: unquoted, bash expands $VERSION while writing the file
		// instead of leaving it for the script to expand when it runs.
		"cat > /tmp/vs-update.sh <<'VSUPDATEEOF'\n" + script + "\nVSUPDATEEOF\n",
		"chmod +x /tmp/vs-update.sh",
		"setsid /tmp/vs-update.sh </dev/null >/tmp/vs-update.log 2>&1 &",
	}, nil
}

// ArchiveWorld uses the game's own genbackup command for the world alone, which produces one
// consistent snapshot file - avoiding both the Saves folder's other worlds, backups and screenshots
// and any risk of missing writes still sitting in the live save's -wal file.
func (g vintageStory) ArchiveWorld(req ArchiveRequest) ([]string, error) {
	if req.Scope != Core {
		commands := zipFolders(req)
		// serverconfig.json holds the join password - zip a redacted copy, never the real file.
		commands = append(commands,
			`jq "del(.Password)" `+req.DataPath+`/serverconfig.json > /tmp/serverconfig-redacted.json`,
			"zip -j /tmp/worldsave.zip /tmp/serverconfig-redacted.json",
			"rm -f /tmp/serverconfig-redacted.json",
		)
		return append(commands, uploadAndClean(req)...), nil
	}

	backupName := "manualbackup-" + time.Now().UTC().Format("2006-01-02-1504")
	backupFile := req.DataPath + "/Backups/" + backupName
	commands := []string{
		"cd " + req.DataPath,
		g.console("genbackup " + backupName),
		// genbackup runs asynchronously - wait for the file to appear and its size to stop changing.
		"BACKUP_FILE=" + remote.Quote(backupFile),
		`for i in $(seq 1 100); do ` +
			`if [ -f "$BACKUP_FILE" ]; then ` +
			`S1=$(stat -c%s "$BACKUP_FILE"); sleep 3; S2=$(stat -c%s "$BACKUP_FILE"); ` +
			`[ "$S1" = "$S2" ] && break; ` +
			`fi; ` +
			`sleep 3; ` +
			`done`,
		`if [ ! -f "$BACKUP_FILE" ]; then echo "genbackup never produced a file" >&2; exit 1; fi`,
		`cp "$BACKUP_FILE" /tmp/world.vcdbs`,
		`rm -f "$BACKUP_FILE"`,
		"zip -j /tmp/worldsave.zip /tmp/world.vcdbs",
		"rm -f /tmp/world.vcdbs",
	}
	return append(commands, uploadAndClean(req)...), nil
}

// zipFolders zips only the folders the scope names. A brand new world may not have every folder
// yet, which one `zip -r` over the lot would fail on.
func zipFolders(req ArchiveRequest) []string {
	var quoted []string
	for _, path := range strings.Split(req.IncludePaths, ",") {
		quoted = append(quoted, strconv.Quote(strings.TrimSpace(path)))
	}
	return []string{
		"cd " + req.DataPath + " && for p in " + strings.Join(quoted, " ") +
			`; do [ -e "$p" ] && zip -r /tmp/worldsave.zip "$p"; done`,
	}
}

func uploadAndClean(req ArchiveRequest) []string {
	return []string{
		"aws s3 cp /tmp/worldsave.zip s3://" + req.Bucket + "/" + req.Key,
		"rm -f /tmp/worldsave.zip",
	}
}

// findModIDScript reads modinfo.json the same way status_agent.py's read_mods() does, so it
// resolves the same entry the control panel is showing.
const findModIDScript = `python3 - <<'PYEOF'
import json, os, sys, zipfile

MODS_PATH = %s
target = %s

def read_info(read_fn):
    try:
        return json.loads(read_fn())
    except Exception:
        return None

modid = None
if os.path.isdir(MODS_PATH):
    for entry in os.listdir(MODS_PATH):
        path = os.path.join(MODS_PATH, entry)
        info = None
        if entry.lower().endswith(".zip"):
            try:
                with zipfile.ZipFile(path) as zf:
                    info = read_info(lambda: zf.read("modinfo.json").decode("utf-8"))
            except Exception:
                pass
        elif os.path.isdir(path):
            modinfo_path = os.path.join(path, "modinfo.json")
            if os.path.exists(modinfo_path):
                info = read_info(lambda: open(modinfo_path).read())
        if info and (info.get("name") or info.get("Name")) == target:
            modid = info.get("modid") or info.get("ModID") or info.get("Modid")
            break

if not modid:
    print("NO_MATCH")
    sys.exit(1)
print(modid)
PYEOF`

// updateScript reimplements the update sequence, because server.sh's own `update` subcommand is a
// disabled stub that prints manual instructions and exits 0.
const updateScript = `#!/bin/bash
set -e

#The API lists pre-release tags like "1.4.4-dev.2" alongside stable ones.
VERSION=$(curl -fsS https://mods.vintagestory.at/api/gameversions | jq -r '[.gameversions[].name | select(contains("-") | not)] | last')
if [ -z "$VERSION" ] || [ "$VERSION" = "null" ]; then
  echo "Couldn't determine the latest available version" >&2
  exit 1
fi
echo "Updating Vintage Story server to $VERSION"

sudo -u vintagestory /home/vintagestory/server/server.sh stop

#Extracting over an existing install mixes old and new asset files, and the game then refuses to start
#("Your Server installation still contains old files from a previous game version"). Safe to wipe:
#this directory only ever holds the game binary, and DataPath is a separate top-level directory.
sudo rm -rf /home/vintagestory/server/*

cd /tmp
sudo wget -O vs_server_update.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server_update.tar.gz
#The tarball is root-owned (sudo wget), so a plain rm -f leaves it behind with "Operation not permitted".
sudo rm -f vs_server_update.tar.gz

#server/ only, never all of /home/vintagestory: an update must not touch the world save.
sudo chown -R vintagestory:vintagestory /home/vintagestory/server
sudo chmod +x /home/vintagestory/server/VintagestoryServer /home/vintagestory/server/server.sh

#The extracted server.sh ships the stock DATAPATH - repatch it, or the server silently comes back up
#pointed at the wrong data directory.
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='__DATAPATH__'|" /home/vintagestory/server/server.sh

sudo -u vintagestory /home/vintagestory/server/server.sh start
echo "Update complete: $VERSION"
`

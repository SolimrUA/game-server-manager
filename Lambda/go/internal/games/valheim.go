package games

import (
	"context"
	"fmt"
	"strings"

	"personal-game-server-manager/lambda/internal/remote"
	"personal-game-server-manager/lambda/internal/usererr"
)

type valheim struct{}

// composeFile is where the server's container is configured, which is also where its mod list lives.
func (valheim) composeFile(dataPath string) string {
	return dataPath + "/docker-compose.yml"
}

func (g valheim) InstallMod(_ context.Context, req ModRequest) ([]string, error) {
	compose := g.composeFile(req.DataPath)
	return []string{
		// The mod manager silently ignores MODS unless TYPE=BepInEx is set. Fail loudly rather than
		// leave the file edited and the mod never loaded; enabling BepInEx is not done automatically.
		`grep -q "^ *- TYPE=BepInEx$" ` + remote.Quote(compose) +
			` || { echo "BepInEx is not enabled on this server - set TYPE=BepInEx in docker-compose.yml first" >&2; exit 1; }`,
		fmt.Sprintf(modInsertScript, pythonString(compose), pythonString(req.Ref)),
	}, nil
}

func (g valheim) DeleteMod(req ModRequest) ([]string, error) {
	return []string{
		fmt.Sprintf(modDeleteScript, pythonString(g.composeFile(req.DataPath)), pythonString(req.Name)),
	}, nil
}

func (valheim) DeleteFailure(req ModRequest, stdout string) error {
	if strings.Contains(stdout, "NO_MATCH") {
		return usererr.Newf("Couldn't find a mod matching %q to delete", req.Name)
	}
	return nil
}

// Restart brings the container down and back up, backgrounded because the graceful-stop period
// alone is 60 seconds - longer than the control Lambda may wait. Logged to a file rather than
// discarded, so a restart that takes the server down and fails to bring it back says why.
func (g valheim) Restart(dataPath string) ([]string, error) {
	return []string{
		`setsid sh -c "cd ` + remote.Quote(dataPath) + ` && docker compose down && docker compose up -d" </dev/null >/tmp/game-restart.log 2>&1 &`,
	}, nil
}

func (valheim) Update(string) ([]string, error) {
	return nil, ErrUnsupported
}

func (g valheim) ArchiveWorld(req ArchiveRequest) ([]string, error) {
	return append(zipFolders(req), uploadAndClean(req)...), nil
}

// MODS is a YAML block scalar: its first line carries the "MODS=" prefix and every following line
// at that indentation is a bare entry. Both scripts rewrite the whole block, since editing one line
// in place would drop the prefix whenever that line was the first one.
const modInsertScript = `python3 - <<'PYEOF'
path = %s
modref = %s
with open(path) as f:
    lines = f.readlines()

out = []
i = 0
inserted = False
while i < len(lines):
    line = lines[i]
    stripped = line.strip()
    if stripped.startswith("MODS="):
        indent = line[:len(line) - len(line.lstrip())]
        entries = [stripped[len("MODS="):]]
        j = i + 1
        while j < len(lines):
            nxt = lines[j]
            nxt_stripped = nxt.strip()
            nxt_indent_len = len(nxt) - len(nxt.lstrip())
            if nxt_stripped == "" or nxt_indent_len < len(indent):
                break
            entries.append(nxt_stripped)
            j += 1
        entries.append(modref)
        out.append(indent + "MODS=" + entries[0] + "\n")
        for e in entries[1:]:
            out.append(indent + e + "\n")
        inserted = True
        i = j
        continue
    out.append(line)
    i += 1

if not inserted:
    print("NO_MODS_BLOCK")
    raise SystemExit(1)

with open(path, "w") as f:
    f.writelines(out)
print("INSERTED")
PYEOF`

const modDeleteScript = `python3 - <<'PYEOF'
path = %s
target = %s
with open(path) as f:
    lines = f.readlines()

out = []
i = 0
removed = False
while i < len(lines):
    line = lines[i]
    stripped = line.strip()
    if stripped.startswith("MODS="):
        indent = line[:len(line) - len(line.lstrip())]
        entries = [stripped[len("MODS="):]]
        j = i + 1
        while j < len(lines):
            nxt = lines[j]
            nxt_stripped = nxt.strip()
            nxt_indent_len = len(nxt) - len(nxt.lstrip())
            if nxt_stripped == "" or nxt_indent_len < len(indent):
                break
            entries.append(nxt_stripped)
            j += 1
        new_entries = []
        for e in entries:
            #Namespace-Name-Version; Thunderstore namespaces and names never contain "-" themselves.
            parts = e.split("-")
            name = parts[1] if len(parts) >= 3 else None
            if name == target and not removed:
                removed = True
                continue
            new_entries.append(e)
        if new_entries:
            out.append(indent + "MODS=" + new_entries[0] + "\n")
            for e in new_entries[1:]:
                out.append(indent + e + "\n")
        elif out and out[-1].strip() == "- |":
            #The entries are gone, so the block scalar introducing them goes too: an empty block
            #scalar is an empty environment entry, and the container will not start with one.
            out.pop()
        i = j
        continue
    out.append(line)
    i += 1

if not removed:
    print("NO_MATCH")
    raise SystemExit(1)

with open(path, "w") as f:
    f.writelines(out)
print("REMOVED")
PYEOF`

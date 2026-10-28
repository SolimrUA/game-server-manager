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

// InstallMod adds the mod to the compose file's MODS list, starting that list - and setting
// TYPE=BepInEx, without which the image ignores MODS entirely - on a server that has neither yet.
// The mod itself is downloaded by the image the next time the container starts.
func (g valheim) InstallMod(_ context.Context, req ModRequest) ([]string, error) {
	return []string{
		fmt.Sprintf(modInsertScript, pythonString(g.composeFile(req.DataPath)), pythonString(req.Ref)),
	}, nil
}

// remainingModsFile hands the list left after the edit to the command that reconciles against it.
const remainingModsFile = "/tmp/valheim-mods-remaining.txt"

// DeleteMod drops the mod from the compose file's MODS list and then has Odin reconcile, which is
// what actually removes the installed files. The image runs that reconcile itself on startup, but
// only when MODS is non-empty, so removing the last mod would otherwise leave it installed and
// loaded. The container is checked first, so a server that can't reconcile isn't left edited.
func (g valheim) DeleteMod(req ModRequest) ([]string, error) {
	composeFile := g.composeFile(req.DataPath)
	compose := remote.Quote(composeFile)
	return []string{
		// Run Command keeps going after a failed command, so without this a refused edit would still
		// reach the reconcile below - with no list to reconcile against, which uninstalls everything.
		`set -e`,
		`rm -f ` + remainingModsFile,
		`docker compose -f ` + compose + ` ps -q valheim | grep -q . || { echo NOT_RUNNING; exit 1; }`,
		fmt.Sprintf(modDeleteScript, pythonString(composeFile), pythonString(req.Name),
			pythonString(remainingModsFile)),
		// A running container keeps the MODS it started with, so the new list is passed in for this
		// one invocation. Empty is meaningful: it uninstalls everything Odin installed.
		`docker compose -f ` + compose + ` exec -T -e MODS="$(cat ` + remainingModsFile +
			`)" valheim odin mod:install --from-var`,
	}, nil
}

func (valheim) DeleteFailure(req ModRequest, stdout string) error {
	if strings.Contains(stdout, "NOT_RUNNING") {
		return usererr.New("Start the server first - removing a mod's files needs its container running")
	}
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


def indent_of(line):
    return len(line) - len(line.lstrip())


with open(path) as f:
    lines = f.readlines()

env = -1
for i, line in enumerate(lines):
    if line.strip() == "environment:":
        env = i
        break
if env < 0:
    import sys
    sys.stderr.write("No environment: section in docker-compose.yml to add mods to\n")
    raise SystemExit(1)

#The list runs until the next line belonging to something other than it.
env_indent = indent_of(lines[env])
end = len(lines)
for j in range(env + 1, len(lines)):
    if lines[j].strip() and indent_of(lines[j]) <= env_indent:
        end = j
        break

entry_indent = env_indent + 2
for j in range(env + 1, end):
    if lines[j].lstrip().startswith("- "):
        entry_indent = indent_of(lines[j])
        break

#The image ignores MODS unless the server type is BepInEx, so asking for a mod sets it.
type_line = -1
for j in range(env + 1, end):
    if lines[j].strip().startswith("- TYPE="):
        type_line = j
        break
if type_line < 0:
    lines.insert(end, " " * entry_indent + "- TYPE=BepInEx\n")
    end += 1
elif lines[type_line].strip() != "- TYPE=BepInEx":
    lines[type_line] = " " * entry_indent + "- TYPE=BepInEx\n"

mods_line = -1
for j in range(env + 1, end):
    if lines[j].strip().startswith("MODS="):
        mods_line = j
        break

if mods_line < 0:
    #MODS is a YAML block scalar: "- |" introduces it and its first line carries the MODS= prefix.
    lines.insert(end, " " * entry_indent + "- |\n")
    lines.insert(end + 1, " " * (entry_indent + 2) + "MODS=" + modref + "\n")
else:
    #Every line of the block is rewritten, since editing one in place would drop the prefix whenever
    #that line was the first one.
    indent = " " * indent_of(lines[mods_line])
    entries = [lines[mods_line].strip()[len("MODS="):]]
    j = mods_line + 1
    while j < end:
        if not lines[j].strip() or indent_of(lines[j]) < len(indent):
            break
        entries.append(lines[j].strip())
        j += 1
    entries.append(modref)
    lines[mods_line:j] = [indent + "MODS=" + entries[0] + "\n"] + [indent + e + "\n" for e in entries[1:]]

with open(path, "w") as f:
    f.writelines(lines)
print("INSERTED")
PYEOF`

const modDeleteScript = `python3 - <<'PYEOF'
path = %s
target = %s
modsfile = %s
with open(path) as f:
    lines = f.readlines()

out = []
i = 0
removed = False
new_entries = []
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
#What Odin reconciles against, so it has to be what the file now says - including nothing at all,
#which is how the last mod gets uninstalled.
with open(modsfile, "w") as f:
    f.write("\n".join(new_entries))
print("REMOVED")
PYEOF`

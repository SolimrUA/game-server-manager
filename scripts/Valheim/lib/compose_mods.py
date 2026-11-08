#!/usr/bin/env python3
# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

"""Edits the MODS list in a server's docker-compose.yml.

MODS is a YAML block scalar: its first line carries the "MODS=" prefix and every following line at
that indentation is a bare entry. Both edits rewrite the whole block, since editing one line in
place would drop the prefix whenever that line was the first one.
"""

import sys


def indent_of(line):
    return len(line) - len(line.lstrip())


def read_lines(path):
    with open(path) as f:
        return f.readlines()


def write_lines(path, lines):
    with open(path, "w") as f:
        f.writelines(lines)


def environment_block(lines):
    env = -1
    for i, line in enumerate(lines):
        if line.strip() == "environment:":
            env = i
            break
    if env < 0:
        sys.stderr.write("No environment: section in docker-compose.yml to add mods to\n")
        raise SystemExit(1)

    # A YAML mapping ends at the first non-blank line indented no deeper than its key.
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
    return env, end, entry_indent


def set_server_type(lines, env, end, entry_indent):
    """The image ignores MODS unless the server type is BepInEx, so asking for a mod sets it."""
    type_line = -1
    for j in range(env + 1, end):
        if lines[j].strip().startswith("- TYPE="):
            type_line = j
            break
    if type_line < 0:
        lines.insert(end, " " * entry_indent + "- TYPE=BepInEx\n")
        return end + 1
    if lines[type_line].strip() != "- TYPE=BepInEx":
        lines[type_line] = " " * entry_indent + "- TYPE=BepInEx\n"
    return end


def mods_line_in(lines, env, end):
    for j in range(env + 1, end):
        if lines[j].strip().startswith("MODS="):
            return j
    return -1


def block_entries(lines, mods_line, stop):
    indent = " " * indent_of(lines[mods_line])
    entries = [lines[mods_line].strip()[len("MODS="):]]
    j = mods_line + 1
    while j < stop:
        if not lines[j].strip() or indent_of(lines[j]) < len(indent):
            break
        entries.append(lines[j].strip())
        j += 1
    return indent, entries, j


def mods_block(indent, entries):
    return [indent + "MODS=" + entries[0] + "\n"] + [indent + e + "\n" for e in entries[1:]]


def insert(path, modref):
    lines = read_lines(path)
    env, end, entry_indent = environment_block(lines)
    end = set_server_type(lines, env, end, entry_indent)

    mods_line = mods_line_in(lines, env, end)
    if mods_line < 0:
        lines.insert(end, " " * entry_indent + "- |\n")
        lines.insert(end + 1, " " * (entry_indent + 2) + "MODS=" + modref + "\n")
    else:
        indent, entries, after = block_entries(lines, mods_line, end)
        entries.append(modref)
        lines[mods_line:after] = mods_block(indent, entries)

    write_lines(path, lines)
    print("INSERTED")


def without(entries, target, already_removed):
    """Drops the first entry naming the mod. Entries are Namespace-Name-Version, and Thunderstore
    namespaces and names never contain "-" themselves."""
    kept = []
    removed = already_removed
    for entry in entries:
        parts = entry.split("-")
        name = parts[1] if len(parts) >= 3 else None
        if name == target and not removed:
            removed = True
            continue
        kept.append(entry)
    return kept, removed


def delete(path, target, modsfile):
    lines = read_lines(path)

    out = []
    i = 0
    removed = False
    kept = []
    while i < len(lines):
        if not lines[i].strip().startswith("MODS="):
            out.append(lines[i])
            i += 1
            continue

        indent, entries, after = block_entries(lines, i, len(lines))
        kept, removed = without(entries, target, removed)
        if kept:
            out.extend(mods_block(indent, kept))
        elif out and out[-1].strip() == "- |":
            # The entries are gone, so the block scalar introducing them goes too: an empty block
            # scalar is an empty environment entry, and the container will not start with one.
            out.pop()
        i = after

    if not removed:
        print("NO_MATCH")
        raise SystemExit(1)

    write_lines(path, out)
    # What Odin reconciles against, so it has to be what the file now says - including nothing at all.
    with open(modsfile, "w") as f:
        f.write("\n".join(kept))
    print("REMOVED")


USAGE = """usage: compose_mods.py insert <compose file> <mod reference>
       compose_mods.py delete <compose file> <mod name> <remaining mods file>
"""


def main(argv):
    if len(argv) == 4 and argv[1] == "insert":
        insert(argv[2], argv[3])
    elif len(argv) == 5 and argv[1] == "delete":
        delete(argv[2], argv[3], argv[4])
    else:
        sys.stderr.write(USAGE)
        raise SystemExit(64)


if __name__ == "__main__":
    main(sys.argv)

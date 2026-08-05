#!/usr/bin/env bash
# Shellcheck the command blocks Taskfile.yml runs, which nothing else reads. They
# come from a dry run rather than from parsing the YAML, so what is linted is
# what Task would execute; --verbose because a silent target prints nothing under
# --dry alone. bash rather than sh, for the pipefail those blocks use.
set -euo pipefail

task_exe="${1:?the task executable to drive the dry run with}"

# The wrapper rather than a shellcheck on PATH; see the head of that script.
shellcheck="$(dirname -- "${BASH_SOURCE[0]}")/shellcheck.sh"

listed="$("$task_exe" --list-all)"
targets="$(printf '%s\n' "$listed" | sed -n 's/^\* \([A-Za-z0-9:_-]*\):.*/\1/p')"
if [ -z "$targets" ]; then
	echo "no target was listed, so this would have shellchecked nothing" >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/blocks"

# A dry run decides nothing, so the value handed to the two targets that require
# one is never read; it is supplied because they refuse to render without it.
# Task announces on stderr and a target's own output is not wanted, so the two
# streams are separated rather than merged.
: >"$work/dry"
for target in $targets; do
	"$task_exe" --dry --verbose "$target" VERSION=unread-by-a-dry-run >/dev/null 2>>"$work/dry"
done

# Task announces each command with one prefixed line naming the target and prints
# the rest raw, so that prefix is the boundary and what it names is the file
# name. Its other output carries the same prefix without the bracketed name and
# is dropped. A line belonging to neither is refused rather than passed over.
report="$(awk -v dir="$work/blocks" '
  /^task: \[[^]]*\] / {
    name = $0
    sub(/^task: \[/, "", name)
    name = substr(name, 1, index(name, "]") - 1)
    gsub(/[^A-Za-z0-9_-]/, "_", name)
    seen[name]++
    blocks++
    file = sprintf("%s/%s.%d.sh", dir, name, seen[name])
    sub(/^task: \[[^]]*\] /, "")
    print > file
    next
  }
  /^task: / { file = ""; next }
  {
    if (file == "") { stray++; next }
    print > file
  }
  END {
    if (blocks == 0) { print "the dry run rendered no command block at all" }
    else if (stray > 0) { printf "%d line(s) of dry-run output belonged to no command block\n", stray }
  }
' "$work/dry")"
if [ -n "$report" ]; then
	echo "$report" >&2
	exit 1
fi

status=0
"$shellcheck" -s bash "$work"/blocks/*.sh || status="$?"
exit "$status"

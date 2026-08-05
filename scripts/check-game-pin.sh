#!/usr/bin/env bash
# Fails if the depot serves a game build other than the manifest gid the
# Dockerfile pins. Exit 1 is a moved pin; exit 2 is the depot not having answered
# -- an outage, a docker failure, a reply that is not a gid -- which is no
# evidence about it, and reporting one as the other teaches people to ignore this.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Through whichever Task is running, because `task` is not what the executable is
# called on every machine.
task_exe="${1:-task}"

pinned="$(./scripts/read-game-manifest.sh)"

# Tested rather than left to errexit, which would end the run before the failure
# could be told apart from a moved pin.
if ! served="$("$task_exe" isa:manifest)"; then
	echo "could not ask the depot what it serves: '${task_exe} isa:manifest' failed; whether the pin has moved is unknown" >&2
	exit 2
fi

case "${served}" in
"" | *[!0-9]*)
	echo "the depot answered '${served}', which is not a single manifest gid; whether the pin has moved is unknown" >&2
	exit 2
	;;
esac

if [ "${served}" != "${pinned}" ]; then
	echo "the game has been updated: the Dockerfile pins manifest ${pinned} and the depot now serves ${served}" >&2
	echo "task gamesrc rebuilds the decompiled tree from the pin; docs/game-updates.md is the whole procedure, which begins by moving it" >&2
	exit 1
fi

echo "the pin and the depot both name ${served}"

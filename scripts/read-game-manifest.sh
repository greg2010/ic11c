#!/usr/bin/env bash
# Prints the depot manifest gid the Dockerfile pins, and exports it to the steps
# that follow. Read from the Dockerfile rather than repeated in each caller, so
# moving the pin stays one edit. The digit test rejects a multi-line capture, so
# a Dockerfile carrying two fails here rather than yielding whichever sed found.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

gid="$(sed -n 's/^ARG STEAM_MANIFEST=\([0-9][0-9]*\)$/\1/p' "${root}/Dockerfile")"
case "${gid}" in
	"" | *[!0-9]*)
		echo "no single pinned STEAM_MANIFEST gid in the Dockerfile" >&2
		exit 1
		;;
esac

printf '%s\n' "${gid}"

# Absent outside a workflow, where there is no later step to carry it to.
if [ -n "${GITHUB_ENV:-}" ]; then
	echo "STEAM_MANIFEST=${gid}" >>"${GITHUB_ENV}"
fi

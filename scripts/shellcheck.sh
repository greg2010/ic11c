#!/usr/bin/env bash
# Runs shellcheck from a digest-pinned container, so nothing needs one installed
# and every machine reads the same version. Arguments pass through untouched and
# stdin is connected, so this stands in for the binary wherever one is expected
# -- including actionlint's -shellcheck, which hands each run block over on stdin.
set -euo pipefail

image=koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d

# Every directory holding a file argument, mounted where the host has it, so a
# relative path resolves against the working directory and
# --source-path=SCRIPTDIR reaches the helpers a script sources.
mounts=()
mounted=""
add_mount() {
	case ":${mounted}:" in
	*":$1:"*) return 0 ;;
	esac
	mounted="${mounted}:$1"
	mounts+=(--volume "$1:$1:ro")
}

add_mount "${PWD}"
for arg in "$@"; do
	[ -f "${arg}" ] || continue
	add_mount "$(cd -- "$(dirname -- "${arg}")" && pwd)"
done

exec docker run --rm --interactive --network none \
	"${mounts[@]}" --workdir "${PWD}" "${image}" "$@"

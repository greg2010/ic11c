#!/usr/bin/env bash
# Fails if the macOS artifact links anything the operating system does not
# already provide. A Homebrew path here means a component resolved to a dylib
# instead of an archive, which works on the build machine and nowhere else.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

bin="$(local_path "${1:-ic11c}")"

# otool -L prints the binary's own path first, then one dependency per line. Its
# own statement, so a failing otool aborts rather than yielding an empty list.
deps="$(otool -L "${bin}" | tail -n +2 | awk '{print $1}')"

# libSystem is tracked rather than merely allowed, so the extraction is asserted
# instead of assumed: an allowlist passes an empty list, and an output shape this
# no longer matches yields nothing. Apple ships no static libSystem, so every
# Mach-O names it and its absence means no load commands were read.
system=0
foreign=()
while IFS= read -r dep; do
	[ -n "${dep}" ] || continue
	case "${dep}" in
		/usr/lib/libSystem.B.dylib) system=1 ;;
		/usr/lib/* | /System/Library/Frameworks/*) ;;
		*) foreign+=("${dep}") ;;
	esac
done <<<"${deps}"

if [ "${system}" -eq 0 ]; then
	echo "otool reported no /usr/lib/libSystem.B.dylib dependency for ${bin}; every Mach-O executable has one, so this read no load commands and has verified nothing" >&2
	exit 1
fi

if [ "${#foreign[@]}" -gt 0 ]; then
	echo "${bin} depends on libraries outside the OS:" >&2
	printf '  %s\n' "${foreign[@]}" >&2
	exit 1
fi

# GOARCH is unpinned and the asset name is a published download URL, for the
# reason scripts/verify-release-linux.sh gives. Held to exactly one slice as
# well: this build is not universal, and a fat binary would mean the link picked
# up something that is.
archs="$(lipo -archs "${bin}")"
if [ "${archs}" != "arm64" ]; then
	echo "${bin} is Mach-O ${archs}, and the published asset is named ic11c_Darwin_arm64" >&2
	exit 1
fi

"$(dirname "${BASH_SOURCE[0]}")/verify-release-compiles.sh" "${bin}"

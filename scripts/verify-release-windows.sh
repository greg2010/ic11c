#!/usr/bin/env bash
# Fails if the Windows artifact imports any DLL that Windows does not ship: a
# partial link surfaces as a missing-DLL dialog on a user's machine. An allowlist
# rather than a blocklist, as in the macOS script. Unlike Linux and macOS this
# does not run the binary, because nothing in the container can execute a PE.
set -euo pipefail

bin="${1:-ic11c.exe}"
objdump="${OBJDUMP:-x86_64-w64-mingw32ucrt-objdump}"

# Its own statement, so a failing objdump aborts here rather than yielding an
# empty import list that every test below then passes.
imports="$("${objdump}" -p "${bin}" | sed -n 's/^[[:space:]]*DLL Name:[[:space:]]*//p' | sort -u)"

echo "${bin} imports:"

# Import table casing is not stable across linkers, and matching case
# insensitively is safe here because no two allowed names differ only in case.
shopt -s nocasematch

# KERNEL32.dll is tracked rather than merely allowed, for the reason
# scripts/verify-release-macos.sh gives about libSystem. Every Win32 PE imports
# it, so its absence means no import table was read.
system=0
foreign=()
while IFS= read -r dll; do
	[ -n "${dll}" ] || continue
	printf '  %s\n' "${dll}"
	case "${dll}" in
		KERNEL32.dll) system=1 ;;
		ADVAPI32.dll | ntdll.dll | ole32.dll | SHELL32.dll) ;;
		api-ms-win-crt-*.dll) ;;
		*) foreign+=("${dll}") ;;
	esac
done <<<"${imports}"

if [ "${system}" -eq 0 ]; then
	echo "${objdump} reported no KERNEL32.dll import for ${bin}; every PE has one, so this read no import table and has verified nothing" >&2
	exit 1
fi

if [ "${#foreign[@]}" -gt 0 ]; then
	echo "${bin} imports DLLs Windows does not ship:" >&2
	printf '  %s\n' "${foreign[@]}" >&2
	exit 1
fi

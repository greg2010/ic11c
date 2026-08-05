#!/usr/bin/env bash
# Fails unless every archive in dist/ carries both the executable and README.md.
# They are packaged in three places and nothing else reads inside them, so a
# packaging line that dropped a member publishes an archive of the right name
# with the wrong contents. README.md is the member that would go unnoticed.
set -euo pipefail

for pair in "ic11c_Linux_x86_64.tar.gz:ic11c" "ic11c_Darwin_arm64.tar.gz:ic11c" "ic11c_Windows_x86_64.zip:ic11c.exe"; do
	archive="${pair%:*}"
	executable="${pair#*:}"
	case "${archive}" in
	*.zip) members="$(unzip -Z1 "dist/${archive}")" ;;
	*) members="$(tar tzf "dist/${archive}")" ;;
	esac
	for member in "${executable}" README.md; do
		if ! printf '%s\n' "${members}" | grep -qxF "${member}"; then
			echo "dist/${archive} carries no ${member}:" >&2
			printf '%s\n' "${members}" >&2
			exit 1
		fi
	done
done

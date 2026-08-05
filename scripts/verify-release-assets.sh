#!/usr/bin/env bash
# Fails unless dist/ holds exactly the three platform artifacts, each non-empty.
# `if-no-files-found: error` catches an upload that matched nothing; this catches
# a download that dropped one, which the publish cannot see -- `gh release create
# dist/*` is as happy with two platforms as three. The count is asserted too.
set -euo pipefail

for asset in ic11c_Linux_x86_64.tar.gz ic11c_Darwin_arm64.tar.gz ic11c_Windows_x86_64.zip; do
	if [ ! -s "dist/${asset}" ]; then
		echo "dist/${asset} is missing or empty" >&2
		ls -l dist >&2
		exit 1
	fi
done

count="$(find dist -maxdepth 1 -type f | wc -l)"
if [ "${count}" -ne 3 ]; then
	echo "dist holds ${count} files, not the three platform artifacts" >&2
	ls -l dist >&2
	exit 1
fi

#!/usr/bin/env bash
# Fails unless the tagged commit is an ancestor of origin/master. A tag can be
# pushed at any commit and everything downstream reads the tag rather than a
# branch, so this is what stops a release cut from work that was never merged.
set -euo pipefail

git fetch origin master
if ! git merge-base --is-ancestor "${GITHUB_SHA}" origin/master; then
	echo "tag ${GITHUB_REF_NAME} points at a commit that is not on master" >&2
	exit 1
fi

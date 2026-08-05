#!/usr/bin/env bash
# Fails unless CI has completed for the tagged commit with at least one success
# and no failure. Every completed run is read rather than the most recent: a
# commit can carry a pull request run and the push that merged it, and taking one
# would let a passing rerun stand in for a sibling that failed.
set -euo pipefail

runs="$(gh run list --commit "${GITHUB_SHA}" --workflow ci.yml --status completed \
	--json conclusion,databaseId,event -L 50 --repo "${GITHUB_REPOSITORY}")"

failed="$(printf '%s' "${runs}" | jq '[.[] | select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "startup_failure")] | length')"
if [ "${failed}" -ne 0 ]; then
	echo "ci for ${GITHUB_SHA} has ${failed} failed run(s):" >&2
	printf '%s' "${runs}" | jq -r '.[] | "  \(.databaseId) \(.event) \(.conclusion)"' >&2
	exit 1
fi

passed="$(printf '%s' "${runs}" | jq '[.[] | select(.conclusion == "success")] | length')"
if [ "${passed}" -eq 0 ]; then
	echo "no successful ci run for ${GITHUB_SHA}; wait for ci before tagging" >&2
	printf '%s' "${runs}" | jq -r '.[] | "  \(.databaseId) \(.event) \(.conclusion)"' >&2
	exit 1
fi

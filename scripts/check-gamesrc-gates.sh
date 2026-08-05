#!/usr/bin/env bash
# Fails unless every test the packages reading the decompiled game source declare
# ran and passed, and unless the extractor still refuses a tree with no game in
# it. The output is asserted rather than the status, because `go test` exits 0
# over a package that skipped everything in it.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The two packages whose tests read the decompiled tree directly rather than
# through the chip cut from it.
packages=(./tools/isagen/ ./tools/chipgen/)

# No build tag: neither package reaches the LLVM bindings, and one spelt here
# would be a second forming of what the Taskfile's GO_TAGS owns. -count=1 because
# these are the only tests whose subject is a directory rather than the
# repository, so a cached answer can be about a game build no longer present.

# Under a runner this lands in the directory the job cleans up; anywhere else it
# is an ordinary temporary directory, so the script runs outside a workflow.
logs="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gates.XXXXXX")"

for pkg in "${packages[@]}"; do
	log="${logs}/gates-$(basename "${pkg}").log"
	go test -count=1 -timeout 300s -v "${pkg}" | tee "${log}"

	if grep -qE '^[[:space:]]*--- SKIP' "${log}"; then
		echo "a gate over the decompiled game source skipped, and the caller has a decompile for it to read" >&2
		exit 1
	fi

	# Nothing below names a test; the roster is what `go test -list` answers, so
	# a gate added beside these is covered and one renamed is followed. Its own
	# statement, because at the head of the filter a failed listing would read as
	# the filter selecting nothing.
	listed="$(go test -list '.*' "${pkg}")"
	declared="$(printf '%s\n' "${listed}" | sed -n 's/^\(Test[[:alnum:]_]*\)$/\1/p' | LC_ALL=C sort)"
	passed="$(sed -n 's/^--- PASS: \([[:alnum:]_]*\) .*/\1/p' "${log}" | LC_ALL=C sort)"
	if [ -z "${declared}" ]; then
		echo "${pkg} declares no tests, so this asserted nothing over it" >&2
		exit 1
	fi
	if [ "${declared}" != "${passed}" ]; then
		echo "${pkg} declares these and this run did not pass them:" >&2
		comm -23 <(printf '%s\n' "${declared}") <(printf '%s\n' "${passed}") >&2
		exit 1
	fi
done

blind="${logs}/gates-isagen-blind.log"
empty="$(mktemp -d "${logs}/no-decompile.XXXXXX")"
status=0
IC11C_GAMESRC="${empty}" go test -count=1 -timeout 300s -v ./tools/isagen/ >"${blind}" 2>&1 || status="$?"
if [ "${status}" -eq 0 ] && ! grep -qE '^[[:space:]]*--- SKIP' "${blind}"; then
	echo "tools/isagen passed every test over an empty decompiled tree, so nothing in it reads the game any more" >&2
	exit 1
fi

#!/usr/bin/env bash
# Requires the corpus manifests two or more separately linked artifacts wrote to
# record the same builds; nothing else in either workflow compares them. The
# libLLVM a manifest opens with is dropped before diffing, since no pin makes the
# three link one build, and printed instead: it separates drift from disagreement.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

reference="${1:?usage: compare-corpora.sh <reference-manifest> <manifest>...}"
shift

if [ "$#" -eq 0 ]; then
	echo "usage: compare-corpora.sh <reference-manifest> <manifest>...; one manifest compares against nothing" >&2
	exit 1
fi

rows="$(mktemp)"
reference_rows="$(mktemp)"
trap 'rm -f "${rows}" "${reference_rows}"' EXIT

# Required rather than printed if present: a run where every manifest named none
# would pass looking exactly like one where all three agreed.
echo "libLLVM each artifact was linked against:"
for manifest in "${reference}" "$@"; do
	if [ ! -s "${manifest}" ]; then
		echo "${manifest} is missing or empty; a platform that recorded nothing is not a platform that agrees" >&2
		exit 1
	fi
	# Resolved before it is printed rather than inside the printf, so there is
	# something to test: printf renders an absent header and a present one alike.
	toolchain="$(corpus_toolchain "${manifest}")"
	case "${toolchain}" in
		"")
			echo "${manifest} opens with no '# libLLVM:' line, so it does not say what linked the artifact that wrote it; a failure below would have no second reading and this comparison cannot tell one such manifest from another" >&2
			exit 1
			;;
		*$'\n'*)
			echo "${manifest} opens with more than one '# libLLVM:' line, so it names no single toolchain:" >&2
			printf '%s\n' "${toolchain}" | sed 's/^/  /' >&2
			exit 1
			;;
	esac
	printf '  %s %s\n' "${manifest}" "${toolchain}"
done

corpus_rows "${reference}" >"${reference_rows}"
if [ ! -s "${reference_rows}" ]; then
	echo "${reference} records no build; there is nothing to compare the others against" >&2
	exit 1
fi

for manifest in "$@"; do
	corpus_rows "${manifest}" >"${rows}"
	# Labelled, because the files diffed are the stripped copies and their
	# temporary names say nothing about which platform wrote what.
	if ! diff -u --label "${reference}" --label "${manifest}" "${reference_rows}" "${rows}"; then
		echo "${manifest} does not record what ${reference} does" >&2
		echo "compare the libLLVM versions printed above first: different ones make this a patch-level drift, equal ones leave a compiler that disagrees with itself across platforms" >&2
		exit 1
	fi
done

echo "all $(($# + 1)) artifacts agree on $(wc -l <"${reference_rows}") builds"

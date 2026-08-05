#!/usr/bin/env bash
# Fails if the Linux artifact carries any dynamic dependency. A partial link
# succeeds silently and surfaces as a missing-library error on a user's
# machine, so this gates both the release workflow and the release container.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

bin="$(local_path "${1:-ic11c}")"

# Its own statement, so a failing file(1) aborts here: folded into the filters
# below its status is the pipeline's first, which set -e ignores.
described="$(file "${bin}")"

# file(1) prints "<path>: <description>", and the tests below have to see only
# the description: a dynamically linked artifact named ic11c_x86-64_statically
# linked otherwise satisfies both of them out of its own name.
details="${described#*: }"

# Explicit conditionals rather than `! ... | grep`: bash exempts a command whose
# status is inverted by ! from set -e, so the negated form reports nothing.
if ! printf '%s\n' "${details}" | grep -q 'statically linked'; then
	echo "${bin} is not statically linked:" >&2
	printf '%s\n' "${described}" >&2
	exit 1
fi

# Neither producer pins GOARCH, so the build takes the architecture of whatever
# machine it ran on, and the asset name is a published download URL. Asserted
# against the ELF header rather than uname, which describes the builder.
if ! printf '%s\n' "${details}" | grep -q 'x86-64'; then
	echo "${bin} is not an x86-64 executable, and the published asset is named ic11c_Linux_x86_64:" >&2
	printf '%s\n' "${described}" >&2
	exit 1
fi

if ! command -v ldd >/dev/null 2>&1; then
	echo "ldd not found; ${bin}'s dynamic dependencies cannot be read" >&2
	exit 1
fi

# ldd exits non-zero on a non-PIE static executable, so only its listing carries
# an answer. Its own statement, so an ldd that printed nothing fails here rather
# than reading as a binary resolving nothing.
linked="$(ldd "${bin}" 2>&1 || true)"
if [ -z "${linked}" ]; then
	echo "ldd printed nothing for ${bin}; this read no dynamic section and has verified nothing" >&2
	exit 1
fi

if printf '%s\n' "${linked}" | grep -q '=>'; then
	echo "${bin} resolves shared libraries:" >&2
	printf '%s\n' "${linked}" >&2
	exit 1
fi

"$(dirname "${BASH_SOURCE[0]}")/verify-release-compiles.sh" "${bin}"

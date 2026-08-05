#!/usr/bin/env bash
# Prints the libLLVM major .llvm-version holds, and exports it to the steps that
# follow. The gate runs first because what follows writes the file's contents
# into every later step's environment, and a multi-line .llvm-version would
# inject whatever its second line spells as a variable of its own.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"${root}/scripts/check-llvm-version.sh"

# shellcheck source=release-common.sh
. "${root}/scripts/release-common.sh"

printf '%s\n' "${LLVM_VERSION}"

# Absent outside a workflow, where there is no later step to carry it to.
if [ -n "${GITHUB_ENV:-}" ]; then
	echo "LLVM_VERSION=${LLVM_VERSION}" >>"${GITHUB_ENV}"
fi

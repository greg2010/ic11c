#!/usr/bin/env bash
# Installs the libLLVM development headers and a matching clang driver for the
# major .llvm-version names, on a Debian or Ubuntu machine. A script rather than
# the CI action's inline shell, because actionlint does not hand a composite
# action's run blocks to shellcheck and this decides a signing key.
set -euo pipefail

release_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Read rather than taken as an argument with a default, which would be a second
# place the major is written down: an argument could install one major while the
# release scripts a caller runs next built against another.
version="$(cat "${release_root}/.llvm-version")"
case "${version}" in
	"" | *[!0-9]*)
		echo ".llvm-version holds '${version}', which is not a major version number" >&2
		exit 1
		;;
esac

# Written rather than handed to add-apt-repository, so this and the release
# container spell the one source line the same way, and nothing depends on
# software-properties-common being installed.
codename="$(lsb_release -cs)"

# Interpolated twice into the source line below, into the mirror path and the
# suite name, so an empty or malformed one names a suite no mirror serves and
# fails several steps later in apt's words. Ahead of the key rather than beside
# the line it guards, so a machine this refuses is left as it was found.
case "${codename}" in
	"" | *[!a-z]*)
		echo "lsb_release calls this machine's release '${codename}', which is not a Debian or Ubuntu codename; the LLVM source line is built out of it twice and would name a suite no mirror serves" >&2
		exit 1
		;;
esac

# Written somewhere of this script's own first, so a rejected key never reaches
# the keyring directory. The keyring is its own file named by the one source list
# that needs it: under trusted.gpg.d the key would be trusted for every
# repository, so a compromised mirror elsewhere could sign an LLVM package.
key="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/apt.llvm.org.asc"
"${release_root}/scripts/fetch-llvm-key.sh" "${key}"
sudo install -m 0644 "${key}" /usr/share/keyrings/apt.llvm.org.asc
rm -f "${key}"

echo "deb [signed-by=/usr/share/keyrings/apt.llvm.org.asc] https://apt.llvm.org/${codename}/ llvm-toolchain-${codename}-${version} main" \
	| sudo tee /etc/apt/sources.list.d/llvm.list >/dev/null
sudo apt-get update
sudo apt-get install -y "llvm-${version}-dev" "clang-${version}"

# llvm-N-dev is headers for the Go bindings, not a C driver, and a runner ships a
# clang of whatever version it pins -- so a bare `clang` would compile against a
# different LLVM than the build tag names. /usr/local/bin precedes /usr/bin on
# the runners, so this wins.
sudo ln -sf "/usr/bin/clang-${version}" /usr/local/bin/clang

# Exported so that the release scripts the job runs next take the major this
# installed rather than re-reading the file for themselves. Absent outside a
# workflow, where there is no later step to carry it to.
if [ -n "${GITHUB_ENV:-}" ]; then
	echo "LLVM_VERSION=${version}" >>"${GITHUB_ENV}"
fi

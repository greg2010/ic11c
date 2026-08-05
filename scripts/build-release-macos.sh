#!/usr/bin/env bash
# The macOS release link. LLVM comes from its static archives so the artifact
# needs no Homebrew install, but the binary is not fully static: Apple ships no
# static libSystem (QA1118), so OS dylibs remain. The Homebrew formula is `llvm`,
# not `llvm@22` -- a versioned formula appears only once that major is superseded.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

# An unstamped binary can be built and cannot be certified: the verification
# refuses this string outright.
: "${VERSION:=dev}"

if [ -z "${LLVM_CONFIG:-}" ]; then
	if ! command -v brew >/dev/null 2>&1; then
		echo "brew not found; set LLVM_CONFIG to an llvm-config for a static LLVM ${LLVM_VERSION}" >&2
		exit 1
	fi
	LLVM_CONFIG="$(brew --prefix llvm)/bin/llvm-config"
fi

assert_llvm_major "${LLVM_CONFIG}" "${LLVM_VERSION}"

out="${1:-ic11c}"

# Plain variables first, for the reason scripts/build-release-linux.sh gives.
# Polly is filtered because llvm-config lists it and the bottle omits it.
libs="$("${LLVM_CONFIG}" --link-static --libs \
	| tr ' ' '\n' \
	| grep -vE '^-l(Polly|PollyISL)$' \
	| tr '\n' ' ')"
cppflags="$("${LLVM_CONFIG}" --cppflags)"
libdir="$("${LLVM_CONFIG}" --libdir)"
zstd="$(brew --prefix zstd)/lib/libzstd.a"

export CGO_CPPFLAGS="${cppflags}"
export CGO_CXXFLAGS="-std=c++17"

# The bottle carries no per-component dylibs, so each -lLLVMFoo resolves to an
# archive unambiguously, and ld64 resolves archive cycles without --start-group.
# zstd is a Homebrew dependency rather than an OS library, so it is named by
# path. libc++ stays dynamic: Homebrew's LLVM is built against the SDK's copy.
export CGO_LDFLAGS="-L${libdir} -Wl,-dead_strip ${libs} ${zstd} -lz -lm -lc++"

# byollvm suppresses the binding layer's own flags, whose baked darwin paths
# name /opt/homebrew/opt/llvm@22, a directory Homebrew never creates. netgo and
# osusergo buy no self-containment here, since Go's cgo resolvers go through
# libSystem, but they keep both artifacts resolving names through the same code.
go build -tags "byollvm ${llvm_tag} netgo osusergo" \
	-ldflags "-s -w -X main.version=${VERSION}" \
	-o "${out}" ./cmd/ic11c

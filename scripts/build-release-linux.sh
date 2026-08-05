#!/usr/bin/env bash
# The Linux release link: fully static, so the artifact runs with no libLLVM
# installed. Requires the static LLVM component libraries, which distributions
# other than Debian and Ubuntu generally omit from their LLVM packages.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

# An unstamped binary can be built and cannot be certified: the verification
# refuses this string outright.
: "${VERSION:=dev}"

out="${1:-ic11c}"

# Debian and Ubuntu install a versioned name, others the bare one. Either is
# taken and then held to the major the build tag names, which is what makes
# accepting whatever is on PATH safe.
if [ -z "${LLVM_CONFIG:-}" ]; then
	for candidate in "llvm-config-${LLVM_VERSION}" llvm-config; do
		if command -v "${candidate}" >/dev/null 2>&1; then
			LLVM_CONFIG="${candidate}"
			break
		fi
	done
fi

if [ -z "${LLVM_CONFIG:-}" ] || ! command -v "${LLVM_CONFIG}" >/dev/null 2>&1; then
	echo "no llvm-config-${LLVM_VERSION} or llvm-config on PATH; install the LLVM ${LLVM_VERSION} development package or set LLVM_CONFIG" >&2
	exit 1
fi

assert_llvm_major "${LLVM_CONFIG}" "${LLVM_VERSION}"

# Plain variables first: `export X="$(cmd)"` takes the exit status of export, so
# a failing llvm-config would leave the flags empty and build against whatever
# headers were on the include path. Polly is filtered because llvm-config lists
# it and it ships in a separate libpolly-N-dev package.
libs="$("${LLVM_CONFIG}" --link-static --libs \
	| tr ' ' '\n' \
	| grep -vE '^-l(Polly|PollyISL)$' \
	| tr '\n' ' ')"
cppflags="$("${LLVM_CONFIG}" --cppflags)"
libdir="$("${LLVM_CONFIG}" --libdir)"

export CGO_CPPFLAGS="${cppflags}"

# Section garbage collection is required, not an optimization: the binding
# layer's LLVMInitializeAllTargetInfos expands to one call per backend, so the
# cgo wrapper references every LLVMInitialize<Target>TargetInfo though this
# compiler links none. Without --gc-sections the link fails on those symbols.
export CGO_CFLAGS="-ffunction-sections -fdata-sections"
export CGO_CXXFLAGS="-std=c++17 -ffunction-sections -fdata-sections"

# Hand-listed rather than from `llvm-config --system-libs`, which emits an
# absolute path to a *shared* libz3 and so silently reintroduces a dynamic
# dependency. Nothing this build links references libxml2, libedit, ncurses,
# libcurl, ICU or z3.
export CGO_LDFLAGS="-L${libdir} -Wl,--gc-sections \
	-Wl,--start-group ${libs} -Wl,--end-group \
	-lz -lzstd -lffi -lrt -ldl -lm -static-libgcc -static-libstdc++"

# byollvm suppresses the binding layer's own -I, -L and -lLLVM directives,
# leaving the variables above as the only source of flags. netgo and osusergo
# drop Go's cgo-backed resolvers, which pflag pulls in through its net.IP flag
# types: a static glibc warns about getaddrinfo unless those tags are set.
go build -tags "byollvm ${llvm_tag} netgo osusergo" \
	-ldflags "-s -w -extldflags \"-static\" -X main.version=${VERSION}" \
	-o "${out}" ./cmd/ic11c

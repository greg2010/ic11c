#!/usr/bin/env bash
# The Windows release link, cross-built from Linux: static but for the system
# DLLs, so the artifact runs with no MSYS2 installed. Requires a mingw-ABI LLVM,
# because cgo cannot consume the official MSVC-ABI releases; the static component
# archives come from the MSYS2 UCRT64 packages the container unpacks.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

: "${MSYS_PREFIX:=/msys/ucrt64}"
# An unstamped binary can be built and cannot be certified: the verification
# refuses this string outright.
: "${VERSION:=dev}"

out="${1:-ic11c.exe}"

if [ ! -d "${MSYS_PREFIX}/lib" ]; then
	echo "${MSYS_PREFIX}/lib not found; the MSYS2 UCRT64 packages are not unpacked" >&2
	exit 1
fi

gccdir="$(echo "${MSYS_PREFIX}"/lib/gcc/x86_64-w64-mingw32/*/)"
gccdir="${gccdir%/}"

# Guarded because an unmatched glob expands to itself, and a -B pointing nowhere
# fails as a wall of undefined symbols. The crt objects are reached through -B
# rather than named, because Go replays CGO_LDFLAGS once per cgo package and a
# named .o would then be linked more than once.
if [ ! -d "${gccdir}" ]; then
	echo "no gcc directory under ${MSYS_PREFIX}/lib/gcc/x86_64-w64-mingw32" >&2
	exit 1
fi

# MSYS2 ships llvm-config only as a Windows .exe, which the container cannot
# run, so the component list is the archive names and --start-group below makes
# their order irrelevant. nullglob and an explicit count, so an unpack that
# landed no LLVM archives says so rather than linking an empty component list.
shopt -s nullglob
archives=("${MSYS_PREFIX}"/lib/libLLVM*.a)
shopt -u nullglob

if [ "${#archives[@]}" -eq 0 ]; then
	echo "no libLLVM*.a in ${MSYS_PREFIX}/lib; the MSYS2 UCRT64 LLVM package is not unpacked" >&2
	exit 1
fi

llvm_libs=""
for archive in "${archives[@]}"; do
	name="${archive##*/}"
	name="${name#lib}"
	llvm_libs+="-l${name%.a} "
done

export CGO_CPPFLAGS="-I${MSYS_PREFIX}/include \
-D__STDC_CONSTANT_MACROS -D__STDC_FORMAT_MACROS -D__STDC_LIMIT_MACROS"
export CGO_CXXFLAGS="-std=c++17"

# -Bstatic is not redundant with an all-archive link: MSYS2 ships libfoo.dll.a
# beside libfoo.a and mingw ld prefers the import library, so a bare -lstdc++
# silently yields a libstdc++-6.dll dependency; -static-libgcc is the same case.
# The runtime group is spelled out because libwinpthread calls back into the UCRT.
export CGO_LDFLAGS="-B${gccdir}/ -B${MSYS_PREFIX}/lib/ \
-L${MSYS_PREFIX}/lib -L${gccdir} \
-static-libgcc -Wl,-Bstatic \
-Wl,--start-group ${llvm_libs} -Wl,--end-group \
-lz -lzstd -lffi -lxml2 -liconv -lcharset \
-lversion -lole32 -luuid -lpsapi -lshell32 -ladvapi32 -lntdll -lws2_32 \
-lstdc++ \
-Wl,--start-group -lmingw32 -lgcc -lgcc_eh -lmoldname -lmingwex -lucrt -lwinpthread -Wl,--end-group \
-lkernel32 -ladvapi32 -lshell32 -luser32"

export GOOS=windows GOARCH=amd64 CGO_ENABLED=1
export CC=x86_64-w64-mingw32ucrt-gcc CXX=x86_64-w64-mingw32ucrt-g++

# byollvm suppresses the binding layer's own -I, -L and -lLLVM directives,
# leaving the variables above as the only source of flags. netgo and osusergo are
# omitted where the other platforms set them: Go's Windows resolvers are already
# pure Go, so the tags would have no effect.
go build -tags "byollvm ${llvm_tag}" \
	-ldflags "-s -w -X main.version=${VERSION}" \
	-o "${out}" ./cmd/ic11c

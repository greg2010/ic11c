#!/usr/bin/env bash
# Sourced by the release build and verification scripts, not executed. The
# checks below exit rather than return, so a failure stops the caller rather than
# being left for it to notice.

# From this file rather than the working directory: the container runs these from
# /src, the workflow from the checkout, and a developer from wherever the tree is.
release_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# .llvm-version is the one place the libLLVM major is written down, and every
# spelling that can read a file derives from it. The ones that cannot are held to
# it by scripts/check-llvm-version.sh, which says what a missed bump would cost.
if [ -z "${LLVM_VERSION:-}" ]; then
	LLVM_VERSION="$(cat "${release_root}/.llvm-version")"
fi

case "${LLVM_VERSION}" in
	"" | *[!0-9]*)
		echo "LLVM_VERSION is '${LLVM_VERSION}', which is not a major version number" >&2
		exit 1
		;;
esac

# The build tag the Go bindings select a libLLVM major through. Formed here
# rather than in each script that passes it, so the shell side spells the rule
# once; scripts/check-llvm-version.sh refuses a third owner.
# shellcheck disable=SC2034 # read by the build scripts and by gopls-check.sh, which source this
llvm_tag="llvm${LLVM_VERSION}"

# Every build script interpolates this into a -ldflags string, which the Go
# toolchain splits on whitespace: a VERSION carrying a space stamps its first
# word and hands the rest to the linker as flags. Refused a leading dash, which
# would read as a flag wherever it is passed on. Left alone where it is unset.
case "${VERSION-}" in
	"") ;;
	-* | *[!A-Za-z0-9._+-]*)
		echo "VERSION is '${VERSION}', which is not a tag this can stamp: it reaches the binary through a -ldflags string, so it is held to letters, digits, dot, underscore, plus and dash, and may not open with a dash" >&2
		exit 1
		;;
esac

# What main.version reports when the -X stamp did not take, and what every build
# script falls back to for an unset VERSION.
# shellcheck disable=SC2034 # read by verify-release-compiles.sh, which sources this
unstamped_version=dev

# Rejects an llvm-config whose major is not the one the build tag names.
# Compiling the bindings against another major's headers is a silent wrong answer
# rather than a link error, and neither platform's default can be trusted:
# LLVM_CONFIG is overridable and Homebrew's `llvm` tracks whatever is current.
assert_llvm_major() {
	local config="$1" want="$2" found
	found="$("${config}" --version)"
	if [ "${found%%.*}" != "${want}" ]; then
		echo "${config} is LLVM ${found}, but this build targets ${want}" >&2
		exit 1
	fi
}

# Prefixes a bare filename with ./ so that the verification scripts run the
# artifact beside them rather than whatever the same name finds on PATH.
local_path() {
	case "$1" in
		*/*) printf '%s\n' "$1" ;;
		*) printf './%s\n' "$1" ;;
	esac
}

# Names the libLLVM an artifact was linked against, which nothing in the artifact
# reports: --version prints the release tag alone. The manifests record it so a
# difference between two platforms reads as patch-level drift rather than as a
# miscompile. This exits, so a caller must resolve it in a statement of its own.
llvm_build_id() {
	local artifact="$1" config version=""

	# Both branches converge on one emptiness check below rather than returning
	# where they find an answer, because an llvm-config that exits 0 printing
	# nothing yields a header no manifest can carry.
	case "${artifact}" in
		# Read from the lock: a .exe was cross-linked in the container and is
		# exercised on a Windows runner that ships an LLVM of its own, which an
		# llvm-config on PATH there would name instead.
		*.exe)
			version="$(sed -n \
				's/^mingw-w64-ucrt-x86_64-llvm mingw-w64-ucrt-x86_64-llvm-\([0-9][^-]*\)-.*/\1/p' \
				"${release_root}/scripts/msys2-ucrt64.lock")"
			if [ -z "${version}" ]; then
				echo "scripts/msys2-ucrt64.lock names no libLLVM version, so nothing here can say what ${artifact} links" >&2
				exit 1
			fi
			;;
		*)
			for config in "${LLVM_CONFIG:-}" "llvm-config-${LLVM_VERSION}" llvm-config; do
				if [ -n "${config}" ] && command -v "${config}" >/dev/null 2>&1; then
					version="$("${config}" --version)"
					break
				fi
			done

			# Homebrew keeps its LLVM off PATH, so on a Mac the loop above
			# finds nothing and this is the one that answers.
			if [ -z "${version}" ] && command -v brew >/dev/null 2>&1; then
				config="$(brew --prefix llvm)/bin/llvm-config"
				if [ -x "${config}" ]; then
					version="$("${config}" --version)"
				fi
			fi

			if [ -z "${version}" ]; then
				echo "no llvm-config on this machine named a version, so nothing here can say what ${artifact} links" >&2
				exit 1
			fi
			;;
	esac

	printf '%s\n' "${version}"
}

# The rows of a corpus manifest and nothing else. The comment lines it opens with
# carry the libLLVM that artifact links, which differs between platforms by
# design and would fail every comparison if diffed along with the rows.
corpus_rows() {
	sed '/^#/d' "$1"
}

# The libLLVM line a manifest opens with, and nothing where it carries none.
# Absence gets no word of its own: a word reads as an answer and compares equal
# to itself, so two manifests that recorded nothing would agree as readily as two
# that recorded the same thing. The callers reject the empty string instead.
corpus_toolchain() {
	sed -n 's/^# libLLVM: //p' "$1"
}

# Writes the SHA-256 of stdin. macOS ships shasum and no sha256sum, so neither
# name can be assumed by a script all three platforms run.
digest_stdin() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 | cut -d' ' -f1
	else
		echo "neither sha256sum nor shasum is on PATH; nothing here can digest what the artifact emitted" >&2
		exit 1
	fi
}

#!/usr/bin/env bash
# Exercises a release artifact instead of only inspecting it: --version is a
# cobra short-circuit reaching no LLVM code, so the linkage checks beside this
# leave nothing published having ever compiled a program. The whole corpus, in
# every mode; scripts/compare-corpora.sh owns the comparison of what it records.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

: "${VERSION:?set it to the version the binary was stamped with}"

# Required to differ from the unstamped string, not merely to be set: every build
# script falls back to it, so accepting it compares dev against dev and passes
# the very binary the version check below exists to catch.
if [ "${VERSION}" = "${unstamped_version}" ]; then
	echo "VERSION is '${unstamped_version}', which is what main.version reports when the -X stamp did not take; comparing it against itself verifies nothing" >&2
	exit 1
fi

bin="${1:?usage: verify-release-compiles.sh <binary>}"
# -f as well as -x: a directory is executable, so -x alone reports a path that
# names one as an executable file and then fails further down saying something
# else.
if [ ! -f "${bin}" ] || [ ! -x "${bin}" ]; then
	echo "${bin} is not an executable file" >&2
	exit 1
fi

# Resolved before the cd below. The Windows artifact is exercised from Git Bash,
# which rewrites an argument that looks like a POSIX path into a Windows one; the
# fixtures stay relative afterwards, being the same string under both.
bin="$(cd "$(dirname "${bin}")" && pwd)/$(basename "${bin}")"

# The manifest path is the caller's, so it is resolved against where the caller
# stood rather than against the root this cds to.
origin="$(pwd)"
manifest="${CORPUS_MANIFEST:-}"
case "${manifest}" in "" | /*) ;; *) manifest="${origin}/${manifest}" ;; esac

cd "${release_root}"

# -X main.version no-ops silently if that variable is renamed or moves package,
# shipping a release that reports 'dev'. The version template prints the bare
# string, so this compares whole rather than by substring.
reported="$("${bin}" --version)"
if [ "${reported}" != "${VERSION}" ]; then
	echo "${bin} reports version '${reported}', not '${VERSION}'; the -X main.version stamp did not take" >&2
	exit 1
fi

# Checked here rather than left to digest_stdin, which runs inside a command
# substitution: an exit there ends only the subshell, and the manifest would
# take the empty string as every digest and compare equal to another platform's
# copy of the same nothing.
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
	echo "neither sha256sum nor shasum is on PATH; nothing here can digest what the artifact emitted" >&2
	exit 1
fi

# Before the corpus rather than at the write below, and in a statement of its
# own: llvm_build_id exits, so through the printf's command substitution that
# would end only the subshell and the manifest would open with an empty libLLVM,
# which scripts/compare-corpora.sh refuses. Only where one is to be written.
toolchain=""
if [ -n "${manifest}" ]; then
	toolchain="$(llvm_build_id "${bin}")"
fi

# The shell side's own copy of internal/corpus's ModulePath, since shell cannot
# read a Go constant.
corpus=internal/corpus/programs

# Asked separately from the floor below, which cannot tell the two apart: under
# nullglob a corpus that moved and one that was emptied both arrive as zero
# fixtures, and the floor's wording would send a reader looking for deleted
# programs rather than for the copy of the path that went stale.
if [ ! -d "${corpus}" ]; then
	echo "${release_root}/${corpus} is not a directory; this gate carries its own spelling of where the corpus lives and nothing holds that spelling to the tree" >&2
	exit 1
fi

shopt -s nullglob
fixtures=("${corpus}"/*.c)
shopt -u nullglob

# A floor rather than an emptiness test. A glob left matching one fixture is the
# shape that passes: it clears every guard below and the three platforms agree on
# its four rows, so the comparison reports a corpus nothing was run over. Set
# below what the tree holds, so retiring a fixture stays an ordinary edit.
corpus_floor=20
if [ "${#fixtures[@]}" -lt "${corpus_floor}" ]; then
	echo "${release_root}/${corpus} holds ${#fixtures[@]} .c fixtures, fewer than the ${corpus_floor} this gate needs to have exercised the compiler over a corpus rather than over a handful" >&2
	exit 1
fi

# Under the C locale, because the manifests are diffed positionally and a glob
# expands in whatever order LC_COLLATE gives, on which the three runners do not
# agree. Sorted here rather than after the loop, so the order is the corpus and
# not the digests -- which would make one changed row read as a move.
sorted=()
while IFS= read -r fixture; do
	sorted+=("${fixture}")
done < <(printf '%s\n' "${fixtures[@]}" | LC_ALL=C sort)
fixtures=("${sorted[@]}")

asm="$(mktemp)"
report="$(mktemp)"
built="$(mktemp)"
trap 'rm -f "${asm}" "${report}" "${built}"' EXIT

# A function rather than an array of flags because the shipped mode passes none,
# and macOS ships bash 3.2, which cannot expand an empty array under set -u.
compile() {
	if [ -n "$2" ]; then
		"${bin}" "$2" "$1" >"${asm}" 2>"${report}"
	else
		"${bin}" "$1" >"${asm}" 2>"${report}"
	fi
}

# One number out of the size report, or the reason there was not one. sed prints
# every matching line, so a report carrying the program: line twice yields two
# numbers here, which a digit test alone would misreport as a count the artifact
# never printed.
single_number() {
	local what="$1" value="$2"
	case "${value}" in
		"")
			echo "${bin} printed no ${what} for ${fixture} under ${mode}; this read no size report and has verified nothing:" >&2
			;;
		*[!0-9]*)
			echo "${bin} printed '${value}' where ${fixture} under ${mode} needs one ${what}; a report stating it more than once, or as something other than a number, is not one this can compare against:" >&2
			;;
		*)
			return 0
			;;
	esac
	cat "${report}" >&2
	exit 1
}

# The one place the emission modes are written down, read by the compile loop and
# both guards after it: a mode added to the loop alone would be gated by nothing.
# Name and flag are one list of pairs rather than two arrays, because the shipped
# mode passes no flag and macOS bash 3.2 cannot expand an empty array under -u.
modes=(shipped: numeric:--numeric readable:--readable unoptimized:--no-optimize)

for fixture in "${fixtures[@]}"; do
	for entry in "${modes[@]}"; do
		mode="${entry%%:*}"
		flag="${entry#*:}"

		status=0
		compile "${fixture}" "${flag}" || status=$?

		emitted="$(awk 'END { print NR }' "${asm}")"

		# 0, 1 and 3 are the statuses a program can cause: compiled, refused,
		# and emitted over a budget. Anything else is this artifact failing
		# rather than the fixture. Which fixture lands on which is not asserted;
		# the manifest holds the platforms to agreeing on it.
		case "${status}" in
			0 | 3)
				# Stricter than cmd/ic11c's exit.go, which allows exitOK
				# over an empty stream. Such a program is legal and is not
				# in this corpus, and an empty stdout digests to the empty
				# digest on every platform and compares equal.
				if [ "${emitted}" -eq 0 ]; then
					echo "${bin} exited ${status} on ${fixture} under ${mode} and emitted nothing; every corpus fixture assembles to at least one line, and an empty stream cannot be told from a write that went nowhere:" >&2
					cat "${report}" >&2
					exit 1
				fi
				;;
			1)
				# --numeric and --readable decide how an accepted program is
				# rendered, not whether it compiles, so a refusal there is
				# this artifact. --no-optimize is the one mode where a refusal
				# is a fixture's property: a recursive local needs a slot.
				if [ "${mode}" != unoptimized ]; then
					echo "${bin} refused ${fixture} under ${mode}, which decides only how an accepted program is rendered:" >&2
					cat "${report}" >&2
					exit 1
				fi
				if [ "${emitted}" -ne 0 ]; then
					echo "${bin} refused ${fixture} under ${mode} and emitted ${emitted} lines anyway; a refused program leaves nothing to keep:" >&2
					cat "${asm}" >&2
					exit 1
				fi
				;;
			*)
				echo "${bin} exited ${status} on ${fixture} under ${mode}, which is not a status a program can cause:" >&2
				cat "${report}" >&2
				exit 1
				;;
		esac

		stated=-
		bytes=-
		if [ "${emitted}" -gt 0 ]; then
			# That the two streams describe one program: a truncated or empty
			# stdout fails against a count the report still states. Extracted
			# in their own statements so a report these patterns no longer
			# match fails here instead of comparing against an empty count.
			stated="$(sed -n 's/^program: .* \([0-9][0-9]*\) of [0-9][0-9]* lines .*/\1/p' "${report}")"
			bytes="$(sed -n 's/^program: \([0-9][0-9]*\) of [0-9][0-9]* bytes .*/\1/p' "${report}")"
			single_number "line count" "${stated}"
			single_number "byte count" "${bytes}"
			if [ "${emitted}" -ne "${stated}" ]; then
				echo "${bin} compiled ${fixture} under ${mode} to ${emitted} lines of assembly while reporting ${stated}:" >&2
				cat "${asm}" >&2
				cat "${report}" >&2
				exit 1
			fi
		fi

		# In a statement of its own, for the reason toolchain above is: through
		# the printf's command substitution a failed digest would open the row
		# with an empty field, shifting every column after it.
		digest="$(digest_stdin <"${asm}")"
		if [ -z "${digest}" ]; then
			echo "nothing digested what ${bin} emitted for ${fixture} under ${mode}; an empty digest is what every platform reproduces and every comparison passes" >&2
			exit 1
		fi

		printf '%s %s %s %s %s %s\n' \
			"${digest}" "${status}" "${stated}" "${bytes}" "${mode}" "${fixture}" >>"${built}"
	done
done

# What bounds how degenerate a manifest may be: every guard above is about one
# build. An artifact refusing a whole mode writes that mode's rows as the empty
# digest, which every platform reproduces and the comparison passes. unoptimized
# is the mode where a refusal is legitimate, so the one that could empty out.
for entry in "${modes[@]}"; do
	mode="${entry%%:*}"

	if ! awk -v mode="${mode}" '$5 == mode && $3 != "-" { found = 1 } END { exit !found }' "${built}"; then
		echo "no fixture in the corpus produced assembly under ${mode}; that mode contributes ${#fixtures[@]} empty digests, which every platform reproduces and every comparison passes" >&2
		exit 1
	fi

	if [ "${mode}" = shipped ]; then
		continue
	fi

	# One level up: that guard sees a mode emptied out, this one a mode that
	# stopped being a mode. A flag left unwired renders every fixture as
	# shipped does, so its rows are real digests every platform reproduces.
	# Two passes so the answer does not depend on shipped leading the list.
	if ! awk -v mode="${mode}" '
		NR == FNR { if ($5 == "shipped") shipped[$6] = $1; next }
		$5 == mode && ($6 in shipped) && $1 != shipped[$6] { differs = 1 }
		END { exit !differs }' "${built}" "${built}"; then
		echo "every fixture in the corpus compiles identically under ${mode} and under shipped; that flag decides nothing, so its ${#fixtures[@]} rows are a second copy of the shipped rows that every platform reproduces and every comparison passes" >&2
		exit 1
	fi
done

echo "${bin} compiled ${#fixtures[@]} fixtures in ${#modes[@]} modes"

if [ -n "${manifest}" ]; then
	# Leads the file rather than sitting beside it, so a manifest cannot reach
	# a comparison without saying what produced it. The comparison drops
	# comment lines before diffing, because the three artifacts link three
	# independently versioned libLLVM builds by design.
	{
		printf '# libLLVM: %s\n' "${toolchain}"
		cat "${built}"
	} >"${manifest}"
	echo "wrote ${manifest}"
fi

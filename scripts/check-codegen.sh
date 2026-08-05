#!/usr/bin/env bash
# The two gates on the checked-in derived files: that each is what its generator
# writes today, and that none shares a Go package with hand-written source. Each
# generator renders into a scratch tree that is diffed against the working tree,
# because asking git what changed compares against the index instead.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

scratch="$(mktemp -d)"
tools="$(mktemp -d)"
trap 'rm -rf "$scratch" "$tools"' EXIT

drifted=0
misplaced=0

# declared collects every path a generator names, read or written. The placement
# gate needs no list of its own because of it: a generator that gains an
# artifact declares it here to be diffed, and the same declaration places it.
declared=()

# gate builds one generator, runs its generate subcommand over the inputs its
# own flags name, and diffs every output it declares against the working tree.
# The second argument is the command a reader is told to run when one drifted.
gate() {
	local pkg="$1" regen="$2"
	local name
	name="$(basename "$pkg")"

	# Built rather than `go run`, because it is run twice and from a directory
	# that is not the module.
	go build -o "$tools/$name" "$pkg"

	# What gets gated is derived from the generate command's own flags rather
	# than listed here. A path-valued flag saying neither what it reads nor what
	# it writes, or saying both, fails below rather than going ungated.
	local classified
	classified="$("$tools/$name" generate --help | awk '
		match($0, /--[a-z0-9-]+ string/) {
			flag = substr($0, RSTART + 2, RLENGTH - 9)
			path = ""
			if (match($0, /\(default "[^"]*"\)$/)) {
				path = substr($0, RSTART + 10, RLENGTH - 12)
			}
			# A placeholder rather than an empty field: tab is IFS whitespace, so a
			# leading empty one collapses and the read below shifts the flag into
			# the mode, naming a flag that does not exist.
			mode = "unclassified"
			writes = index($0, " to write ")
			reads = index($0, " to read ")
			if (writes && reads) {
				mode = "ambiguous"
			} else if (writes) {
				mode = "write"
			} else if (reads) {
				mode = "read"
			}
			print mode "\t" flag "\t" path
		}
	')"

	if [ -z "$classified" ]; then
		echo "check-codegen: $name generate declares no path flags, so this gate would compare nothing" >&2
		exit 1
	fi

	local inputs=() outputs=() mode flag path
	while IFS=$'\t' read -r mode flag path; do
		if [ "$mode" = "unclassified" ] || [ -z "$path" ]; then
			echo "check-codegen: $name generate --$flag is a path this gate cannot place; its help has to say what it reads or what it writes, and name the file it defaults to" >&2
			exit 1
		fi
		if [ "$mode" = "ambiguous" ]; then
			echo "check-codegen: $name generate --$flag says both what it reads and what it writes, so this gate cannot place it; its help has to say one or the other" >&2
			exit 1
		fi
		declared+=("$path")
		case "$mode" in
		read) inputs+=("--$flag" "$root/$path") ;;
		write) outputs+=("$path") ;;
		esac
	done <<<"$classified"

	if [ "${#outputs[@]}" -eq 0 ]; then
		echo "check-codegen: $name generate writes nothing this gate can diff" >&2
		exit 1
	fi

	# A tree per generator, so the check below that every file written was one a
	# flag named stays total rather than meeting what another generator left.
	local tree="$scratch/$name"
	mkdir -p "$tree"

	# Inputs named absolutely and everything else left to default, so the scratch
	# tree takes the layout the real one has. isagen's argument files name the
	# prelude relative to themselves, so rendering elsewhere would report drift.
	(cd "$tree" && "$tools/$name" generate "${inputs[@]}")

	local out
	for out in "${outputs[@]}"; do
		if ! diff -u --label "$out" --label "$out (regenerated)" "$out" "$tree/$out"; then
			echo "$out is not what $name generate writes now; run: $regen" >&2
			drifted=1
		fi
	done

	# Its own statement rather than a process substitution, whose status the loop
	# would discard: a walk that failed would leave nothing to read, and this check
	# passes on an empty listing by construction.
	local rendered written
	rendered="$(cd "$tree" && find . -type f | sed 's|^\./||' | sort)"

	while IFS= read -r written; do
		[ -n "$written" ] || continue
		for out in "${outputs[@]}"; do
			if [ "$written" = "$out" ]; then
				continue 2
			fi
		done
		echo "check-codegen: $name generate wrote $written, which no flag of its own names, so nothing gates it" >&2
		drifted=1
	done <<<"$rendered"
}

# generatedBanner is the line a generator opens its output with, and the wording
# go/build and golangci-lint recognise a file by. Only the first line is read:
# the generators hold this same text as the string they write, and a fixture
# holding a rendered artifact is a fixture rather than an artifact.
generatedBanner='^[[:space:]]*([/#;*-]+[[:space:]]*)?Code generated .* DO NOT EDIT\.[[:space:]]*$'

# derivedFile answers whether one path is a file nobody wrote by hand.
derivedFile() {
	local path="$1" entry first=""
	for entry in "${declared[@]}"; do
		if [ "$path" = "$entry" ]; then
			return 0
		fi
	done
	IFS= read -r first <"$path" || true
	[[ "$first" =~ $generatedBanner ]]
}

# placement fails where a directory Go compiles as a package holds derived files
# and hand-written ones at once. A rule rather than a list of paths: what it reads
# as derived is what the generators declare, plus the banner above.
placement() {
	# The package directory is the unit because it is the unit of everything a
	# mixture misleads: go:embed, `go build`, the linter's exemption for
	# generated files and the coverage profile. A directory Go compiles nothing
	# in is not scanned, which leaves the MicroC corpus alone.
	local packages dir file problems
	# From the toolchain rather than from a walk, so that what is scanned is what
	# is built. It is also what keeps testdata out, where a rendered artifact is
	# a golden fixture and belongs beside the test reading it.
	packages="$(go list -f '{{.Dir}}' ./... | sed "s|^$root/||;s|^$root\$|.|" | LC_ALL=C sort)"
	if [ -z "$packages" ]; then
		echo "check-codegen: the toolchain listed no package, so the placement gate scanned nothing" >&2
		exit 1
	fi

	local -A isPackage=() derivedIn=() handwrittenIn=()
	while IFS= read -r dir; do
		if [ -n "$dir" ]; then
			isPackage["$dir"]=1
		fi
	done <<<"$packages"

	while IFS= read -r file; do
		[ -f "$file" ] || continue
		# A path with no separator is at the module root, which git names bare
		# and the listing above names as a dot.
		dir="."
		if [ "$file" != "${file#*/}" ]; then
			dir="${file%/*}"
		fi
		[ -n "${isPackage[$dir]:-}" ] || continue
		if derivedFile "$file"; then
			derivedIn["$dir"]="${derivedIn[$dir]:-}${file##*/} "
		else
			handwrittenIn["$dir"]=$((${handwrittenIn[$dir]:-0} + 1))
		fi
	done < <(git ls-files --cached --others --exclude-standard)

	# The derived files are named and the hand-written ones counted: the derived
	# ones are what has to move, and a package's whole file list would bury them.
	problems="$(
		for dir in "${!derivedIn[@]}"; do
			if [ -n "${handwrittenIn[$dir]:-}" ]; then
				echo "$dir holds ${derivedIn[$dir]}beside ${handwrittenIn[$dir]} hand-written files"
			fi
		done | LC_ALL=C sort
	)"
	if [ -n "$problems" ]; then
		echo "check-codegen: a derived file may not share a Go package with hand-written source; move it to a directory of its own and repoint whatever names it:" >&2
		printf '%s\n' "$problems" >&2
		misplaced=1
	fi
}

gate ./tools/isagen "task isa:generate"
gate ./tools/nodegen "go run ./tools/nodegen generate"

# After the gates, which is what fills declared.
placement

if [ "$drifted" -ne 0 ] || [ "$misplaced" -ne 0 ]; then
	exit 1
fi

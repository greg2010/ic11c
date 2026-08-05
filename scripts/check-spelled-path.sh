#!/usr/bin/env bash
# Fails if the tree and Taskfile.yml disagree about where a path lives: a file
# accounted for that no longer spells the text it says it does, or a file naming
# the path that nothing accounts for. Both directions, because either alone reads
# as clean while half a move is outstanding.
set -euo pipefail

if [ "$#" -lt 2 ]; then
	echo "usage: ${0##*/} <label> <path> [skipped]... -- the accounted list is read from stdin, one 'file literal' line per file Taskfile.yml accounts for" >&2
	exit 1
fi

# On stdin rather than as an argument, because an entry holding a quote would
# close the quote Taskfile.yml has to wrap it in and shred every entry after it.
label="$1"
needle="$2"
shift 2
skipped="$(printf '%s\n' "$@")"

# Before anything else reads a stream, so that no later command consumes part of
# the list. An empty one is a caller whose list did not arrive, which would
# otherwise report every file naming the path as one nothing accounts for.
pairs="$(cat)"
if [ -z "${pairs//[[:space:]]/}" ]; then
	echo "no accounted list arrived on stdin, so nothing could be held to Taskfile.yml" >&2
	exit 1
fi

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# On strings rather than by piping into `grep -q`, which exits on the first
# match and leaves the writer a broken pipe: under pipefail that status is the
# pipeline's, and a match would read as a miss.
contains_line() {
	local haystack=$'\n'"$1"$'\n'
	case "${haystack}" in
	*$'\n'"$2"$'\n'*) return 0 ;;
	esac
	return 1
}

# What git would show, so everything ignored is out. Its own statement rather
# than the head of a pipeline, so a failed enumeration is reported as one rather
# than as a short list.
tracked="$(git ls-files --cached --others --exclude-standard)"

files=()
while IFS= read -r file; do
	[ -f "${file}" ] || continue
	if contains_line "${skipped}" "${file}"; then
		continue
	fi
	files+=("${file}")
done <<<"${tracked}"

# A scan that reached nothing is one that stopped working rather than a tree
# holding no files, and it would otherwise report every accounted path as
# unreached and read as a move nobody finished.
if [ "${#files[@]}" -eq 0 ]; then
	echo "nothing in the tree could be scanned for ${needle}" >&2
	exit 1
fi

status=0
found="$(grep -lIwF -- "${needle}" "${files[@]}")" || status="$?"
case "${status}" in
0 | 1) ;;
*)
	echo "scanning the tree for ${needle} failed: grep exited ${status}" >&2
	exit 1
	;;
esac
naming="$(LC_ALL=C sort <<<"${found}")"
accounted="$(cut -d' ' -f1 <<<"${pairs}" | LC_ALL=C sort -u)"

problems=""

# Each accounted path rather than the directory name alone, because most of
# these files also name the path in prose. Each is held to being followed by
# something that cannot continue a path, so a longer name does not satisfy a
# shorter one.
while IFS= read -r pair; do
	[ -n "${pair}" ] || continue
	file="${pair%% *}"
	literal="${pair#* }"
	if [ ! -f "${file}" ]; then
		problems+="${file} is accounted for in Taskfile.yml and is not in the tree"$'\n'
		continue
	fi
	status=0
	grep -qE -- "${literal}([^-./_[:alnum:]]|\$)" "${file}" || status="$?"
	case "${status}" in
	0) ;;
	1) problems+="${file} no longer spells ${literal}"$'\n' ;;
	*) problems+="reading ${file} failed: grep exited ${status}"$'\n' ;;
	esac
done <<<"${pairs}"

while IFS= read -r file; do
	[ -n "${file}" ] || continue
	contains_line "${naming}" "${file}" ||
		problems+="${file} is accounted for in Taskfile.yml and the scan for ${needle} did not reach it"$'\n'
done <<<"${accounted}"

while IFS= read -r file; do
	[ -n "${file}" ] || continue
	contains_line "${accounted}" "${file}" ||
		problems+="${file} names ${needle} and nothing in Taskfile.yml accounts for it"$'\n'
done <<<"${naming}"

if [ -n "${problems}" ]; then
	echo "the tree and Taskfile.yml disagree about where ${label} lives:" >&2
	printf '%s' "${problems}" >&2
	exit 1
fi

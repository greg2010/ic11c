#!/usr/bin/env bash
# Fails if anything in the build machinery names a libLLVM major other than the
# one .llvm-version holds. Most spellings derive from that file; what is left is
# configuration that can read nothing. A bump that missed one would compile the
# tests against one major's bindings while the release linked another's library.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

cd "${release_root}"

failed=0

# The build machinery, and the roots every scan below reads. One list rather
# than a repeated set of arguments, so that what counts as machinery cannot come
# to mean three different things in one file.
scan_roots=(
	.github
	Dockerfile
	Taskfile.yml
	scripts
)

# Everything else at the top of the tree, named rather than skipped by default.
# .golangci.yml and .llvm-version are here because the checks below already hold
# them. The rest does not decide which library a build links, and the scans strip
# `#` comments and not `//`, so reading the Go tree would report sentences.
unscanned=(
	.codecov.yml
	.dockerignore
	.gitattributes
	.gitignore
	.golangci.yml
	.llvm-version
	README.md
	cmd
	go.mod
	go.sum
	internal
	tools
)

# Both directions, because neither alone says the lists still describe the tree:
# an entry in neither is scanned by nothing and reads exactly like a clean tree.
# Both lists name top-level entries, which is what the comparison reads; a path
# further down would be reported as missing on every run.
top_level="$(git ls-files --cached --others --exclude-standard | cut -d/ -f1 | LC_ALL=C sort -u)"
present="$(printf '%s\n' "${top_level}" | while IFS= read -r entry; do
	if [ -e "${entry}" ]; then printf '%s\n' "${entry}"; fi
done)"
if [ -z "${present}" ]; then
	echo "git listed nothing at the top of the tree, so this compared the roots against nothing" >&2
	exit 1
fi

classified="$(printf '%s\n' "${scan_roots[@]}" "${unscanned[@]}" | LC_ALL=C sort -u)"

# comm under the caller's collation against lists sorted under C compares two
# orders, which it reports where it notices and otherwise answers wrongly in
# silence. Both are pinned to the collation the sorts above used.
unclassified="$(LC_ALL=C comm -23 <(printf '%s\n' "${present}") <(printf '%s\n' "${classified}"))"
if [ -n "${unclassified}" ]; then
	echo "these are at the top of the tree and are neither scanned nor named exempt above, so a build tag arriving in one would be read by nothing:" >&2
	printf '%s\n' "${unclassified}" >&2
	failed=1
fi

departed="$(LC_ALL=C comm -13 <(printf '%s\n' "${present}") <(printf '%s\n' "${classified}"))"
if [ -n "${departed}" ]; then
	echo "these are named above and are not in the tree, so the lists describe a layout this is no longer run against:" >&2
	printf '%s\n' "${departed}" >&2
	failed=1
fi

# golangci-lint takes its build tags from configuration or from the command
# line, and interpolates neither. `task lint` passes them, so this is what holds
# a bare `golangci-lint run` to linting the same code.
tags="$(sed -n 's/^[[:space:]]*build-tags:[[:space:]]*\[\(.*\)\][[:space:]]*$/\1/p' .golangci.yml)"
if [ -z "${tags}" ]; then
	echo ".golangci.yml sets no run.build-tags, so a bare 'golangci-lint run' lints with none of them" >&2
	failed=1
elif [ "${tags}" != "${llvm_tag}" ]; then
	echo ".golangci.yml lints with build tags '${tags}', but .llvm-version says ${llvm_tag}" >&2
	failed=1
fi

# Workflow and action YAML cannot read a file from an `env:` block, so a literal
# there is a spelling nothing would correct. Both `:` and `=` are matched, since
# a --build-arg on a docker build line is the same literal reaching the release
# container. Comments are stripped: prose is not a build linking a wrong library.
assignment_shape="LLVM_VERSION[[:space:]]*[:=][[:space:]]*['\"]?[0-9]"
assignments="$(grep -rnE "${assignment_shape}" "${scan_roots[@]}" || [ "$?" -eq 1 ])"
literals="$(printf '%s\n' "${assignments}" \
	| sed 's/#.*$//' \
	| grep -E "${assignment_shape}" || [ "$?" -eq 1 ])"
if [ -n "${literals}" ]; then
	echo "these name an LLVM major that .llvm-version cannot move; read the file instead:" >&2
	printf '%s\n' "${literals}" >&2
	failed=1
fi

# What the scan above cannot find is a major spelled straight into a value, which
# is where it matters most: GO_TAGS is what `task test` compiles with and what
# `task lint` passes as --build-tags, overriding .golangci.yml and the check
# above. An llvm-config-N or a Homebrew llvm@N reaches the same place.
tag_shape='llvm(@|-config-)?[0-9]'
scanned="$(grep -rnE "${tag_shape}" "${scan_roots[@]}" || [ "$?" -eq 1 ])"
tag_literals="$(printf '%s\n' "${scanned}" \
	| sed 's/#.*$//' \
	| grep -E "${tag_shape}" || [ "$?" -eq 1 ])"
if [ -n "${tag_literals}" ]; then
	echo "these spell an LLVM major into a value that .llvm-version cannot move; derive it from the file instead:" >&2
	printf '%s\n' "${tag_literals}" >&2
	failed=1
fi

# This one finds the rule that turns a major into the build tag, which neither
# scan above reports because neither names a version. Two places are entitled to
# it, one per language, because Task templating cannot read a shell variable and
# a script cannot read a task variable. Moving either fails this.
formation_shape='llvm(\$\{|\$\(|\{\{)'
formations="$(grep -rnE "${formation_shape}" "${scan_roots[@]}" || [ "$?" -eq 1 ])"
formed="$(printf '%s\n' "${formations}" \
	| sed 's/#.*$//' \
	| grep -E "${formation_shape}" || [ "$?" -eq 1 ])"
elsewhere="$(printf '%s\n' "${formed}" | grep -vE '^(Taskfile\.yml|scripts/release-common\.sh):' || [ "$?" -eq 1 ])"
if [ -n "${elsewhere}" ]; then
	echo "these form the build tag out of a major, which Taskfile.yml and scripts/release-common.sh already do; read what they hold instead:" >&2
	printf '%s\n' "${elsewhere}" >&2
	failed=1
fi

exit "${failed}"

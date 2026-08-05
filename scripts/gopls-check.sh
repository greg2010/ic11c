#!/usr/bin/env bash
# gopls reports diagnostics golangci-lint does not, including modernization
# hints. It takes file paths rather than package patterns, hence the go list.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

# `task lint` passes this. The fallback is the tag the file sourced above forms
# out of .llvm-version rather than one spelled here, so a direct run cannot
# analyse a different major's build than the tests compile.
: "${GO_TAGS:=${llvm_tag}}"
export GOFLAGS="-tags=${GO_TAGS}"

# Test files are listed alongside the rest, because the verification harness is
# the larger half of this tree. Generated files are excluded: their hints are for
# tools/isagen's renderer, and the output is not hand-editable.
diagnostics="$(
	go list -f '{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}' ./... \
		| grep -v -e '\.gen\.go$' \
		| xargs gopls check -severity=hint
)"

# gopls check exits zero whatever it found, so the verdict is the output rather
# than the status. Without this the gate prints its findings and passes.
if [ -n "$diagnostics" ]; then
	echo "$diagnostics" >&2
	exit 1
fi

#!/usr/bin/env bash
# Writes dist/checksums.txt over the release artifacts and prints it. Bare names
# rather than ./-prefixed ones, so `sha256sum -c checksums.txt` verifies a
# downloaded set from whatever directory it was unpacked into.
set -euo pipefail

cd dist
# shellcheck disable=SC2035 # the names are the three already asserted, none of which starts with a dash
sha256sum * > checksums.txt
cat checksums.txt
